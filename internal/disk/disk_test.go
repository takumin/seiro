package disk_test

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/disk"
	"github.com/takumin/seiro/internal/size"
)

func TestBlankAndInspect(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "blank.raw")
	if err := disk.CreateBlank(p, 3*size.GiB); err != nil {
		t.Fatal(err)
	}
	info, err := disk.Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Format != disk.FormatRaw || info.VirtualSize != 3*size.GiB {
		t.Errorf("Inspect = %+v", info)
	}
	if err := disk.CreateBlank(p, size.GiB); err == nil {
		t.Error("CreateBlank overwrote an existing file")
	}
}

func TestOverlay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := filepath.Join(dir, "base.raw")
	if err := disk.CreateBlank(base, 64*size.MiB); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "overlay.qcow2")
	if err := disk.CreateOverlay(overlay, base, 2*size.GiB); err != nil {
		t.Fatal(err)
	}

	info, err := disk.Inspect(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if info.Format != disk.FormatQCOW2 || info.VirtualSize != 2*size.GiB {
		t.Errorf("Inspect = %+v", info)
	}
	fi, _ := os.Stat(overlay)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("overlay mode = %v", fi.Mode())
	}

	// The backing file name is stored at the offset given by the header.
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	off := binary.BigEndian.Uint64(data[8:16])
	n := binary.BigEndian.Uint32(data[16:20])
	if got := string(data[off : off+uint64(n)]); got != base {
		t.Errorf("backing file = %q, want %q", got, base)
	}

	if qemuImg, err := exec.LookPath("qemu-img"); err == nil {
		out, err := exec.Command(qemuImg, "check", overlay).CombinedOutput() // #nosec G204 -- test helper
		if err != nil || !strings.Contains(string(out), "No errors") {
			t.Errorf("qemu-img check failed: %v\n%s", err, out)
		}
	}
}

func TestInspectSmallFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "tiny")
	if err := os.WriteFile(p, []byte("QFI"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := disk.Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Format != disk.FormatRaw || info.VirtualSize != 3 {
		t.Errorf("Inspect = %+v", info)
	}
}

func TestInspectSquashfs(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "rootfs.squashfs")
	if err := os.WriteFile(p, append([]byte("hsqs"), make([]byte, 60)...), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := disk.Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	if *info != (disk.Info{Format: disk.FormatRaw, VirtualSize: 64, Filesystem: disk.FilesystemSquashfs}) {
		t.Errorf("Inspect = %+v", info)
	}
}
