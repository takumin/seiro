package cache_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/takumin/seiro/internal/cache"
)

func server(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Write([]byte(body)) //nolint:errcheck,gosec // test server
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestFetchCachesAndVerifies(t *testing.T) {
	t.Parallel()

	srv, hits := server(t, "image-bytes")
	s := &cache.Store{Root: t.TempDir(), Client: srv.Client()}
	ctx := context.Background()

	p, err := s.Fetch(ctx, "images", "k", srv.URL+"/a.img", sum("image-bytes"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if p != s.Path("images", "k") {
		t.Errorf("path = %q, want %q", p, s.Path("images", "k"))
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "image-bytes" {
		t.Fatalf("cached data = %q, %v", data, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("cached file is writable: %v", fi.Mode())
	}

	if _, err := s.Fetch(ctx, "images", "k", srv.URL+"/a.img", sum("image-bytes")); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server was hit %d times, want 1", hits.Load())
	}
}

func TestFetchWithoutChecksum(t *testing.T) {
	t.Parallel()

	srv, hits := server(t, "x")
	s := &cache.Store{Root: t.TempDir(), Client: srv.Client()}
	for range 2 {
		if _, err := s.Fetch(context.Background(), "images", "k", srv.URL, ""); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("server was hit %d times, want 1", hits.Load())
	}
}

func TestFetchMismatchLeavesNothing(t *testing.T) {
	t.Parallel()

	srv, _ := server(t, "tampered")
	root := t.TempDir()
	s := &cache.Store{Root: root, Client: srv.Client()}

	_, err := s.Fetch(context.Background(), "images", "k", srv.URL, sum("original"))
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "images", "k"))
	for _, e := range entries {
		if e.Name() != ".lock" {
			t.Errorf("unexpected file left in cache: %s", e.Name())
		}
	}
}

func TestFetchHTTPError(t *testing.T) {
	t.Parallel()

	srv, _ := server(t, "")
	s := &cache.Store{Root: t.TempDir(), Client: srv.Client()}
	if _, err := s.Fetch(context.Background(), "images", "k", srv.URL+"/missing", ""); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestFetchConcurrent(t *testing.T) {
	t.Parallel()

	srv, hits := server(t, "shared")
	s := &cache.Store{Root: t.TempDir(), Client: srv.Client()}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.Fetch(context.Background(), "images", "k", srv.URL, sum("shared")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Errorf("server was hit %d times, want 1", hits.Load())
	}
}

func TestFirmwareURL(t *testing.T) {
	t.Parallel()

	if !strings.Contains(cache.FirmwareURL, cache.FirmwareVersion) || len(cache.FirmwareSHA256) != 64 {
		t.Errorf("unexpected firmware pin: %s %s", cache.FirmwareURL, cache.FirmwareSHA256)
	}
}
