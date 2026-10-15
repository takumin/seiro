package supervise_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/supervise"
)

// TestHelperProcess is not a real test: it is re-executed as fake passt and
// cloud-hypervisor processes by TestRun.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("SEIRO_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "passt":
		fakePasst(args[1:])
	case "vmm":
		fakeVMM(args[1:])
	case "stuck":
		fakeStuckVMM(args[1:])
	case "crash":
		os.Exit(3)
	}
	os.Exit(0)
}

func fakePasst(args []string) {
	l, err := net.Listen("unix", args[0])
	if err != nil {
		os.Exit(2)
	}
	defer l.Close() //nolint:errcheck // helper
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	<-sigs
	fmt.Println("passt: terminated")
}

// fakeVMM serves a serial socket, prints a boot banner, echoes input and exits
// with code 0 when it receives "poweroff".
func fakeVMM(args []string) {
	l, err := net.Listen("unix", args[0])
	if err != nil {
		os.Exit(2)
	}
	defer l.Close() //nolint:errcheck // helper
	conn, err := l.Accept()
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprint(conn, "boot ok\n") //nolint:errcheck // helper
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		if sc.Text() == "poweroff" {
			fmt.Fprint(conn, "bye\n") //nolint:errcheck // helper
			conn.Close()              //nolint:errcheck,gosec // helper
			return
		}
		fmt.Fprintf(conn, "echo: %s\n", sc.Text()) //nolint:errcheck // helper
	}
}

// fakeStuckVMM serves a serial socket but never runs the guest, like QEMU
// retrying a vhost-user backend that offers too few queues.
func fakeStuckVMM(args []string) {
	l, err := net.Listen("unix", args[0])
	if err != nil {
		os.Exit(2)
	}
	defer l.Close()                                                      //nolint:errcheck // helper
	fmt.Fprintln(os.Stderr, "vmm: asking more queues than supported: 1") //nolint:errcheck // helper
	select {}
}

func helper(mode string, args ...string) []string {
	return append([]string{"-test.run=TestHelperProcess", "--", mode}, args...)
}

