package hostkey_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/hostkey"
)

func TestEnsure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "key")
	k, err := hostkey.Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Private, "-----BEGIN OPENSSH PRIVATE KEY-----") || !strings.HasPrefix(k.Public, "ssh-ed25519 ") || strings.HasSuffix(k.Public, "\n") {
		t.Errorf("key = %+v", k)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}

	again, err := hostkey.Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if *again != *k {
		t.Error("Ensure regenerated an existing key")
	}
	if got := k.KnownHosts("dev"); got != "dev "+k.Public+"\n" {
		t.Errorf("KnownHosts = %q", got)
	}
}

func TestEnsureInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostkey.Ensure(path); err == nil {
		t.Error("expected an error for an invalid key")
	}
}
