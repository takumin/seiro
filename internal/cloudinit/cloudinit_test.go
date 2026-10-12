package cloudinit_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"

	"github.com/takumin/seiro/internal/cloudinit"
)

func readBack(t *testing.T, path string) (label string, files map[string]string) {
	t.Helper()
	b, err := file.OpenFromPath(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close() //nolint:errcheck // test
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := iso9660.Read(b, fi.Size(), 0, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// go-diskfs fails ReadDir on images it writes, so look the files up by name.
	files = map[string]string{}
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		f, err := fs.OpenFile("/"+name, os.O_RDONLY)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	return strings.TrimRight(fs.Label(), "\x00 "), files
}

func TestBuild(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "seed.iso")
	seed := &cloudinit.Seed{
		UserData:      "#cloud-config\npassword: x\n",
		MetaData:      "instance-id: a\nlocal-hostname: a\n",
		NetworkConfig: "version: 2\n",
	}
	if err := cloudinit.Build(p, seed); err != nil {
		t.Fatal(err)
	}
	// Building again replaces the image.
	seed.UserData = "#cloud-config\npassword: y\n" + strings.Repeat("#", 2<<20) + "\n"
	if err := cloudinit.Build(p, seed); err != nil {
		t.Fatal(err)
	}

	label, files := readBack(t, p)
	if label != cloudinit.VolumeLabel {
		t.Errorf("label = %q", label)
	}
	if files["user-data"] != seed.UserData || files["meta-data"] != seed.MetaData || files["network-config"] != seed.NetworkConfig {
		t.Errorf("unexpected files: %v", files)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestBuildWithoutNetworkConfig(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "seed.iso")
	if err := cloudinit.Build(p, &cloudinit.Seed{UserData: "#cloud-config\n", MetaData: "instance-id: a\n"}); err != nil {
		t.Fatal(err)
	}
	_, files := readBack(t, p)
	if _, ok := files["network-config"]; ok || len(files) != 2 {
		t.Errorf("unexpected files: %v", files)
	}
}
