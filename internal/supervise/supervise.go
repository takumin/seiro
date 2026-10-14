package supervise

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
)

// Timeouts used while starting and stopping children.
var (
	SocketTimeout = 10 * time.Second
	StopTimeout   = 5 * time.Second
)

// Run supervises the planned machine until the VMM exits. It is the body of the
// hidden "seiro supervise" command.
func Run(ctx context.Context, p *plan.Machine) error {
	s := &supervisor{plan: p, status: &Status{
		State:         StateStarting,
		SupervisorPID: os.Getpid(),
		BackendPIDs:   map[string]int{},
		StartedAt:     time.Now().UTC(),
	}}
	return s.run(ctx)
}

// child is a started process whose exit is observed in the background.
type child struct {
	cmd  *exec.Cmd
	log  *os.File
	done chan struct{}
	err  error
}

func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

type supervisor struct {
	plan     *plan.Machine
	status   *Status
	backends []*child
}

func (s *supervisor) run(ctx context.Context) (err error) {
	defer func() {
		s.stopBackends()
		// The runtime directory only holds sockets and the lock files that
		// the VMM and the backends create next to them.
		os.RemoveAll(s.plan.RuntimeDir) //nolint:errcheck,gosec // best effort
		now := time.Now().UTC()
		s.status.State = StateStopped
		s.status.StoppedAt = &now
		if err != nil {
			s.status.Error = err.Error()
		}
		if werr := s.writeStatus(); werr != nil {
			err = errors.Join(err, werr)
		}
	}()

	if err := os.RemoveAll(s.plan.RuntimeDir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.plan.RuntimeDir, 0o700); err != nil {
		return err
	}
	if err := s.writeStatus(); err != nil {
		return err
	}

	for _, n := range s.plan.NICs {
		c, err := start(n.Backend)
		if err != nil {
			return fmt.Errorf("start passt for %s: %w", n.Name, err)
		}
		s.backends = append(s.backends, c)
		s.status.BackendPIDs[n.Name] = c.cmd.Process.Pid
		if err := waitSocket(n.Socket, c); err != nil {
			return fmt.Errorf("passt for %s: %w (see %s)", n.Name, err, n.Backend.Log)
		}
	}

	if d := s.plan.VSockDevice; d != nil {
		c, err := start(d.Backend)
		if err != nil {
			return fmt.Errorf("start vsock device: %w", err)
		}
		s.backends = append(s.backends, c)
		s.status.BackendPIDs[VSockBackend] = c.cmd.Process.Pid
		if err := waitSocket(d.Socket, c); err != nil {
			return fmt.Errorf("vsock device: %w (see %s)", err, d.Backend.Log)
		}
	}

	name := filepath.Base(s.plan.VMM.Path)
	vmm, err := start(s.plan.VMM)
	if err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	s.status.VMMPID = vmm.cmd.Process.Pid
	abort := func(err error) error {
		vmm.cmd.Process.Kill() //nolint:errcheck,gosec // best effort
		<-vmm.done
		return err
	}

	if err := waitSocket(s.plan.SerialSocket, vmm); err != nil {
		return abort(fmt.Errorf("%s: %w (see %s)", name, err, s.plan.VMM.Log))
	}
	serial, err := net.Dial("unix", s.plan.SerialSocket)
	if err != nil {
		return abort(fmt.Errorf("connect serial: %w", err))
	}
	defer serial.Close() //nolint:errcheck // closed by the VMM on exit

	serialLog, err := OpenRotating(s.plan.SerialLog, DefaultLogSize)
	if err != nil {
		return abort(err)
	}
	defer serialLog.Close() //nolint:errcheck // log file

	console, err := net.Listen("unix", s.plan.ConsoleSocket)
	if err != nil {
		return abort(fmt.Errorf("listen console: %w", err))
	}
	defer console.Close() //nolint:errcheck // socket removed on exit

	relay := NewRelay(serial, serialLog)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := relay.Run(); err != nil {
			slog.Warn("serial relay stopped", slog.Any("error", err))
		}
	})
	go relay.Serve(console) //nolint:errcheck // stops when the listener is closed

	s.status.State = StateRunning
	if err := s.writeStatus(); err != nil {
		slog.Warn("failed to write status", slog.Any("error", err))
	}
	slog.Info("machine running", slog.String("machine", s.plan.Name), slog.Int("vmm_pid", vmm.cmd.Process.Pid))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)

loop:
	for {
		select {
		case <-vmm.done:
			break loop
		case sig := <-sigs:
			slog.Info("stopping "+name, slog.String("signal", sig.String()))
			vmm.cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck,gosec // best effort
		case <-ctx.Done():
			vmm.cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck,gosec // best effort
		}
	}
	s.recordExit(vmm.err)
	slog.Info(name+" exited", slog.Any("exit_code", s.status.ExitCode), slog.String("signal", s.status.ExitSignal))

	// Let the relay drain what is left into the log.
	serial.Close() //nolint:errcheck,gosec // unblock the relay if the socket is still open
	wg.Wait()
	return nil
}

func start(p plan.Process) (*child, error) {
	log, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- log path from the plan
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(log, "--- %s %s %q\n", time.Now().UTC().Format(time.RFC3339), p.Path, p.Args) //nolint:errcheck,gosec // log header
	cmd := exec.Command(p.Path, p.Args...)                                                    // #nosec G204 -- binaries and arguments come from the plan
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close() //nolint:errcheck,gosec // already failing
		return nil, err
	}
	c := &child{cmd: cmd, log: log, done: make(chan struct{})}
	go func() {
		c.err = cmd.Wait()
		log.Close() //nolint:errcheck,gosec // log file
		close(c.done)
	}()
	return c, nil
}

func (s *supervisor) recordExit(err error) {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code := 0
		s.status.ExitCode = &code
	case errors.As(err, &exitErr):
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			s.status.ExitSignal = ws.Signal().String()
		} else {
			code := exitErr.ExitCode()
			s.status.ExitCode = &code
		}
	default:
		s.status.Error = err.Error()
	}
}

func (s *supervisor) stopBackends() {
	for _, c := range s.backends {
		c.cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck,gosec // may have exited already
		select {
		case <-c.done:
		case <-time.After(StopTimeout):
			c.cmd.Process.Kill() //nolint:errcheck,gosec // best effort
			<-c.done
		}
	}
}

func (s *supervisor) writeStatus() error {
	return env.WriteJSON(s.plan.Status, s.status)
}

// waitSocket waits until a UNIX socket appears at path, failing early if c exits.
func waitSocket(path string, c *child) error {
	deadline := time.Now().Add(SocketTimeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if c.exited() {
			return errors.New("exited before creating its socket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %s", path, SocketTimeout)
}
