package qemu_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/qemu"
)

func writeDescriptor(t *testing.T, dir, name, code, vars, arch string, features ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(features))
	for i, f := range features {
		quoted[i] = `"` + f + `"`
	}
	data := `{
  "interface-types": ["uefi"],
  "mapping": {
    "device": "flash",
    "executable": {"filename": "` + code + `", "format": "raw"},
    "nvram-template": {"filename": "` + vars + `", "format": "raw"}
  },
  "targets": [{"architecture": "` + arch + `", "machines": ["pc-i440fx-*", "pc-q35-*"]}],
  "features": [` + strings.Join(quoted, ",") + `]
}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindFirmware(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fw := filepath.Join(root, "fw")
	if err := os.MkdirAll(fw, 0o750); err != nil {
		t.Fatal(err)
	}
	code, vars := filepath.Join(fw, "CODE.fd"), filepath.Join(fw, "VARS.fd")
	secCode, secVars := filepath.Join(fw, "CODE.secboot.fd"), filepath.Join(fw, "VARS.ms.fd")
	touch(t, code, vars, secCode, secVars)

	system, user := filepath.Join(root, "system"), filepath.Join(root, "user")
	// Secure Boot comes first by name but needs keys and SMM seiro does not set up.
	writeDescriptor(t, system, "40-secure.json", secCode, secVars, "x86_64", "secure-boot", "enrolled-keys", "requires-smm")
	writeDescriptor(t, system, "50-aarch64.json", code, vars, "aarch64")
	writeDescriptor(t, system, "55-missing.json", filepath.Join(fw, "missing.fd"), vars, "x86_64")
	writeDescriptor(t, system, "60-plain.json", code, vars, "x86_64", "acpi-s3")
	if err := os.WriteFile(filepath.Join(system, "45-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := qemu.FindFirmware([]string{system, user, filepath.Join(root, "absent")}, "x86_64", "pc-q35")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != code || got.VarsTemplate != vars {
		t.Errorf("firmware = %+v", got)
	}

	// A descriptor of the same name in a later directory replaces it.
	writeDescriptor(t, user, "60-plain.json", secCode, secVars, "x86_64")
	got, err = qemu.FindFirmware([]string{system, user}, "x86_64", "pc-q35")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != secCode {
		t.Errorf("firmware = %+v, want the user's descriptor", got)
	}

	if _, err := qemu.FindFirmware([]string{system}, "riscv64", "virt"); err == nil || !strings.Contains(err.Error(), "no UEFI firmware for riscv64") {
		t.Errorf("err = %v", err)
	}
}

func TestFirmwareDirs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	dirs := qemu.FirmwareDirs()
	if len(dirs) != 3 || dirs[0] != "/usr/share/qemu/firmware" || dirs[2] != "/cfg/qemu/firmware" {
		t.Errorf("dirs = %q", dirs)
	}
}
