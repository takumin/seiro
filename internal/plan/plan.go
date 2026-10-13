// Package plan derives how a declared machine is realized: which files to
// create and which processes to run with which arguments. It has no side effects.
package plan

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/takumin/seiro/internal/cloudinit"
	"github.com/takumin/seiro/internal/disk"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/hostkey"
	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

// Image is a cached image resolved for planning.
type Image struct {
	Path string
	Info disk.Info
}

// Resources are the host-side inputs needed to plan a machine.
type Resources struct {
	CloudHypervisor string
	Passt           string
	// Firmware is only needed by machines that boot from a disk.
	Firmware string
	// Images maps spec.Image.Key() to the resolved image.
	Images map[string]Image
	// HostKey is the SSH host key of the machine being planned. It is
	// required when the machine declares cloud_init.
	HostKey *hostkey.Key
}

// GuestCID is the vsock CID of every guest. With hybrid vsock the host side is
// a UNIX socket per VM, so CIDs never collide between machines.
const GuestCID = 3

// SSHVSockPort is the vsock port the guest's sshd listens on. It is not 22,
// which systemd-ssh-generator (systemd 256 and later) may already claim.
const SSHVSockPort = 10022

// RootfsSerial is the virtio serial of the live root image, by which the
// initrd finds it whatever disks precede it.
const RootfsSerial = "seiro-rootfs"

// Machine is the realization of a declared machine. It is written to plan.json
// by up and read by the supervisor.
type Machine struct {
	Env           string        `json:"env"`
	Name          string        `json:"name"`
	Spec          *spec.Machine `json:"spec"`
	Boot          *Boot         `json:"boot,omitempty"`
	Disks         []Disk        `json:"disks"`
	Seed          *Seed         `json:"seed,omitempty"`
	NICs          []NIC         `json:"nics"`
	VMM           Process       `json:"vmm"`
	APISocket     string        `json:"api_socket"`
	SerialSocket  string        `json:"serial_socket"`
	ConsoleSocket string        `json:"console_socket"`
	SerialLog     string        `json:"serial_log"`
	VSockSocket   string        `json:"vsock_socket"`
	SSH           *SSH          `json:"ssh,omitempty"`
	Status        string        `json:"status"`
	SuperviseLog  string        `json:"supervise_log"`
	RuntimeDir    string        `json:"runtime_dir"`
}

// Disk is a disk file attached to the VM.
type Disk struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Size    uint64 `json:"size"`
	Backing string `json:"backing,omitempty"`
}

// Boot is a direct kernel boot. Machines without it boot the firmware.
type Boot struct {
	Kernel  string `json:"kernel"`
	Initrd  string `json:"initrd,omitempty"`
	Cmdline string `json:"cmdline"`
	// Rootfs is the read-only root image of a live boot.
	Rootfs string `json:"rootfs"`
}

// Seed is the cloud-init seed attached to the VM.
type Seed struct {
	Path string         `json:"path"`
	Data cloudinit.Seed `json:"data"`
}

// NIC is a network interface and the passt process backing it.
type NIC struct {
	Name    string  `json:"name"`
	MAC     string  `json:"mac"`
	Socket  string  `json:"socket"`
	Backend Process `json:"backend"`
}

// SSH describes how to reach the guest's sshd over vsock. It is only set when
// cloud-init configures the guest.
type SSH struct {
	// User is the first user created by cloud-init; empty means the local user.
	User       string `json:"user,omitempty"`
	Port       uint32 `json:"port"`
	KnownHosts string `json:"known_hosts"`
}

// Process is a command run by the supervisor.
type Process struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
	Log  string   `json:"log"`
}

