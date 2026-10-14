package plan_test

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/takumin/seiro/internal/disk"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/hostkey"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/qemu"
	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

var img = &spec.Image{URL: "https://example.com/a.img", SHA256: strings.Repeat("a", 64)}

func fixture(t *testing.T) (*env.Env, plan.Resources) {
	t.Helper()
	e, err := env.New(env.Dirs{State: "/s", Runtime: "/r", Cache: "/c"}, "/p/lab/Seirofile")
	if err != nil {
		t.Fatal(err)
	}
	return e, plan.Resources{
		CloudHypervisor: "/bin/cloud-hypervisor",
		QEMU:            "/bin/qemu-system-x86_64",
		VSockDevice:     "/bin/vhost-device-vsock",
		Passt:           "/bin/passt",
		Firmware:        "/c/firmware/CLOUDHV.fd",
		QEMUFirmware:    &qemu.Firmware{Code: "/f/OVMF_CODE.fd", VarsTemplate: "/f/OVMF_VARS.fd"},
		Images:          map[string]plan.Image{img.CacheKey(): {Path: "/c/images/a/data", Info: disk.Info{Format: disk.FormatQCOW2, VirtualSize: 3 * size.GiB}}},
		HostKey:         hostKey,
	}
}

var hostKey = &hostkey.Key{Private: "PRIVATE\nKEY\n", Public: "ssh-ed25519 AAAA"}

