// Package cache downloads, verifies and stores files such as OS images and firmware.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/takumin/seiro/internal/env"
)

// DataFile is the name of the cached file inside an entry directory.
const DataFile = "data"

// Store is a content cache rooted at a directory.
type Store struct {
	Root   string
	Client *http.Client
}

// Entry describes a cached file.
type Entry struct {
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Path returns the path of the cached data for kind/key, whether or not it exists.
func (s *Store) Path(kind, key string) string {
	return filepath.Join(s.Root, kind, key, DataFile)
}

// Fetch returns the path of the cached file for kind/key, downloading url first
// if it is not cached yet. When sum is non-empty the download must match it.
// Concurrent fetches of the same entry are serialized with a file lock.
func (s *Store) Fetch(ctx context.Context, kind, key, url, sum string) (string, error) {
	dir := filepath.Join(s.Root, kind, key)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	unlock, err := lock(filepath.Join(dir, ".lock"))
	if err != nil {
		return "", err
	}
	defer unlock()

	data := filepath.Join(dir, DataFile)
	meta := filepath.Join(dir, "entry.json")
	var cached Entry
	if err := env.ReadJSON(meta, &cached); err == nil {
		if _, err := os.Stat(data); err == nil && (sum == "" || cached.SHA256 == sum) {
			return data, nil
		}
	}

	slog.InfoContext(ctx, "downloading", slog.String("url", url))
	entry, err := s.download(ctx, url, sum, dir)
	if err != nil {
		return "", err
	}
	if err := env.WriteJSON(meta, entry); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "downloaded", slog.String("url", url), slog.Int64("bytes", entry.Size), slog.String("sha256", entry.SHA256))
	return data, nil
}

func (s *Store) download(ctx context.Context, url, sum, dir string) (*Entry, error) {
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req) // #nosec G704 -- downloading user-declared URLs is the purpose of the cache
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // no-op after a successful rename

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if sum != "" && got != sum {
		return nil, fmt.Errorf("download %s: sha256 mismatch: got %s, want %s", url, got, sum)
	}
	if err := os.Chmod(tmp.Name(), 0o400); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, DataFile)); err != nil {
		return nil, err
	}
	return &Entry{URL: url, SHA256: got, Size: n, FetchedAt: time.Now().UTC()}, nil
}

func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- lock file inside the cache
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- file descriptors fit in int
		return nil, errors.Join(err, f.Close())
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck,gosec // released on close anyway
		f.Close()                                   //nolint:errcheck,gosec // lock file
	}, nil
}