func testPlan(t *testing.T, vmmMode string) *plan.Machine {
	t.Helper()
	state := t.TempDir()
	// UNIX socket paths must be short, so do not use t.TempDir for them.
	run, err := os.MkdirTemp("", "seiro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) }) //nolint:errcheck,gosec // test
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	passtSock := filepath.Join(run, "passt-eth0.sock")
	serialSock := filepath.Join(run, "serial.sock")
	return &plan.Machine{
		Name:          "dev",
		RuntimeDir:    run,
		APISocket:     filepath.Join(run, "api.sock"),
		SerialSocket:  serialSock,
		ConsoleSocket: filepath.Join(run, "console.sock"),
		SerialLog:     filepath.Join(state, "serial.log"),
		Status:        filepath.Join(state, "state.json"),
		NICs: []plan.NIC{{
			Name:    "eth0",
			Socket:  passtSock,
			Backend: plan.Process{Path: self, Args: helper("passt", passtSock), Log: filepath.Join(state, "passt-eth0.log")},
		}},
		VMM: plan.Process{Path: self, Args: helper(vmmMode, serialSock), Log: filepath.Join(state, "vmm.log")},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRun(t *testing.T) {
	t.Setenv("SEIRO_HELPER", "1")
	p := testPlan(t, "vmm")

	// The guest runs from the third question on.
	var asked atomic.Int32
	ready := func(context.Context) (bool, error) { return asked.Add(1) >= 3, nil }

	done := make(chan error, 1)
	go func() { done <- supervise.Run(context.Background(), p, ready) }()

	waitFor(t, "running state", func() bool {
		st, _ := supervise.ReadStatus(p.Status)
		return st != nil && st.State == supervise.StateRunning
	})

	// Two console clients both see the output; input from either reaches the VM.
	c1, err := net.Dial("unix", p.ConsoleSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close() //nolint:errcheck // test
	c2, err := net.Dial("unix", p.ConsoleSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()                   //nolint:errcheck // test
	time.Sleep(100 * time.Millisecond) // let the relay register both clients

	if _, err := c1.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	r2 := bufio.NewReader(c2)
	line, err := r2.ReadString('\n')
	if err != nil || line != "echo: hello\n" {
		t.Fatalf("client 2 read %q, %v", line, err)
	}
	if _, err := c2.Write([]byte("poweroff\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after the VM powered off")
	}

	st, err := supervise.ReadStatus(p.Status)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != supervise.StateStopped || st.ExitCode == nil || *st.ExitCode != 0 || st.Error != "" {
		t.Errorf("status = %+v", st)
	}
	log, _ := os.ReadFile(p.SerialLog)
	for _, want := range []string{"boot ok\n", "echo: hello\n", "bye\n"} {
		if !bytes.Contains(log, []byte(want)) {
			t.Errorf("serial log %q does not contain %q", log, want)
		}
	}
	passtLog, _ := os.ReadFile(p.NICs[0].Backend.Log)
	if !strings.Contains(string(passtLog), "passt: terminated") {
		t.Errorf("passt was not terminated: %q", passtLog)
	}
	if _, err := os.Stat(p.RuntimeDir); err == nil {
		t.Errorf("runtime directory %s was not removed", p.RuntimeDir)
	}
	if _, err := io.ReadAll(c1); err != nil {
		t.Errorf("client 1 was not disconnected cleanly: %v", err)
	}
}

func TestRunVMMFailsToStart(t *testing.T) {
	t.Setenv("SEIRO_HELPER", "1")
	p := testPlan(t, "crash")

	err := supervise.Run(context.Background(), p, nil)
	if err == nil || !strings.Contains(err.Error(), "exited before creating its socket") {
		t.Fatalf("expected start failure, got %v", err)
	}
	st, _ := supervise.ReadStatus(p.Status)
	if st == nil || st.State != supervise.StateStopped || st.Error == "" {
		t.Errorf("status = %+v", st)
	}
	passtLog, _ := os.ReadFile(p.NICs[0].Backend.Log)
	if !strings.Contains(string(passtLog), "passt: terminated") {
		t.Errorf("passt was not cleaned up: %q", passtLog)
	}
}

func TestRunGuestNeverRuns(t *testing.T) {
	t.Setenv("SEIRO_HELPER", "1")
	old := supervise.ReadyTimeout
	supervise.ReadyTimeout = 500 * time.Millisecond
	t.Cleanup(func() { supervise.ReadyTimeout = old })
	p := testPlan(t, "stuck")

	never := func(context.Context) (bool, error) { return false, nil }
	err := supervise.Run(context.Background(), p, never)
	if err == nil || !strings.Contains(err.Error(), "did not start running") ||
		!strings.Contains(err.Error(), "asking more queues than supported: 1") {
		t.Fatalf("expected the guest to never run, got %v", err)
	}
	st, _ := supervise.ReadStatus(p.Status)
	if st == nil || st.State != supervise.StateStopped || st.Error != err.Error() {
		t.Errorf("status = %+v", st)
	}
	if st != nil && st.VMMPID != 0 {
		if err := syscall.Kill(st.VMMPID, 0); err == nil {
			t.Errorf("the VMM (pid %d) is still alive", st.VMMPID)
		}
	}
	passtLog, _ := os.ReadFile(p.NICs[0].Backend.Log)
	if !strings.Contains(string(passtLog), "passt: terminated") {
		t.Errorf("passt was not cleaned up: %q", passtLog)
	}
}

func TestReadStatusMissing(t *testing.T) {
	t.Parallel()

	st, err := supervise.ReadStatus(filepath.Join(t.TempDir(), "state.json"))
	if st != nil || err != nil {
		t.Errorf("ReadStatus = %+v, %v", st, err)
	}
}
