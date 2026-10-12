package env_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/env"
)

func TestID(t *testing.T) {
	t.Parallel()

	a, err := env.ID("/home/user/projects/lab/Seirofile")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^lab-[0-9a-f]{8}$`).MatchString(a) {
		t.Errorf("ID = %q", a)
	}
	b, _ := env.ID("/home/user/other/lab/Seirofile")
	if a == b {
		t.Errorf("different paths produced the same ID %q", a)
	}
	again, _ := env.ID("/home/user/projects/lab/Seirofile")
	if a != again {
		t.Errorf("ID is not deterministic: %q != %q", a, again)
	}
	weird, _ := env.ID("/tmp/my lab!/Seirofile")
	if !strings.HasPrefix(weird, "my_lab-") {
		t.Errorf("unsafe characters not replaced: %q", weird)
	}
	root, _ := env.ID("/Seirofile")
	if !strings.HasPrefix(root, "env-") {
		t.Errorf("root directory ID = %q", root)
	}
}

func TestDefaultDirs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	t.Setenv("XDG_CACHE_HOME", "relative/is/ignored")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1234")

	dirs, err := env.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if dirs.State != "/xdg/state/seiro" {
		t.Errorf("State = %q", dirs.State)
	}
	if dirs.Cache != filepath.Join(home, ".cache", "seiro") {
		t.Errorf("Cache = %q", dirs.Cache)
	}
	if dirs.Runtime != "/run/user/1234/seiro" {
		t.Errorf("Runtime = %q", dirs.Runtime)
	}
}

func TestLayoutAndList(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dirs := env.Dirs{State: filepath.Join(base, "state"), Runtime: filepath.Join(base, "run"), Cache: filepath.Join(base, "cache")}
	e, err := env.New(dirs, filepath.Join(base, "proj", "Seirofile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Init(); err != nil {
		t.Fatal(err)
	}
	if err := e.Init(); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}

	m := e.Machine("dev")
	if m.APISocket() != filepath.Join(dirs.Runtime, e.ID, "dev", "api.sock") {
		t.Errorf("APISocket = %q", m.APISocket())
	}
	if m.Disk(1, "raw") != filepath.Join(dirs.State, "envs", e.ID, "machines", "dev", "disk1.raw") {
		t.Errorf("Disk = %q", m.Disk(1, "raw"))
	}
	if err := os.MkdirAll(m.State, 0o750); err != nil {
		t.Fatal(err)
	}
	names, err := e.MachineNames()
	if err != nil || len(names) != 1 || names[0] != "dev" {
		t.Errorf("MachineNames = %v, %v", names, err)
	}

	envs, err := env.List(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].ID != e.ID || envs[0].Seirofile != e.Seirofile {
		t.Errorf("List = %+v", envs)
	}

	if err := e.Remove(); err != nil {
		t.Fatal(err)
	}
	if envs, _ := env.List(dirs); len(envs) != 0 {
		t.Errorf("List after Remove = %+v", envs)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "x.json")
	type doc struct{ A int }
	if err := env.WriteJSON(p, doc{A: 7}); err != nil {
		t.Fatal(err)
	}
	var got doc
	if err := env.ReadJSON(p, &got); err != nil || got.A != 7 {
		t.Errorf("ReadJSON = %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}