// Build plans the machine m of environment e.
func Build(e *env.Env, m *spec.Machine, res Resources) (*Machine, error) {
	paths := e.Machine(m.Name)
	p := &Machine{
		Env:           e.ID,
		Name:          m.Name,
		Spec:          m,
		APISocket:     paths.APISocket(),
		SerialSocket:  paths.SerialSocket(),
		ConsoleSocket: paths.ConsoleSocket(),
		SerialLog:     paths.SerialLog(),
		VSockSocket:   paths.VSockSocket(),
		Status:        paths.Status(),
		SuperviseLog:  paths.SuperviseLog(),
		RuntimeDir:    paths.Runtime,
	}

	if m.Live != nil {
		boot, err := liveBoot(m, res)
		if err != nil {
			return nil, err
		}
		p.Boot = boot
	}

	for i, d := range m.Disks {
		if d.Image == nil {
			p.Disks = append(p.Disks, Disk{Path: paths.Disk(i, disk.FormatRaw), Format: disk.FormatRaw, Size: d.Size})
			continue
		}
		img, ok := res.Images[d.Image.Key()]
		if !ok {
			return nil, fmt.Errorf("machine %q: disk %d: image %s is not available", m.Name, i, d.Image.Source())
		}
		if d.Size < img.Info.VirtualSize {
			return nil, fmt.Errorf("machine %q: disk %d: size %s is smaller than the image (%s)",
				m.Name, i, size.Format(d.Size), size.Format(img.Info.VirtualSize))
		}
		p.Disks = append(p.Disks, Disk{Path: paths.Disk(i, disk.FormatQCOW2), Format: disk.FormatQCOW2, Size: d.Size, Backing: img.Path})
	}

	for _, n := range m.NICs {
		mac := n.MAC
		if mac == "" {
			mac = MAC(e.ID, m.Name, n.Name)
		}
		sock := paths.PasstSocket(n.Name)
		p.NICs = append(p.NICs, NIC{
			Name:    n.Name,
			MAC:     mac,
			Socket:  sock,
			Backend: Process{Path: res.Passt, Args: PasstArgs(sock, m.Ports), Log: paths.PasstLog(n.Name)},
		})
	}

	if m.CloudInit != nil {
		netcfg, err := NetworkConfig(p.NICs)
		if err != nil {
			return nil, err
		}
		if res.HostKey == nil {
			return nil, fmt.Errorf("machine %q: no SSH host key", m.Name)
		}
		userData, err := UserData(m.CloudInit.UserData, res.HostKey)
		if err != nil {
			return nil, err
		}
		p.SSH = &SSH{User: cloudinit.DefaultUser(m.CloudInit.UserData), Port: SSHVSockPort, KnownHosts: paths.KnownHosts()}
		p.Seed = &Seed{Path: paths.Seed(), Data: cloudinit.Seed{
			UserData:      userData,
			MetaData:      MetaData(e.ID, m.Name),
			NetworkConfig: netcfg,
		}}
	}

	p.VMM = Process{Path: res.CloudHypervisor, Args: VMMArgs(p, m, res.Firmware), Log: paths.VMMLog()}
	return p, nil
}

// liveBoot plans the direct kernel boot of a live machine.
func liveBoot(m *spec.Machine, res Resources) (*Boot, error) {
	resolve := func(what string, img *spec.Image) (Image, error) {
		r, ok := res.Images[img.Key()]
		if !ok {
			return r, fmt.Errorf("machine %q: live: %s %s is not available", m.Name, what, img.Source())
		}
		return r, nil
	}
	kernel, err := resolve("kernel", m.Live.Kernel)
	if err != nil {
		return nil, err
	}
	rootfs, err := resolve("rootfs", m.Live.Rootfs)
	if err != nil {
		return nil, err
	}
	if rootfs.Info.Filesystem != disk.FilesystemSquashfs {
		return nil, fmt.Errorf("machine %q: live: rootfs %s is not a squashfs image", m.Name, m.Live.Rootfs.Source())
	}
	// The kernel cannot mount a squashfs on a disk as a live root by itself.
	if m.Live.Initrd == nil {
		return nil, fmt.Errorf("machine %q: live: a squashfs rootfs needs an initrd", m.Name)
	}
	initrd, err := resolve("initrd", m.Live.Initrd)
	if err != nil {
		return nil, err
	}
	return &Boot{Kernel: kernel.Path, Initrd: initrd.Path, Rootfs: rootfs.Path, Cmdline: LiveCmdline(m)}, nil
}

// LiveCmdline derives the kernel command line of a live machine: the serial
// console seiro attaches, the root that dracut's dmsquash-live mounts from
// the rootfs disk, and the NoCloud seed when cloud-init is declared. The
// declared cmdline comes last so that it can override them.
func LiveCmdline(m *spec.Machine) string {
	args := []string{"console=ttyS0", "root=live:/dev/disk/by-id/virtio-" + RootfsSerial}
	if m.CloudInit != nil {
		args = append(args, "ds=nocloud")
	}
	if m.Live.Cmdline != "" {
		args = append(args, m.Live.Cmdline)
	}
	return strings.Join(args, " ")
}

// MAC derives a stable, locally administered unicast MAC address.
func MAC(envID, machine, nic string) string {
	sum := sha256.Sum256([]byte(envID + "/" + machine + "/" + nic))
	hw := net.HardwareAddr{0x02, sum[0], sum[1], sum[2], sum[3], sum[4]}
	return hw.String()
}

