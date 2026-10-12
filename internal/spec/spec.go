// Package spec defines the declarative model produced by evaluating a Seirofile.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
)

// NetworkInternet is the built-in network that only guarantees outbound reachability.
const NetworkInternet = "internet"

// Environment is the whole set of declarations in a Seirofile.
type Environment struct {
	Machines []*Machine `json:"machines"`
}

// Image is an OS image fetched from a URL.
type Image struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
}

// Tweaks are adjustments seiro passes to the guest at boot (the image itself
// is never modified) so that stock cloud images work well. Guests that take
// care of these themselves can turn them off.
type Tweaks struct {
	// EarlyNICNames gives NICs their declared names as soon as udev sees
	// them, even in the initrd, so that nothing has to rename them later.
	EarlyNICNames bool `json:"early_nic_names"`
	// SkipNetworkWait skips waiting for the network at boot on machines
	// without a NIC.
	SkipNetworkWait bool `json:"skip_network_wait"`
}

// DefaultTweaks enables every tweak.
func DefaultTweaks() Tweaks {
	return Tweaks{EarlyNICNames: true, SkipNetworkWait: true}
}

// CacheKey returns the key under which the image is stored in the cache.
// The declared checksum is used when present, otherwise a digest of the URL.
func (i *Image) CacheKey() string {
	if i.SHA256 != "" {
		return i.SHA256
	}
	sum := sha256.Sum256([]byte(i.URL))
	return "url-" + hex.EncodeToString(sum[:])
}

// Disk is a virtual disk. A disk without an image is blank.
type Disk struct {
	Image *Image `json:"image,omitempty"`
	Size  uint64 `json:"size"`
}

// NIC is a network interface attached to a network.
type NIC struct {
	Name    string `json:"name"`
	Network string `json:"network"`
	MAC     string `json:"mac,omitempty"`
}

// Port is a port published on the host.
type Port struct {
	Host     uint16 `json:"host"`
	Guest    uint16 `json:"guest"`
	Protocol string `json:"protocol"`
}

// CloudInit holds the rendered cloud-init user-data.
type CloudInit struct {
	UserData string `json:"user_data"`
}

// Machine is a virtual machine.
type Machine struct {
	Name      string     `json:"name"`
	CPUs      int        `json:"cpus"`
	Memory    uint64     `json:"memory"`
	Disks     []Disk     `json:"disks"`
	NICs      []NIC      `json:"nics"`
	Ports     []Port     `json:"ports"`
	CloudInit *CloudInit `json:"cloud_init,omitempty"`
	Tweaks    Tweaks     `json:"tweaks"`
}

// Images returns the distinct images referenced by the machines, in declaration order.
func (e *Environment) Images() []*Image {
	seen := map[string]bool{}
	var images []*Image
	for _, m := range e.Machines {
		for _, d := range m.Disks {
			if d.Image == nil || seen[d.Image.CacheKey()] {
				continue
			}
			seen[d.Image.CacheKey()] = true
			images = append(images, d.Image)
		}
	}
	return images
}

// Machine returns the machine with the given name, or nil.
func (e *Environment) Machine(name string) *Machine {
	for _, m := range e.Machines {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// InternetNICs returns the NICs attached to the internet network.
func (m *Machine) InternetNICs() []NIC {
	var nics []NIC
	for _, n := range m.NICs {
		if n.Network == NetworkInternet {
			nics = append(nics, n)
		}
	}
	return nics
}
