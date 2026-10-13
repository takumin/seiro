package seirofile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/seirofile"
	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

const header = `
img = image(url = "https://example.com/a.img", sha256 = "6A81C37564DB9B1EE84E141922625E1D7C5B389B99BB3C572E0243607D5BB4D2")
`

func eval(t *testing.T, src string) (*spec.Environment, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "key.pub"), []byte("ssh-ed25519 AAAA test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return seirofile.Eval(filepath.Join(dir, seirofile.Name), []byte(header+src))
}

func TestEvalFull(t *testing.T) {
	t.Parallel()

	env, err := eval(t, `
machine(
	name = "dev",
	cpus = 2,
	memory = "2GiB",
	disks = [disk(image = img, size = "20GiB"), disk(size = "1TiB")],
	nics = [nic("eth0", network = internet, mac = "02:AA:BB:CC:DD:EE")],
	ports = [port(host = 2222, guest = 22), port(host = 5353, guest = 53, protocol = "udp")],
	cloud_init = cloud_init(user_data = {
		"users": [{"name": "dev", "ssh_authorized_keys": [file("key.pub")]}],
		"package_update": True,
	}),
)
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env.Machines) != 1 {
		t.Fatalf("got %d machines, want 1", len(env.Machines))
	}
	m := env.Machines[0]
	if m.Name != "dev" || m.CPUs != 2 || m.Memory != 2*size.GiB {
		t.Errorf("unexpected machine: %+v", m)
	}
	if len(m.Disks) != 2 || m.Disks[0].Image == nil || m.Disks[0].Size != 20*size.GiB || m.Disks[1].Image != nil || m.Disks[1].Size != size.TiB {
		t.Errorf("unexpected disks: %+v", m.Disks)
	}
	if m.Disks[0].Image.SHA256 != "6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2" {
		t.Errorf("sha256 was not normalized to lowercase: %q", m.Disks[0].Image.SHA256)
	}
	if len(m.NICs) != 1 || m.NICs[0] != (spec.NIC{Name: "eth0", Network: spec.NetworkInternet, MAC: "02:aa:bb:cc:dd:ee"}) {
		t.Errorf("unexpected nics: %+v", m.NICs)
	}
	wantPorts := []spec.Port{{Host: 2222, Guest: 22, Protocol: "tcp"}, {Host: 5353, Guest: 53, Protocol: "udp"}}
	if len(m.Ports) != 2 || m.Ports[0] != wantPorts[0] || m.Ports[1] != wantPorts[1] {
		t.Errorf("unexpected ports: %+v", m.Ports)
	}
	wantUserData := "#cloud-config\n" +
		"users:\n" +
		"  - name: dev\n" +
		"    ssh_authorized_keys:\n" +
		"      - |\n" +
		"        ssh-ed25519 AAAA test\n" +
		"package_update: true\n"
	if m.CloudInit == nil || m.CloudInit.UserData != wantUserData {
		t.Errorf("unexpected user-data:\n%s\nwant:\n%s", m.CloudInit.UserData, wantUserData)
	}
	if err := env.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestEvalDefaults(t *testing.T) {
	t.Parallel()

	env, err := eval(t, `machine(name = "a", disks = [disk(image = img, size = "10GiB")])`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := env.Machines[0]
	if m.CPUs != 1 || m.Memory != 512*size.MiB || len(m.NICs) != 0 || len(m.Ports) != 0 || m.CloudInit != nil {
		t.Errorf("unexpected defaults: %+v", m)
	}
}

func TestEvalTweaks(t *testing.T) {
	t.Parallel()

	env, err := eval(t, `
custom = tweaks(early_nic_names = False)
machine(name = "a", disks = [disk(image = img, size = "10GiB")])
machine(name = "b", disks = [disk(image = img, size = "10GiB")], tweaks = custom)
machine(name = "c", disks = [disk(image = img, size = "10GiB")], tweaks = False)
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]spec.Tweaks{
		"a": spec.DefaultTweaks(),
		"b": {SkipNetworkWait: true},
		"c": {},
	}
	for _, m := range env.Machines {
		if got := m.Tweaks; got != want[m.Name] {
			t.Errorf("%s: tweaks = %+v, want %+v", m.Name, got, want[m.Name])
		}
	}
}

