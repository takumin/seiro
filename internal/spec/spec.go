// Package spec defines the declarative model produced by evaluating a Seirofile.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
)

// NetworkInternet is the built-in network that only guarantees outbound reachability.
const NetworkInternet = "internet"

// Virtual machine monitors that run machines.
const (
	VMMCloudHypervisor = "cloud-hypervisor"
	VMMQEMU            = "qemu"
)

// Environment is the whole set of declarations in a Seirofile.
type Environment struct {
	Machines []*Machine `json:"machines"`
}

// Image is an OS image fetched from a URL, or a local file used in place.
// Exactly one of URL and Path is set.
type Image struct {
	URL    string `json:"url,omitempty"`
	Path   string `json:"path,omitempty"`
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

// Source returns the URL or the path of the image, for messages.
func (i *Image) Source() string {
	if i.Path != "" {
		return i.Path
	}
	return i.URL
}

// Key identifies the image among the images of an environment: the path of a
// local image, otherwise its cache key.
func (i *Image) Key() string {
	if i.Path != "" {
		return "path:" + i.Path
	}
	return i.CacheKey()
}

// CacheKey returns the key under which a downloaded image is stored in the cache.
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

// Live boots the kernel and initrd directly, with the root on a read-only
// image whose changes live in memory and are lost when the machine stops.
type Live struct {
	Kernel *Image `json:"kernel"`
	Initrd *Image `json:"initrd,omitempty"`
	Rootfs *Image `json:"rootfs"`
	// Cmdline is appended to the kernel command line seiro derives.
	Cmdline string `json:"cmdline,omitempty"`
}

// Images returns the images the live boot uses.
func (l *Live) Images() []*Image {
	images := []*Image{l.Kernel}
	if l.Initrd != nil {
		images = append(images, l.Initrd)
	}
	return append(images, l.Rootfs)
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
	Name string `json:"name"`
	// VMM is the virtual machine monitor that runs the machine. Plans
	// written before it was declared run on cloud-hypervisor.
	VMM       string     `json:"vmm,omitempty"`
	CPUs      int        `json:"cpus"`
	Memory    uint64     `json:"memory"`
	Live      *Live      `json:"live,omitempty"`
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
	add := func(img *Image) {
		if img == nil || seen[img.Key()] {
			return
		}
		seen[img.Key()] = true
		images = append(images, img)
	}
	for _, m := range e.Machines {
		if m.Live != nil {
			for _, img := range m.Live.Images() {
				add(img)
			}
		}
		for _, d := range m.Disks {
			add(d.Image)
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

// VMMName returns the virtual machine monitor that runs the machine.
func (m *Machine) VMMName() string {
	if m.VMM == "" {
		return VMMCloudHypervisor
	}
	return m.VMM
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