// MetaData renders the NoCloud meta-data.
func MetaData(envID, machine string) string {
	return fmt.Sprintf("instance-id: %s-%s\nlocal-hostname: %s\n", envID, machine, machine)
}

type netplanEthernet struct {
	Match   map[string]string `yaml:"match"`
	SetName string            `yaml:"set-name"`
	DHCP4   bool              `yaml:"dhcp4"`
	DHCP6   bool              `yaml:"dhcp6"`
}

// NetworkConfig renders a cloud-init network-config (version 2) that renames
// each NIC, matched by MAC address, to its declared name and enables DHCP,
// which passt answers.
func NetworkConfig(nics []NIC) (string, error) {
	if len(nics) == 0 {
		return "", nil
	}
	eths := &yaml.Node{Kind: yaml.MappingNode}
	for _, n := range nics {
		var v yaml.Node
		if err := v.Encode(netplanEthernet{
			Match:   map[string]string{"macaddress": n.MAC},
			SetName: n.Name,
			DHCP4:   true,
			DHCP6:   true,
		}); err != nil {
			return "", err
		}
		eths.Content = append(eths.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: n.Name}, &v)
	}
	doc := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "version"}, {Kind: yaml.ScalarNode, Tag: "!!int", Value: "2"},
		{Kind: yaml.ScalarNode, Value: "ethernets"}, eths,
	}}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// sshdSocketUnit makes the guest's sshd reachable over vsock, one sshd -i per
// connection, without depending on how the distribution starts sshd.
var sshdSocketUnit = fmt.Sprintf(`[Unit]
Description=OpenSSH over vsock for seiro

[Socket]
ListenStream=vsock::%d
Accept=yes

[Install]
WantedBy=sockets.target
`, SSHVSockPort)

const sshdServiceUnit = `[Unit]
Description=OpenSSH per-connection server over vsock for seiro

[Service]
ExecStart=-/usr/sbin/sshd -i
StandardInput=socket
StandardError=journal
RuntimeDirectory=sshd
RuntimeDirectoryPreserve=yes
`

type mergeRule struct {
	Name     string   `yaml:"name"`
	Settings []string `yaml:"settings"`
}

type writeFile struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

type guestConfig struct {
	MergeHow   []mergeRule       `yaml:"merge_how"`
	SSHKeys    map[string]string `yaml:"ssh_keys"`
	WriteFiles []writeFile       `yaml:"write_files"`
	RunCmd     [][]string        `yaml:"runcmd"`
}

// UserData combines the declared user-data with the cloud-config seiro needs:
// the host key and sshd listening on vsock. The seiro part comes last, appends
// to lists and does not override declared values, so the declared write_files
// and runcmd are kept (and a declared ed25519 host key would win).
func UserData(declared string, key *hostkey.Key) (string, error) {
	cfg := guestConfig{
		MergeHow: []mergeRule{
			{Name: "list", Settings: []string{"append"}},
			// replace would overwrite whole lists before they could be appended.
			{Name: "dict", Settings: []string{"no_replace", "recurse_list"}},
		},
		SSHKeys: map[string]string{
			"ed25519_private": key.Private,
			"ed25519_public":  key.Public,
		},
		WriteFiles: []writeFile{
			// Loaded early on later boots so that the socket can bind.
			{Path: "/etc/modules-load.d/seiro-vsock.conf", Content: "vmw_vsock_virtio_transport\n"},
			{Path: "/etc/systemd/system/seiro-sshd-vsock.socket", Content: sshdSocketUnit},
			{Path: "/etc/systemd/system/seiro-sshd-vsock@.service", Content: sshdServiceUnit},
		},
		RunCmd: [][]string{{"systemctl", "enable", "--now", "seiro-sshd-vsock.socket"}},
	}
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return cloudinit.Multipart(
		cloudinit.Part{ContentType: cloudinit.TypeDetect, Content: declared},
		cloudinit.Part{ContentType: cloudinit.TypeCloudConfig, Content: b.String()},
	)
}

