package chapi_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/chapi"
)

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) //nolint:errcheck,gosec // test
	sock := filepath.Join(dir, "api.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}   // #nosec G112 -- test server
	go srv.Serve(l)                   //nolint:errcheck // closed in cleanup
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck,gosec // test
	return sock
}

func TestClient(t *testing.T) {
	t.Parallel()

	var calls []string
	sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/vm.info":
			w.Write([]byte(`{"state":"Running","config":{}}`)) //nolint:errcheck,gosec // test
		case "/api/v1/vm.power-button", "/api/v1/vmm.shutdown":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/vmm.ping":
			w.Write([]byte(`{"version":"v53.0"}`)) //nolint:errcheck,gosec // test
		default:
			http.Error(w, "VM is not booted", http.StatusInternalServerError)
		}
	}))

	c := chapi.New(sock)
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx)
	if err != nil || info.State != "Running" {
		t.Fatalf("Info = %+v, %v", info, err)
	}
	if err := c.PowerButton(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	want := "GET /api/v1/vmm.ping,GET /api/v1/vm.info,PUT /api/v1/vm.power-button,PUT /api/v1/vmm.shutdown"
	if got := strings.Join(calls, ","); got != want {
		t.Errorf("calls = %s", got)
	}
}

func TestClientErrors(t *testing.T) {
	t.Parallel()

	sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "VM is not booted", http.StatusInternalServerError)
	}))
	if err := chapi.New(sock).PowerButton(context.Background()); err == nil || !strings.Contains(err.Error(), "VM is not booted") {
		t.Errorf("expected API error, got %v", err)
	}
	if err := chapi.New(filepath.Join(t.TempDir(), "missing.sock")).Ping(context.Background()); err == nil {
		t.Error("expected dial error")
	}
}