func machine() *spec.Machine {
	return &spec.Machine{
		Name:      "dev",
		CPUs:      2,
		Memory:    2 * size.GiB,
		Disks:     []spec.Disk{{Image: img, Size: 20 * size.GiB}, {Size: 100 * size.GiB}},
		NICs:      []spec.NIC{{Name: "eth0", Network: spec.NetworkInternet}},
		Ports:     []spec.Port{{Host: 2222, Guest: 22, Protocol: "tcp"}, {Host: 8080, Guest: 80, Protocol: "tcp"}, {Host: 5353, Guest: 53, Protocol: "udp"}},
		CloudInit: &spec.CloudInit{UserData: "#cloud-config\nusers:\n  - name: dev\n"},
		Tweaks:    spec.DefaultTweaks(),
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	p, err := plan.Build(e, machine(), res)
	if err != nil {
		t.Fatal(err)
	}
	mp := e.Machine("dev")

	wantDisks := []plan.Disk{
		{Path: mp.Disk(0, "qcow2"), Format: "qcow2", Size: 20 * size.GiB, Backing: "/c/images/a/data"},
		{Path: mp.Disk(1, "raw"), Format: "raw", Size: 100 * size.GiB},
	}
	if !slices.Equal(p.Disks, wantDisks) {
		t.Errorf("disks = %+v\nwant %+v", p.Disks, wantDisks)
	}

	if len(p.NICs) != 1 {
		t.Fatalf("nics = %+v", p.NICs)
	}
	nic := p.NICs[0]
	if nic.MAC != plan.MAC(e.ID, "dev", "eth0") || nic.Socket != mp.PasstSocket("eth0") {
		t.Errorf("nic = %+v", nic)
	}
	wantPasst := []string{"--foreground", "--quiet", "--vhost-user", "--socket", mp.PasstSocket("eth0"), "--tcp-ports", "2222:22,8080:80", "--udp-ports", "5353:53"}
	if nic.Backend.Path != "/bin/passt" || !slices.Equal(nic.Backend.Args, wantPasst) || nic.Backend.Log != mp.PasstLog("eth0") {
		t.Errorf("passt = %+v\nwant args %q", nic.Backend, wantPasst)
	}

	wantVMM := []string{
		"--api-socket", "path=" + mp.APISocket(),
		"--firmware", "/c/firmware/CLOUDHV.fd",
		"--cpus", "boot=2",
		"--memory", "size=2048M,shared=on",
		"--serial", "socket=" + mp.SerialSocket(),
		"--console", "off",
		"--vsock", "cid=3,socket=" + mp.VSockSocket(),
		"--platform", "oem_strings=[" + strings.Join(plan.GuestCredentials(p.NICs, spec.DefaultTweaks()), ",") + "]",
		"--disk",
		"path=" + mp.Disk(0, "qcow2") + ",image_type=qcow2,id=disk0,backing_files=on",
		"path=" + mp.Disk(1, "raw") + ",image_type=raw,id=disk1",
		"path=" + mp.Seed() + ",image_type=raw,readonly=on,id=seed",
		"--net",
		"vhost_user=true,socket=" + mp.PasstSocket("eth0") + ",mac=" + nic.MAC + ",id=nic-eth0",
	}
	if !slices.Equal(p.VMM.Args, wantVMM) {
		t.Errorf("vmm args:\n%q\nwant:\n%q", p.VMM.Args, wantVMM)
	}

	if p.UEFI != nil || p.VSockDevice != nil {
		t.Errorf("uefi = %+v, vsock device = %+v", p.UEFI, p.VSockDevice)
	}

	if p.Seed == nil || p.Seed.Path != mp.Seed() {
		t.Fatalf("seed = %+v", p.Seed)
	}
	if p.Seed.Data.MetaData != "instance-id: "+e.ID+"-dev\nlocal-hostname: dev\n" {
		t.Errorf("meta-data = %q", p.Seed.Data.MetaData)
	}
	want, err := plan.UserData(machine().CloudInit.UserData, hostKey)
	if err != nil {
		t.Fatal(err)
	}
	if p.Seed.Data.UserData != want {
		t.Errorf("user-data:\n%s\nwant:\n%s", p.Seed.Data.UserData, want)
	}
	wantSSH := plan.SSH{User: "dev", Port: plan.SSHVSockPort, KnownHosts: mp.KnownHosts()}
	if p.SSH == nil || *p.SSH != wantSSH || p.VSockSocket != mp.VSockSocket() {
		t.Errorf("ssh = %+v, vsock = %q", p.SSH, p.VSockSocket)
	}
	wantNet := "version: 2\nethernets:\n  eth0:\n    match:\n      macaddress: " + nic.MAC + "\n    set-name: eth0\n    dhcp4: true\n    dhcp6: true\n"
	if p.Seed.Data.NetworkConfig != wantNet {
		t.Errorf("network-config:\n%s\nwant:\n%s", p.Seed.Data.NetworkConfig, wantNet)
	}
}

func TestBuildQEMU(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	m := machine()
	m.VMM = spec.VMMQEMU
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	mp := e.Machine("dev")

	wantUEFI := plan.UEFI{Code: "/f/OVMF_CODE.fd", Vars: mp.NVRAM(), VarsTemplate: "/f/OVMF_VARS.fd"}
	if p.UEFI == nil || *p.UEFI != wantUEFI {
		t.Errorf("uefi = %+v, want %+v", p.UEFI, wantUEFI)
	}
	wantDevice := plan.VSockDevice{Socket: mp.VSockDeviceSocket(), Backend: plan.Process{
		Path: "/bin/vhost-device-vsock",
		Args: []string{"--guest-cid", "3", "--socket", mp.VSockDeviceSocket(), "--uds-path", mp.VSockSocket()},
		Log:  mp.VSockDeviceLog(),
	}}
	if d := p.VSockDevice; d == nil || d.Socket != wantDevice.Socket || d.Backend.Path != wantDevice.Backend.Path ||
		!slices.Equal(d.Backend.Args, wantDevice.Backend.Args) || d.Backend.Log != wantDevice.Backend.Log {
		t.Errorf("vsock device = %+v, want %+v", p.VSockDevice, wantDevice)
	}

	var smbios []string
	for _, c := range plan.GuestCredentials(p.NICs, spec.DefaultTweaks()) {
		smbios = append(smbios, "-smbios", "type=11,value="+c)
	}
	wantVMM := []string{
		"-nodefaults", "-no-user-config", "-display", "none",
		"-object", "memory-backend-memfd,id=mem,share=on,size=2048M",
		"-machine", "q35,accel=kvm,memory-backend=mem",
		"-cpu", "host",
		"-smp", "2",
		"-m", "2048M",
		"-qmp", "unix:" + mp.APISocket() + ",server=on,wait=off",
		"-chardev", "socket,id=serial,server=on,wait=off,path=" + mp.SerialSocket(),
		"-serial", "chardev:serial",
		"-drive", "if=pflash,unit=0,format=raw,readonly=on,file=/f/OVMF_CODE.fd",
		"-drive", "if=pflash,unit=1,format=raw,file=" + mp.NVRAM(),
	}
	wantVMM = append(wantVMM, smbios...)
	wantVMM = append(wantVMM,
		"-drive", "if=none,id=disk0,format=qcow2,file="+mp.Disk(0, "qcow2"), "-device", "virtio-blk-pci,drive=disk0",
		"-drive", "if=none,id=disk1,format=raw,file="+mp.Disk(1, "raw"), "-device", "virtio-blk-pci,drive=disk1",
		"-drive", "if=none,id=seed,format=raw,readonly=on,file="+mp.Seed(), "-device", "virtio-blk-pci,drive=seed",
		"-chardev", "socket,id=nic-eth0,path="+mp.PasstSocket("eth0"),
		"-netdev", "vhost-user,id=nic-eth0,chardev=nic-eth0",
		"-device", "virtio-net-pci,netdev=nic-eth0,mac="+p.NICs[0].MAC,
		"-chardev", "socket,id=vsock,path="+mp.VSockDeviceSocket(),
		"-device", "vhost-user-vsock-pci,chardev=vsock",
	)
	if p.VMM.Path != "/bin/qemu-system-x86_64" || !slices.Equal(p.VMM.Args, wantVMM) {
		t.Errorf("vmm %s args:\n%q\nwant:\n%q", p.VMM.Path, p.VMM.Args, wantVMM)
	}
}

func TestBuildQEMUEscapesCommas(t *testing.T) {
	t.Parallel()

	e, err := env.New(env.Dirs{State: "/s,1", Runtime: "/r,1", Cache: "/c"}, "/p/lab/Seirofile")
	if err != nil {
		t.Fatal(err)
	}
	_, res := fixture(t)
	m := &spec.Machine{Name: "a", VMM: spec.VMMQEMU, CPUs: 1, Memory: 512 * size.MiB, Disks: []spec.Disk{{Size: size.GiB}}}
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unix:/r,,1/" + e.ID + "/a/api.sock,server=on,wait=off",
		"if=none,id=disk0,format=raw,file=/s,,1/envs/" + e.ID + "/machines/a/disk0.raw",
	} {
		if !slices.Contains(p.VMM.Args, want) {
			t.Errorf("vmm args %q do not contain %q", p.VMM.Args, want)
		}
	}
}

