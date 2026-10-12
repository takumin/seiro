package machine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
	"github.com/takumin/seiro/internal/supervise"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	base := t.TempDir()
	e, err := env.New(env.Dirs{State: filepath.Join(base, "state"), Runtime: filepath.Join(base, "run"), Cache: filepath.Join(base, "cache")}, filepath.Join(base, "proj", "Seirofile"))
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{
		Env: e,
		Spec: &spec.Environment{Machines: []*spec.Machine{
			{Name: "a", CPUs: 1, Memory: 512 * size.MiB, Disks: []spec.Disk{{Size: size.GiB}}},
			{Name: "b", CPUs: 1, Memory: 512 * size.MiB, Disks: []spec.Disk{{Size: size.GiB}}},
		}},
		Out: &bytes.Buffer{},
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	m := testManager(t)
	if err := m.Env.Init(); err != nil {
		t.Fatal(err)
	}
	// "b" was created and has stopped; "gone" is no longer declared but has state.
	for _, name := range []string{"b", "gone"} {
		if err := os.MkdirAll(m.Env.Machine(name).State, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	code := 0
	if err := env.WriteJSON(m.Env.Machine("b").Status(), supervise.Status{State: supervise.StateStopped, ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	// A status claiming to run under a pid that is not a supervisor is stopped.
	if err := env.WriteJSON(m.Env.Machine("gone").Status(), supervise.Status{State: supervise.StateRunning, SupervisorPID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}

	sts, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Status{}
	for _, s := range sts {
		got[s.Name] = s
	}
	if len(sts) != 3 || sts[2].Name != "gone" {
		t.Fatalf("status order = %+v", sts)
	}
	if got["a"].State != StateNotCreated || !got["a"].Declared {
		t.Errorf("a = %+v", got["a"])
	}
	if got["b"].State != StateStopped || got["b"].Runtime == nil || *got["b"].Runtime.ExitCode != 0 {
		t.Errorf("b = %+v", got["b"])
	}
	if got["gone"].State != StateStopped || got["gone"].Declared {
		t.Errorf("gone = %+v", got["gone"])
	}
}

func TestDestroyRemovesState(t *testing.T) {
	t.Parallel()

	m := testManager(t)
	if err := m.Env.Init(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.MkdirAll(m.Env.Machine(name).State, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := m.Destroy(ctx, []string{"a"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Env.Machine("a").State); !os.IsNotExist(err) {
		t.Error("state of a was not removed")
	}
	if _, err := os.Stat(m.Env.StateDir()); err != nil {
		t.Error("environment was removed while b still exists")
	}
	if err := m.Destroy(ctx, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Env.StateDir()); !os.IsNotExist(err) {
		t.Error("environment was not removed after destroying every machine")
	}
	if err := m.Destroy(ctx, []string{"nope"}, time.Second); err == nil || !strings.Contains(err.Error(), `unknown machine "nope"`) {
		t.Errorf("expected unknown machine error, got %v", err)
	}
}

func TestCopyUntilEscape(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := copyUntilEscape(&out, strings.NewReader("ls\r\x1dexit\r"))
	if err != errDetached || out.String() != "ls\r" {
		t.Errorf("copyUntilEscape = %q, %v", out.String(), err)
	}
}

func TestLogsAndTail(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "serial.log")
	if err := os.WriteFile(p, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Logs(context.Background(), p, false, &out); err != nil || out.String() != "line1\nline2\n" {
		t.Errorf("Logs = %q, %v", out.String(), err)
	}
	tail, err := readTail(p, 6)
	if err != nil || string(tail) != "line2\n" {
		t.Errorf("readTail = %q, %v", tail, err)
	}

	// Follow picks up appended data and stops when the context is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	pr := &syncWriter{}
	done := make(chan error, 1)
	go func() { done <- Logs(ctx, p, true, pr) }()
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("line3\n") //nolint:errcheck,gosec // test
	f.Close()                //nolint:errcheck,gosec // test
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(pr.String(), "line3") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pr.String(), "line3") {
		t.Errorf("follow output = %q", pr.String())
	}
}

func TestDescribePorts(t *testing.T) {
	t.Parallel()

	got := describePorts([]spec.Port{{Host: 2222, Guest: 22, Protocol: "tcp"}})
	if got != " (localhost:2222->22/tcp)" {
		t.Errorf("describePorts = %q", got)
	}
	if describePorts(nil) != "" {
		t.Error("expected empty description")
	}
}
