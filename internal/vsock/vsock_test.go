package vsock_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/takumin/seiro/internal/vsock"
)

// fakeVMM emulates cloud-hypervisor's hybrid vsock socket: it answers CONNECT
// for open ports and echoes back in upper case, and closes the connection otherwise.
func fakeVMM(t *testing.T, open uint32) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "vsock.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close() //nolint:errcheck // test
				r := bufio.NewReader(c)
				line, err := r.ReadString('\n')
				if err != nil || line != "CONNECT "+strconv.FormatUint(uint64(open), 10)+"\n" {
					return
				}
				// The greeting follows the reply in the same write, as sshd's banner may.
				if _, err := io.WriteString(c, "OK 1073741824\nhello\n"); err != nil {
					return
				}
				b, _ := io.ReadAll(r)
				c.Write(bytes.ToUpper(b)) //nolint:errcheck,gosec // test
			}()
		}
	}()
	return sock
}

func TestDialAndRelay(t *testing.T) {
	t.Parallel()

	sock := fakeVMM(t, 22)
	conn, err := vsock.Dial(context.Background(), sock, 22)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test
	var out bytes.Buffer
	if err := vsock.Relay(conn, strings.NewReader("ping"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\nPING" {
		t.Errorf("relayed %q", out.String())
	}
}

func TestDialRefused(t *testing.T) {
	t.Parallel()

	sock := fakeVMM(t, 22)
	if _, err := vsock.Dial(context.Background(), sock, 23); err == nil || !strings.Contains(err.Error(), "no reply") {
		t.Errorf("expected no reply error, got %v", err)
	}
	start := time.Now()
	waited := 0
	if _, err := vsock.DialWait(context.Background(), sock, 23, 1500*time.Millisecond, func() { waited++ }); err == nil {
		t.Error("expected DialWait to give up")
	}
	if waited != 1 {
		t.Errorf("waiting called %d times", waited)
	}
	if time.Since(start) < time.Second {
		t.Error("DialWait did not retry")
	}
}

func TestDialNotRunning(t *testing.T) {
	t.Parallel()

	sock := filepath.Join(t.TempDir(), "missing.sock")
	if _, err := vsock.DialWait(context.Background(), sock, 22, time.Minute, nil); !errors.Is(err, vsock.ErrNotRunning) {
		t.Errorf("expected ErrNotRunning, got %v", err)
	}
}