func TestBuildQEMUWithoutFirmware(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	res.QEMUFirmware = nil
	m := machine()
	m.VMM = spec.VMMQEMU
	if _, err := plan.Build(e, m, res); err == nil || !strings.Contains(err.Error(), "no UEFI firmware") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildMinimal(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	m := &spec.Machine{Name: "a", CPUs: 1, Memory: 512 * size.MiB, Disks: []spec.Disk{{Size: size.GiB}}, Tweaks: spec.DefaultTweaks()}
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	if p.Seed != nil || p.SSH != nil || len(p.NICs) != 0 {
		t.Errorf("unexpected seed or nics: %+v", p)
	}
	if !slices.Contains(p.VMM.Args, "size=512M") || slices.Contains(p.VMM.Args, "--net") {
		t.Errorf("vmm args = %q", p.VMM.Args)
	}
	// Without a NIC, systemd-networkd-wait-online is disabled through SMBIOS.
	if i := slices.Index(p.VMM.Args, "--platform"); i < 0 || p.VMM.Args[i+1] != "oem_strings=["+strings.Join(plan.GuestCredentials(nil, spec.DefaultTweaks()), ",")+"]" {
		t.Errorf("vmm args = %q", p.VMM.Args)
	}
}

var (
	kernel = &spec.Image{Path: "/b/vmlinuz"}
	initrd = &spec.Image{Path: "/b/initrd.img"}
	rootfs = &spec.Image{Path: "/b/rootfs.squashfs"}
)

func liveFixture(t *testing.T) (*env.Env, plan.Resources) {
	t.Helper()
	e, res := fixture(t)
	res.Firmware = ""
	res.Images[kernel.Key()] = plan.Image{Path: kernel.Path, Info: disk.Info{Format: disk.FormatRaw, VirtualSize: 13 * size.MiB}}
	res.Images[initrd.Key()] = plan.Image{Path: initrd.Path, Info: disk.Info{Format: disk.FormatRaw, VirtualSize: 36 * size.MiB}}
	res.Images[rootfs.Key()] = plan.Image{Path: rootfs.Path, Info: disk.Info{Format: disk.FormatRaw, VirtualSize: 292 * size.MiB, Filesystem: disk.FilesystemSquashfs}}
	return e, res
}

func liveMachine() *spec.Machine {
	m := machine()
	m.Live = &spec.Live{Kernel: kernel, Initrd: initrd, Rootfs: rootfs, Cmdline: "anynet"}
	m.Disks = []spec.Disk{{Size: 10 * size.GiB}}
	m.Ports = nil
	return m
}

func TestBuildLive(t *testing.T) {
	t.Parallel()

	e, res := liveFixture(t)
	p, err := plan.Build(e, liveMachine(), res)
	if err != nil {
		t.Fatal(err)
	}
	mp := e.Machine("dev")

	wantCmdline := "console=ttyS0 root=live:/dev/disk/by-id/virtio-seiro-rootfs ds=nocloud anynet"
	wantBoot := plan.Boot{Kernel: kernel.Path, Initrd: initrd.Path, Cmdline: wantCmdline, Rootfs: rootfs.Path}
	if p.Boot == nil || *p.Boot != wantBoot {
		t.Errorf("boot = %+v, want %+v", p.Boot, wantBoot)
	}
	// The rootfs is not one of the declared disks: it is neither created nor compared.
	if len(p.Disks) != 1 || p.Disks[0].Path != mp.Disk(0, "raw") {
		t.Errorf("disks = %+v", p.Disks)
	}

	wantVMM := []string{
		"--api-socket", "path=" + mp.APISocket(),
		"--kernel", kernel.Path,
		"--initramfs", initrd.Path,
		"--cmdline", wantCmdline,
		"--cpus", "boot=2",
		"--memory", "size=2048M,shared=on",
		"--serial", "socket=" + mp.SerialSocket(),
		"--console", "off",
		"--vsock", "cid=3,socket=" + mp.VSockSocket(),
		"--platform", "oem_strings=[" + strings.Join(plan.GuestCredentials(p.NICs, spec.DefaultTweaks()), ",") + "]",
		"--disk",
		"path=" + mp.Disk(0, "raw") + ",image_type=raw,id=disk0",
		"path=" + rootfs.Path + ",image_type=raw,readonly=on,id=rootfs,serial=seiro-rootfs",
		"path=" + mp.Seed() + ",image_type=raw,readonly=on,id=seed",
		"--net",
		"vhost_user=true,socket=" + mp.PasstSocket("eth0") + ",mac=" + p.NICs[0].MAC + ",id=nic-eth0",
	}
	if !slices.Equal(p.VMM.Args, wantVMM) {
		t.Errorf("vmm args:\n%q\nwant:\n%q", p.VMM.Args, wantVMM)
	}
}

func TestBuildLiveQEMU(t *testing.T) {
	t.Parallel()

	e, res := liveFixture(t)
	res.QEMUFirmware = nil
	m := liveMachine()
	m.VMM = spec.VMMQEMU
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	mp := e.Machine("dev")
	if p.UEFI != nil {
		t.Errorf("uefi = %+v", p.UEFI)
	}
	for _, want := range [][]string{
		{"-kernel", kernel.Path, "-initrd", initrd.Path, "-append", p.Boot.Cmdline},
		{"-drive", "if=none,id=disk0,format=raw,file=" + mp.Disk(0, "raw"), "-device", "virtio-blk-pci,drive=disk0",
			"-drive", "if=none,id=rootfs,format=raw,readonly=on,file=" + rootfs.Path, "-device", "virtio-blk-pci,drive=rootfs,serial=seiro-rootfs"},
	} {
		if !containsSeq(p.VMM.Args, want) {
			t.Errorf("vmm args %q do not contain %q", p.VMM.Args, want)
		}
	}
	if slices.ContainsFunc(p.VMM.Args, func(a string) bool { return strings.Contains(a, "pflash") }) {
		t.Errorf("a live machine boots no firmware: %q", p.VMM.Args)
	}
}

func containsSeq(s, seq []string) bool {
	for i := range s {
		if i+len(seq) <= len(s) && slices.Equal(s[i:i+len(seq)], seq) {
			return true
		}
	}
	return false
}

func TestLiveCmdline(t *testing.T) {
	t.Parallel()

	m := liveMachine()
	m.CloudInit = nil
	m.Live.Cmdline = ""
	if got, want := plan.LiveCmdline(m), "console=ttyS0 root=live:/dev/disk/by-id/virtio-seiro-rootfs"; got != want {
		t.Errorf("cmdline = %q, want %q", got, want)
	}
}

func TestBuildLiveErrors(t *testing.T) {
	t.Parallel()

	e, res := liveFixture(t)

	notSquashfs := liveMachine()
	notSquashfs.Live.Rootfs = kernel
	if _, err := plan.Build(e, notSquashfs, res); err == nil || !strings.Contains(err.Error(), "is not a squashfs image") {
		t.Errorf("expected squashfs error, got %v", err)
	}

	noInitrd := liveMachine()
	noInitrd.Live.Initrd = nil
	if _, err := plan.Build(e, noInitrd, res); err == nil || !strings.Contains(err.Error(), "needs an initrd") {
		t.Errorf("expected initrd error, got %v", err)
	}

	missing := liveMachine()
	missing.Live.Kernel = &spec.Image{Path: "/b/other"}
	if _, err := plan.Build(e, missing, res); err == nil || !strings.Contains(err.Error(), "live: kernel /b/other is not available") {
		t.Errorf("expected missing kernel error, got %v", err)
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)

	small := machine()
	small.Disks[0].Size = size.GiB
	if _, err := plan.Build(e, small, res); err == nil || !strings.Contains(err.Error(), "smaller than the image (3GiB)") {
		t.Errorf("expected size error, got %v", err)
	}

	nokey := machine()
	res.HostKey = nil
	if _, err := plan.Build(e, nokey, res); err == nil || !strings.Contains(err.Error(), "no SSH host key") {
		t.Errorf("expected host key error, got %v", err)
	}
	res.HostKey = hostKey

	missing := machine()
	missing.Disks[0].Image = &spec.Image{URL: "https://example.com/other.img"}
	if _, err := plan.Build(e, missing, res); err == nil || !strings.Contains(err.Error(), "is not available") {
		t.Errorf("expected missing image error, got %v", err)
	}
}

func TestExplicitMAC(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	m := machine()
	m.NICs[0].MAC = "02:00:00:00:00:01"
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	if p.NICs[0].MAC != "02:00:00:00:00:01" {
		t.Errorf("mac = %q", p.NICs[0].MAC)
	}
}

func TestMAC(t *testing.T) {
	t.Parallel()

	a := plan.MAC("env", "m", "eth0")
	if a != plan.MAC("env", "m", "eth0") {
		t.Error("MAC is not deterministic")
	}
	if a == plan.MAC("env", "m", "eth1") || a == plan.MAC("env2", "m", "eth0") {
		t.Error("MAC collides across inputs")
	}
	if !strings.HasPrefix(a, "02:") || len(a) != 17 {
		t.Errorf("MAC = %q", a)
	}
}

func TestPasstArgsWithoutPorts(t *testing.T) {
	t.Parallel()

	got := plan.PasstArgs("/r/s.sock", nil)
	want := []string{"--foreground", "--quiet", "--vhost-user", "--socket", "/r/s.sock"}
	if !slices.Equal(got, want) {
		t.Errorf("PasstArgs = %q", got)
	}
}

func TestUserData(t *testing.T) {
	t.Parallel()

	got, err := plan.UserData("#!/bin/sh\necho hi\n", hostKey)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(p)
		dec, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, string(dec))
	}
	if len(parts) != 2 || parts[0] != "#!/bin/sh\necho hi\n" {
		t.Fatalf("parts = %q", parts)
	}

	var cfg struct {
		MergeHow []map[string]any  `yaml:"merge_how"`
		SSHKeys  map[string]string `yaml:"ssh_keys"`
		Files    []struct {
			Path    string `yaml:"path"`
			Content string `yaml:"content"`
		} `yaml:"write_files"`
		RunCmd [][]string `yaml:"runcmd"`
	}
	if !strings.HasPrefix(parts[1], "#cloud-config\n") {
		t.Fatalf("seiro part:\n%s", parts[1])
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SSHKeys["ed25519_private"] != hostKey.Private || cfg.SSHKeys["ed25519_public"] != hostKey.Public {
		t.Errorf("ssh_keys = %+v", cfg.SSHKeys)
	}
	var socket string
	for _, f := range cfg.Files {
		if f.Path == "/etc/systemd/system/seiro-sshd-vsock.socket" {
			socket = f.Content
		}
	}
	if !strings.Contains(socket, "ListenStream=vsock::10022\n") {
		t.Errorf("socket unit:\n%s", socket)
	}
	if len(cfg.RunCmd) != 1 || !slices.Equal(cfg.RunCmd[0], []string{"systemctl", "enable", "--now", "seiro-sshd-vsock.socket"}) {
		t.Errorf("runcmd = %q", cfg.RunCmd)
	}
	if len(cfg.MergeHow) != 2 {
		t.Errorf("merge_how = %+v", cfg.MergeHow)
	}
}