// PasstArgs returns the passt arguments for a vhost-user NIC that publishes ports.
func PasstArgs(socket string, ports []spec.Port) []string {
	args := []string{"--foreground", "--quiet", "--vhost-user", "--socket", socket}
	var tcp, udp []string
	for _, p := range ports {
		spec := strconv.Itoa(int(p.Host)) + ":" + strconv.Itoa(int(p.Guest))
		if p.Protocol == "udp" {
			udp = append(udp, spec)
		} else {
			tcp = append(tcp, spec)
		}
	}
	if len(tcp) > 0 {
		args = append(args, "--tcp-ports", strings.Join(tcp, ","))
	}
	if len(udp) > 0 {
		args = append(args, "--udp-ports", strings.Join(udp, ","))
	}
	return args
}

// credential renders an SMBIOS OEM string that passes a credential to systemd
// (256 and later) in the guest. Older guests ignore it.
func credential(name, value string) string {
	return "io.systemd.credential.binary:" + name + "=" + base64.StdEncoding.EncodeToString([]byte(value))
}

// GuestCredentials returns the systemd credentials that realize the enabled
// tweaks in the guest.
//
// With EarlyNICNames, each NIC gets a .link file that gives it its declared name as soon as udev
// sees it, even in the initrd. Otherwise an initrd that configures the network
// (dracut, as on Ubuntu 26.04) leaves the NIC busy with an address under its
// predictable name, and cloud-init fails to rename it, so the network-config
// never applies.
//
// With SkipNetworkWait and no NIC, systemd-networkd-wait-online, which would
// wait for its timeout (about two minutes) on every boot, is made a no-op.
func GuestCredentials(nics []NIC, tw spec.Tweaks) []string {
	if len(nics) == 0 {
		if !tw.SkipNetworkWait {
			return nil
		}
		return []string{credential("systemd.unit-dropin.systemd-networkd-wait-online.service", "[Service]\nExecStart=\nExecStart=true\n")}
	}
	if !tw.EarlyNICNames {
		return nil
	}
	var creds []string
	for _, n := range nics {
		creds = append(creds, credential("network.link.10-seiro-"+n.Name,
			fmt.Sprintf("[Match]\nMACAddress=%s\n\n[Link]\nNamePolicy=\nName=%s\n", n.MAC, n.Name)))
	}
	return creds
}

// VMMArgs returns the cloud-hypervisor arguments for the planned machine.
func VMMArgs(p *Machine, m *spec.Machine, firmware string) []string {
	memory := fmt.Sprintf("size=%dM", m.Memory/size.MiB)
	if len(p.NICs) > 0 {
		// vhost-user devices need guest memory shared with the backend.
		memory += ",shared=on"
	}
	args := []string{"--api-socket", "path=" + p.APISocket}
	if p.Boot != nil {
		args = append(args, "--kernel", p.Boot.Kernel)
		if p.Boot.Initrd != "" {
			args = append(args, "--initramfs", p.Boot.Initrd)
		}
		args = append(args, "--cmdline", p.Boot.Cmdline)
	} else {
		args = append(args, "--firmware", firmware)
	}
	args = append(args,
		"--cpus", "boot="+strconv.Itoa(m.CPUs),
		"--memory", memory,
		"--serial", "socket="+p.SerialSocket,
		"--console", "off",
		"--vsock", fmt.Sprintf("cid=%d,socket=%s", GuestCID, p.VSockSocket),
	)
	if creds := GuestCredentials(p.NICs, m.Tweaks); len(creds) > 0 {
		args = append(args, "--platform", "oem_strings=["+strings.Join(creds, ",")+"]")
	}

	// Declared disks come first, in order, so that they appear as vda, vdb, ...
	// The image type is always explicit: auto-detection disables backing files.
	var disks []string
	for i, d := range p.Disks {
		opt := fmt.Sprintf("path=%s,image_type=%s,id=disk%d", d.Path, d.Format, i)
		if d.Backing != "" {
			opt += ",backing_files=on"
		}
		disks = append(disks, opt)
	}
	if p.Boot != nil && p.Boot.Rootfs != "" {
		disks = append(disks, fmt.Sprintf("path=%s,image_type=raw,readonly=on,id=rootfs,serial=%s", p.Boot.Rootfs, RootfsSerial))
	}
	if p.Seed != nil {
		disks = append(disks, fmt.Sprintf("path=%s,image_type=raw,readonly=on,id=seed", p.Seed.Path))
	}
	args = append(args, "--disk")
	args = append(args, disks...)

	if len(p.NICs) > 0 {
		args = append(args, "--net")
		for _, n := range p.NICs {
			args = append(args, fmt.Sprintf("vhost_user=true,socket=%s,mac=%s,id=nic-%s", n.Socket, n.MAC, n.Name))
		}
	}
	return args
}
