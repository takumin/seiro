package supervise_test

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/takumin/seiro/internal/supervise"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRelayLogsWithoutClients(t *testing.T) {
	t.Parallel()

	vm, host := net.Pipe()
	log := &syncBuffer{}
	r := supervise.NewRelay(host, log)
	done := make(chan error, 1)
	go func() { done <- r.Run() }()

	if _, err := vm.Write([]byte("early output")); err != nil {
		t.Fatal(err)
	}
	vm.Close() //nolint:errcheck,gosec // test
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if log.String() != "early output" {
		t.Errorf("log = %q", log.String())
	}
}

func TestRelayClientDisconnectDoesNotStopRelay(t *testing.T) {
	t.Parallel()

	vm, host := net.Pipe()
	log := &syncBuffer{}
	r := supervise.NewRelay(host, log)
	go r.Run() //nolint:errcheck // stopped by closing vm

	sock := filepath.Join(shortTempDir(t), "c.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() //nolint:errcheck // test
	go r.Serve(l)   //nolint:errcheck // stopped by closing l

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	c.Close() //nolint:errcheck,gosec // test
	time.Sleep(50 * time.Millisecond)

	go io.Copy(io.Discard, vm) //nolint:errcheck // drain input
	if _, err := vm.Write([]byte("still logging")); err != nil {
		t.Fatal(err)
	}
	vm.Close() //nolint:errcheck,gosec // test
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(log.String(), "still logging") {
		t.Errorf("log = %q", log.String())
	}
}

func TestRotatingFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "serial.log")
	r, err := supervise.OpenRotating(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"12345", "67890", "abc"} {
		if _, err := r.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	cur, _ := os.ReadFile(p)
	old, _ := os.ReadFile(p + ".1")
	if string(cur) != "abc" || string(old) != "1234567890" {
		t.Errorf("current = %q, rotated = %q", cur, old)
	}

	// Reopening appends to the existing file.
	r, err = supervise.OpenRotating(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("d")) //nolint:errcheck,gosec // test
	r.Close()            //nolint:errcheck,gosec // test
	if cur, _ := os.ReadFile(p); string(cur) != "abcd" {
		t.Errorf("after reopen = %q", cur)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "seiro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) }) //nolint:errcheck,gosec // test
	return d
}
