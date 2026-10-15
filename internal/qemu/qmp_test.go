package qemu_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/takumin/seiro/internal/qemu"
)

// fakeQMP serves QMP on a UNIX socket: it greets each connection, records
// the commands and answers them, sending an event before each reply.
func fakeQMP(t *testing.T, reply func(cmd string) string) (string, func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "qmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) //nolint:errcheck,gosec // test
	sock := filepath.Join(dir, "qmp.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test

	var (
		mu    sync.Mutex
		calls []string
	)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			fmt.Fprintln(conn, `{"QMP": {"version": {}, "capabilities": []}}`) //nolint:errcheck // test
			r := bufio.NewReader(conn)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					break
				}
				var req struct {
					Execute string `json:"execute"`
				}
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					break
				}
				mu.Lock()
				calls = append(calls, req.Execute)
				mu.Unlock()
				resp := reply(req.Execute)
				if resp == "" {
					break
				}
				fmt.Fprintln(conn, `{"event": "TEST", "timestamp": {}}`) //nolint:errcheck // test
				fmt.Fprintln(conn, resp)                                 //nolint:errcheck // test
			}
			conn.Close() //nolint:errcheck,gosec // test
		}
	}()
	return sock, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func TestQMP(t *testing.T) {
	t.Parallel()

	sock, calls := fakeQMP(t, func(cmd string) string {
		switch cmd {
		case "qmp_capabilities", "system_powerdown":
			return `{"return": {}}`
		case "query-status":
			return `{"return": {"running": true, "status": "running"}}`
		case "quit":
			return "" // QEMU may exit before replying.
		default:
			return `{"error": {"class": "CommandNotFound", "desc": "The command ` + cmd + ` has not been found"}}`
		}
	})
	q := qemu.NewQMP(sock)
	ctx := context.Background()

	st, err := q.Status(ctx)
	if err != nil || !st.Running || st.Status != "running" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if err := q.PowerDown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.Quit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.Execute(ctx, "nope", nil); err == nil || !strings.Contains(err.Error(), "CommandNotFound") {
		t.Errorf("err = %v", err)
	}
	want := "qmp_capabilities query-status qmp_capabilities system_powerdown qmp_capabilities quit qmp_capabilities nope"
	if got := strings.Join(calls(), " "); got != want {
		t.Errorf("calls = %s\nwant %s", got, want)
	}

	if _, err := qemu.NewQMP(filepath.Join(t.TempDir(), "missing.sock")).Status(ctx); err == nil {
		t.Error("expected an error for a missing socket")
	}
}