func TestEvalLive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, seirofile.Name)
	env, err := seirofile.Eval(path, []byte(`
build = "releases/amd64"
machine(
	name = "live",
	boot = live(
		kernel = image(path = build + "/vmlinuz"),
		initrd = image(path = "/abs/../abs/initrd.img"),
		rootfs = image(url = "https://example.com/rootfs.squashfs"),
		cmdline = "anynet",
	),
)
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := env.Machines[0]
	want := &spec.Live{
		Kernel:  &spec.Image{Path: filepath.Join(dir, "releases/amd64/vmlinuz")},
		Initrd:  &spec.Image{Path: "/abs/initrd.img"},
		Rootfs:  &spec.Image{URL: "https://example.com/rootfs.squashfs"},
		Cmdline: "anynet",
	}
	if m.Live == nil || *m.Live.Kernel != *want.Kernel || *m.Live.Initrd != *want.Initrd || *m.Live.Rootfs != *want.Rootfs || m.Live.Cmdline != want.Cmdline {
		t.Errorf("live = %+v, want %+v", m.Live, want)
	}
	if len(m.Disks) != 0 {
		t.Errorf("disks = %+v, want none", m.Disks)
	}
	if err := env.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestEvalCloudInitString(t *testing.T) {
	t.Parallel()

	env, err := eval(t, `machine(name = "a", disks = [disk(size = "1GiB")], cloud_init = cloud_init(user_data = "#!/bin/sh\necho hi\n"))`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := env.Machines[0].CloudInit.UserData; got != "#!/bin/sh\necho hi\n" {
		t.Errorf("user-data = %q", got)
	}
}

func TestEvalYAMLQuoting(t *testing.T) {
	t.Parallel()

	env, err := eval(t, `machine(name = "a", disks = [disk(size = "1GiB")], cloud_init = cloud_init(user_data = {"z": "yes", "a": None, "n": 1, "f": 1.5, "t": ("x", "on")}))`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "#cloud-config\nz: \"yes\"\na: null\n\"n\": 1\nf: 1.5\nt:\n  - x\n  - \"on\"\n"
	if got := env.Machines[0].CloudInit.UserData; got != want {
		t.Errorf("user-data:\n%s\nwant:\n%s", got, want)
	}
}

func TestEvalErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		src  string
		want string
	}{
		"syntax":            {src: `machine(`, want: "got end of file"},
		"unknown builtin":   {src: `vm()`, want: "undefined: vm"},
		"network":           {src: `network("pxe")`, want: "planned for phase 3"},
		"load":              {src: `load("x.star", "y")`, want: "load() is not supported yet"},
		"bad memory":        {src: `machine(name = "a", memory = "1GB", disks = [])`, want: `machine "a": memory: invalid size`},
		"disks element":     {src: `machine(name = "a", disks = ["x"])`, want: "disks must contain disk() values, got string"},
		"nics element":      {src: `machine(name = "a", disks = [], nics = [1])`, want: "nics must contain nic() values"},
		"ports element":     {src: `machine(name = "a", disks = [], ports = [None])`, want: "ports must contain port() values"},
		"cloud_init type":   {src: `machine(name = "a", disks = [], cloud_init = {})`, want: "cloud_init must be a cloud_init()"},
		"disk image type":   {src: `disk(image = "x", size = "1GiB")`, want: "image must be an image()"},
		"disk size":         {src: `disk(size = "big")`, want: "invalid size"},
		"image url":         {src: `image(url = "ftp://x")`, want: "url must start with"},
		"image source":      {src: `image()`, want: "url or path is required"},
		"image both":        {src: `image(url = "https://x", path = "x")`, want: "url and path cannot both be set"},
		"boot type":         {src: `machine(name = "a", boot = img)`, want: "boot must be a live(), got image"},
		"live kernel":       {src: `live(kernel = "vmlinuz", rootfs = img)`, want: `"kernel": got string, want image`},
		"live rootfs":       {src: `live(kernel = img)`, want: "missing argument for rootfs"},
		"live initrd":       {src: `live(kernel = img, rootfs = img, initrd = "x")`, want: "initrd must be an image()"},
		"nic network":       {src: `nic("eth0", network = "internet")`, want: "network must be a network"},
		"port range":        {src: `port(host = 70000, guest = 22)`, want: "must be between 1 and 65535"},
		"user_data type":    {src: `cloud_init(user_data = 1)`, want: "user_data must be a dict or a string"},
		"user_data key":     {src: `cloud_init(user_data = {1: "x"})`, want: "dict keys must be strings"},
		"user_data value":   {src: `cloud_init(user_data = {"x": img})`, want: "x: cannot convert image to YAML"},
		"file missing":      {src: `file("nope")`, want: "no such file"},
		"tweaks type":       {src: `machine(name = "a", disks = [], tweaks = {})`, want: "tweaks must be a bool or a tweaks()"},
		"tweaks unknown":    {src: `tweaks(early_nic_name = False)`, want: "unexpected keyword argument \"early_nic_name\""},
		"tweaks value":      {src: `tweaks(skip_network_wait = "no")`, want: `"skip_network_wait": got string, want bool`},
		"positional misuse": {src: `machine("a", "b")`, want: "disks: got string, want iterable"},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := eval(t, tt.src)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestFind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, seirofile.Name), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := seirofile.Find(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(root, seirofile.Name) {
		t.Errorf("Find() = %q", got)
	}

	if _, err := seirofile.Find(t.TempDir()); !errors.Is(err, seirofile.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadValidates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, seirofile.Name)
	if err := os.WriteFile(p, []byte(`machine(name = "a", disks = [])`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := seirofile.Load(p); err == nil || !strings.Contains(err.Error(), "at least one disk") {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestExamples(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "id_ed25519.pub")); err != nil {
		t.Skip("examples read ~/.ssh/id_ed25519.pub")
	}
	paths, err := filepath.Glob("../../examples/*/Seirofile")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for _, p := range paths {
		if _, err := seirofile.Load(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