func TestGuestCredentials(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, oem string) (string, string) {
		t.Helper()
		name, value, ok := strings.Cut(strings.TrimPrefix(oem, "io.systemd.credential.binary:"), "=")
		if !ok || !strings.HasPrefix(oem, "io.systemd.credential.binary:") || strings.Contains(oem, ",") {
			t.Fatalf("oem string = %q", oem)
		}
		b, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		return name, string(b)
	}

	none := plan.GuestCredentials(nil, spec.DefaultTweaks())
	if len(none) != 1 {
		t.Fatalf("credentials = %q", none)
	}
	if name, value := decode(t, none[0]); name != "systemd.unit-dropin.systemd-networkd-wait-online.service" || value != "[Service]\nExecStart=\nExecStart=true\n" {
		t.Errorf("%s = %q", name, value)
	}

	nics := []plan.NIC{{Name: "eth0", MAC: "02:00:00:00:00:01"}, {Name: "lan", MAC: "02:00:00:00:00:02"}}
	creds := plan.GuestCredentials(nics, spec.DefaultTweaks())
	if len(creds) != 2 {
		t.Fatalf("credentials = %q", creds)
	}
	if got := plan.GuestCredentials(nics, spec.Tweaks{SkipNetworkWait: true}); got != nil {
		t.Errorf("credentials without EarlyNICNames = %q", got)
	}
	if got := plan.GuestCredentials(nil, spec.Tweaks{EarlyNICNames: true}); got != nil {
		t.Errorf("credentials without SkipNetworkWait = %q", got)
	}
	for i, n := range nics {
		name, value := decode(t, creds[i])
		want := "[Match]\nMACAddress=" + n.MAC + "\n\n[Link]\nNamePolicy=\nName=" + n.Name + "\n"
		if name != "network.link.10-seiro-"+n.Name || value != want {
			t.Errorf("%s = %q, want %q", name, value, want)
		}
	}
}

func TestBuildWithoutTweaks(t *testing.T) {
	t.Parallel()

	e, res := fixture(t)
	m := machine()
	m.Tweaks = spec.Tweaks{}
	p, err := plan.Build(e, m, res)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.VMM.Args, "--platform") {
		t.Errorf("vmm args = %q", p.VMM.Args)
	}
}
