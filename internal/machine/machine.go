// Package machine orchestrates the lifecycle of the machines of an environment.
package machine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/takumin/seiro/internal/cache"
	"github.com/takumin/seiro/internal/chapi"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/seirofile"
	"github.com/takumin/seiro/internal/spec"
	"github.com/takumin/seiro/internal/supervise"
)

// Display states of a machine.
const (
	StateRunning    = "running"
	StateStopped    = "stopped"
	StateNotCreated = "not created"
)

// Manager operates on the machines of one environment.
type Manager struct {
	Env   *env.Env
	Spec  *spec.Environment
	Cache *cache.Store
	// Self is the seiro executable used to start supervisors.
	Self string
	Out  io.Writer
	// StartTimeout bounds how long up waits for a machine to start running.
	StartTimeout time.Duration
}

// Open finds and evaluates the Seirofile (searching from the working directory
// when path is empty) and returns a manager for its environment.
func Open(path string, out io.Writer) (*Manager, error) {
	path, err := seirofile.Resolve(path)
	if err != nil {
		return nil, err
	}
	envSpec, err := seirofile.Load(path)
	if err != nil {
		return nil, err
	}
	dirs, err := env.DefaultDirs()
	if err != nil {
		return nil, err
	}
	e, err := env.New(dirs, path)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Manager{
		Env:          e,
		Spec:         envSpec,
		Cache:        &cache.Store{Root: dirs.Cache},
		Self:         self,
		Out:          out,
		StartTimeout: 30 * time.Second,
	}, nil
}

// Status is the observed state of a machine.
type Status struct {
	Name     string
	State    string
	Declared bool
	Spec     *spec.Machine
	Runtime  *supervise.Status
}

// Status returns the state of every machine that is declared or has state.
func (m *Manager) Status() ([]Status, error) {
	names, err := m.allNames()
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, name := range names {
		st, err := m.status(name)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, nil
}

func (m *Manager) status(name string) (*Status, error) {
	st := &Status{Name: name, State: StateNotCreated, Spec: m.Spec.Machine(name)}
	st.Declared = st.Spec != nil
	paths := m.Env.Machine(name)
	if _, err := os.Stat(paths.State); errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	st.State = StateStopped
	var p plan.Machine
	if err := env.ReadJSON(paths.Plan(), &p); err == nil && st.Spec == nil {
		st.Spec = p.Spec
	}
	rt, err := supervise.ReadStatus(paths.Status())
	if err != nil {
		return nil, err
	}
	st.Runtime = rt
	if rt != nil && rt.State != supervise.StateStopped && supervisorAlive(rt.SupervisorPID) {
		st.State = StateRunning
	}
	return st, nil
}

// allNames returns declared machines followed by undeclared machines that still have state.
func (m *Manager) allNames() ([]string, error) {
	var names []string
	for _, mc := range m.Spec.Machines {
		names = append(names, mc.Name)
	}
	existing, err := m.Env.MachineNames()
	if err != nil {
		return nil, err
	}
	for _, n := range existing {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names, nil
}

// selectNames validates requested names; with none requested it returns all of candidates.
func selectNames(requested, candidates []string) ([]string, error) {
	if len(requested) == 0 {
		return candidates, nil
	}
	for _, n := range requested {
		if !slices.Contains(candidates, n) {
			return nil, fmt.Errorf("unknown machine %q", n)
		}
	}
	return requested, nil
}

// supervisorAlive reports whether pid is a running seiro supervisor.
func supervisorAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", fmt.Sprint(pid), "cmdline"))
	if err != nil {
		return false
	}
	return slices.Contains(strings.Split(string(cmdline), "\x00"), "supervise")
}

func processAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func waitExit(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) || !supervisorAlive(pid) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}

// Down stops the named machines (all machines with state when none are named).
// Without force the guest is asked to power off and given timeout to do so.
func (m *Manager) Down(ctx context.Context, names []string, force bool, timeout time.Duration) error {
	all, err := m.allNames()
	if err != nil {
		return err
	}
	if names, err = selectNames(names, all); err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		if err := m.down(ctx, name, force, timeout); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) down(ctx context.Context, name string, force bool, timeout time.Duration) error {
	st, err := m.status(name)
	if err != nil {
		return err
	}
	if st.State != StateRunning {
		return nil
	}
	pid := st.Runtime.SupervisorPID
	api := chapi.New(m.Env.Machine(name).APISocket())

	if !force {
		fmt.Fprintf(m.Out, "%s: powering off\n", name) //nolint:errcheck // progress output
		if err := api.PowerButton(ctx); err == nil && waitExit(ctx, pid, timeout) {
			fmt.Fprintf(m.Out, "%s: stopped\n", name) //nolint:errcheck // progress output
			return nil
		}
		fmt.Fprintf(m.Out, "%s: guest did not power off within %s, stopping the VMM\n", name, timeout) //nolint:errcheck // progress output
	}

	if err := api.Shutdown(ctx); err == nil && waitExit(ctx, pid, 10*time.Second) {
		fmt.Fprintf(m.Out, "%s: stopped\n", name) //nolint:errcheck // progress output
		return nil
	}
	// Last resort: the supervisor forwards SIGTERM to cloud-hypervisor and cleans up.
	syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck,gosec // may have exited
	if waitExit(ctx, pid, 10*time.Second) {
		fmt.Fprintf(m.Out, "%s: stopped\n", name) //nolint:errcheck // progress output
		return nil
	}
	if st.Runtime.VMMPID > 0 {
		syscall.Kill(st.Runtime.VMMPID, syscall.SIGKILL) //nolint:errcheck,gosec // may have exited
	}
	syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck,gosec // may have exited
	return errors.New("killed after failing to stop gracefully")
}

// Destroy stops the named machines and removes their state. When no machine
// state remains, the environment itself is removed.
func (m *Manager) Destroy(ctx context.Context, names []string, timeout time.Duration) error {
	existing, err := m.Env.MachineNames()
	if err != nil {
		return err
	}
	all, err := m.allNames()
	if err != nil {
		return err
	}
	if names, err = selectNames(names, all); err != nil {
		return err
	}
	if err := m.Down(ctx, intersect(names, existing), false, timeout); err != nil {
		return err
	}
	for _, name := range names {
		if !slices.Contains(existing, name) {
			continue
		}
		paths := m.Env.Machine(name)
		if err := errors.Join(os.RemoveAll(paths.State), os.RemoveAll(paths.Runtime)); err != nil {
			return err
		}
		fmt.Fprintf(m.Out, "%s: destroyed\n", name) //nolint:errcheck // progress output
	}
	if remaining, err := m.Env.MachineNames(); err == nil && len(remaining) == 0 {
		return m.Env.Remove()
	}
	return nil
}

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// lookPath resolves a required executable with a helpful error.
func lookPath(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH (install it with `aqua i`)", name)
	}
	return p, nil
}
