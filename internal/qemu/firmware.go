// Package qemu holds what seiro needs from the host's QEMU besides its
// command line: the UEFI firmware it ships and its QMP control socket.
package qemu

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Firmware is a UEFI firmware mapped as flash: the read-only code and the
// template of the variable store each machine gets a copy of.
type Firmware struct {
	Code         string `json:"code"`
	VarsTemplate string `json:"vars_template"`
}

// descriptor is the subset of a QEMU firmware descriptor (see
// docs/interop/firmware.json in QEMU) used to choose a firmware.
type descriptor struct {
	InterfaceTypes []string `json:"interface-types"`
	Mapping        struct {
		Device     string `json:"device"`
		Executable struct {
			Filename string `json:"filename"`
			Format   string `json:"format"`
		} `json:"executable"`
		NVRAMTemplate struct {
			Filename string `json:"filename"`
			Format   string `json:"format"`
		} `json:"nvram-template"`
	} `json:"mapping"`
	Targets []struct {
		Architecture string   `json:"architecture"`
		Machines     []string `json:"machines"`
	} `json:"targets"`
	Features []string `json:"features"`
}

// unwanted are firmware features that need more than seiro sets up: keys
// enrolled for Secure Boot, SMM, or a confidential computing host.
var unwanted = []string{"secure-boot", "enrolled-keys", "requires-smm", "amd-sev-es", "amd-sev-snp", "intel-tdx"}

// FirmwareDirs returns the directories QEMU firmware descriptors are searched
// in, from the lowest to the highest priority.
func FirmwareDirs() []string {
	dirs := []string{"/usr/share/qemu/firmware", "/etc/qemu/firmware"}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		if home, err := os.UserHomeDir(); err == nil {
			config = filepath.Join(home, ".config")
		}
	}
	if config != "" {
		dirs = append(dirs, filepath.Join(config, "qemu", "firmware"))
	}
	return dirs
}

// FindFirmware returns the UEFI firmware described in dirs for arch (such as
// x86_64) and the versioned machine types of family (such as pc-q35). As in
// QEMU, descriptors are considered in the order of their file names, and a
// descriptor in a later directory replaces one of the same name in an earlier
// directory.
func FindFirmware(dirs []string, arch, family string) (*Firmware, error) {
	files := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				files[e.Name()] = filepath.Join(dir, e.Name())
			}
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		data, err := os.ReadFile(files[n])
		if err != nil {
			continue
		}
		var d descriptor
		if json.Unmarshal(data, &d) != nil || !d.suits(arch, family) {
			continue
		}
		fw := &Firmware{Code: d.Mapping.Executable.Filename, VarsTemplate: d.Mapping.NVRAMTemplate.Filename}
		if _, err := os.Stat(fw.Code); err != nil {
			continue
		}
		if _, err := os.Stat(fw.VarsTemplate); err != nil {
			continue
		}
		return fw, nil
	}
	return nil, errors.New("no UEFI firmware for " + arch + " found in " + strings.Join(dirs, ", ") + " (install OVMF, e.g. the ovmf package)")
}

func (d *descriptor) suits(arch, family string) bool {
	m := d.Mapping
	if !slices.Contains(d.InterfaceTypes, "uefi") || m.Device != "flash" ||
		m.Executable.Filename == "" || m.NVRAMTemplate.Filename == "" ||
		(m.Executable.Format != "" && m.Executable.Format != "raw") ||
		(m.NVRAMTemplate.Format != "" && m.NVRAMTemplate.Format != "raw") {
		return false
	}
	for _, f := range d.Features {
		if slices.Contains(unwanted, f) {
			return false
		}
	}
	for _, t := range d.Targets {
		if t.Architecture != arch {
			continue
		}
		for _, pattern := range t.Machines {
			if strings.HasPrefix(pattern, family+"-") {
				return true
			}
		}
	}
	return false
}
