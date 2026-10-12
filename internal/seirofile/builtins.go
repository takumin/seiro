package seirofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.starlark.net/starlark"

	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

// Defaults applied to machine() arguments.
const (
	DefaultCPUs   = 1
	DefaultMemory = "512MiB"
)

type builder struct {
	dir string
	env *spec.Environment
}

func (b *builder) predeclared() starlark.StringDict {
	return starlark.StringDict{
		"image":      starlark.NewBuiltin("image", b.image),
		"disk":       starlark.NewBuiltin("disk", b.disk),
		"machine":    starlark.NewBuiltin("machine", b.machine),
		"nic":        starlark.NewBuiltin("nic", b.nic),
		"port":       starlark.NewBuiltin("port", b.port),
		"cloud_init": starlark.NewBuiltin("cloud_init", b.cloudInit),
		"tweaks":     starlark.NewBuiltin("tweaks", b.tweaks),
		"file":       starlark.NewBuiltin("file", b.file),
		"network":    starlark.NewBuiltin("network", unsupported("network() is not supported yet (planned for phase 3)")),
		"internet":   networkValue{name: spec.NetworkInternet},
	}
}

func unsupported(msg string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
		return nil, errors.New(msg)
	}
}

func (b *builder) image(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var url, sum string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &url, "sha256?", &sum); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, fmt.Errorf("%s: url must start with https:// or http://", fn.Name())
	}
	return &imageValue{img: &spec.Image{URL: url, SHA256: strings.ToLower(sum)}}, nil
}

func (b *builder) tweaks(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	t := spec.DefaultTweaks()
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"early_nic_names?", &t.EarlyNICNames,
		"skip_network_wait?", &t.SkipNetworkWait,
	); err != nil {
		return nil, err
	}
	return tweaksValue{tweaks: t}, nil
}

func (b *builder) disk(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var sz string
	var img starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "size", &sz, "image?", &img); err != nil {
		return nil, err
	}
	n, err := size.Parse(sz)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	d := spec.Disk{Size: n}
	switch v := img.(type) {
	case starlark.NoneType:
	case *imageValue:
		d.Image = v.img
	default:
		return nil, fmt.Errorf("%s: image must be an image(), got %s", fn.Name(), img.Type())
	}
	return diskValue{disk: d}, nil
}

func (b *builder) machine(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name      string
		disks     starlark.Iterable
		cpus                        = DefaultCPUs
		memory                      = DefaultMemory
		nics      starlark.Iterable = starlark.NewList(nil)
		ports     starlark.Iterable = starlark.NewList(nil)
		cloudInit starlark.Value    = starlark.None
		tw        starlark.Value    = starlark.True
	)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"name", &name,
		"disks", &disks,
		"cpus?", &cpus,
		"memory?", &memory,
		"nics?", &nics,
		"ports?", &ports,
		"cloud_init?", &cloudInit,
		"tweaks?", &tw,
	); err != nil {
		return nil, err
	}
	errorf := func(format string, args ...any) error {
		return fmt.Errorf("%s %q: "+format, append([]any{fn.Name(), name}, args...)...)
	}

	mem, err := size.Parse(memory)
	if err != nil {
		return nil, errorf("memory: %w", err)
	}
	m := &spec.Machine{Name: name, CPUs: cpus, Memory: mem, Disks: []spec.Disk{}, NICs: []spec.NIC{}, Ports: []spec.Port{}}

	if err := each(disks, func(v starlark.Value) error {
		d, ok := v.(diskValue)
		if !ok {
			return errorf("disks must contain disk() values, got %s", v.Type())
		}
		m.Disks = append(m.Disks, d.disk)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := each(nics, func(v starlark.Value) error {
		n, ok := v.(nicValue)
		if !ok {
			return errorf("nics must contain nic() values, got %s", v.Type())
		}
		m.NICs = append(m.NICs, n.nic)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := each(ports, func(v starlark.Value) error {
		p, ok := v.(portValue)
		if !ok {
			return errorf("ports must contain port() values, got %s", v.Type())
		}
		m.Ports = append(m.Ports, p.port)
		return nil
	}); err != nil {
		return nil, err
	}
	switch v := cloudInit.(type) {
	case starlark.NoneType:
	case cloudInitValue:
		m.CloudInit = v.ci
	default:
		return nil, errorf("cloud_init must be a cloud_init(), got %s", cloudInit.Type())
	}
	switch v := tw.(type) {
	case starlark.Bool:
		// True enables every tweak and False disables them all.
		m.Tweaks = spec.Tweaks{EarlyNICNames: bool(v), SkipNetworkWait: bool(v)}
	case tweaksValue:
		m.Tweaks = v.tweaks
	default:
		return nil, errorf("tweaks must be a bool or a tweaks(), got %s", tw.Type())
	}

	b.env.Machines = append(b.env.Machines, m)
	return starlark.None, nil
}

func (b *builder) nic(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, mac string
	var network starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "name", &name, "network", &network, "mac?", &mac); err != nil {
		return nil, err
	}
	nw, ok := network.(networkValue)
	if !ok {
		return nil, fmt.Errorf("%s %q: network must be a network such as internet, got %s", fn.Name(), name, network.Type())
	}
	return nicValue{nic: spec.NIC{Name: name, Network: nw.name, MAC: strings.ToLower(mac)}}, nil
}

func (b *builder) port(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var host, guest int
	protocol := "tcp"
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "host", &host, "guest", &guest, "protocol?", &protocol); err != nil {
		return nil, err
	}
	for _, p := range []int{host, guest} {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("%s: port %d must be between 1 and 65535", fn.Name(), p)
		}
	}
	return portValue{port: spec.Port{Host: uint16(host), Guest: uint16(guest), Protocol: protocol}}, nil // #nosec G115 -- range checked above
}

func (b *builder) cloudInit(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var userData starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "user_data", &userData); err != nil {
		return nil, err
	}
	var rendered string
	switch v := userData.(type) {
	case starlark.String:
		rendered = string(v)
	case *starlark.Dict:
		y, err := toYAML(v)
		if err != nil {
			return nil, fmt.Errorf("%s: user_data: %w", fn.Name(), err)
		}
		rendered = "#cloud-config\n" + y
	default:
		return nil, fmt.Errorf("%s: user_data must be a dict or a string, got %s", fn.Name(), userData.Type())
	}
	return cloudInitValue{ci: &spec.CloudInit{UserData: rendered}}, nil
}

func (b *builder) file(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var p string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "path", &p); err != nil {
		return nil, err
	}
	resolved, err := b.resolve(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	data, err := os.ReadFile(resolved) // #nosec G304 -- reading files named in the Seirofile is the purpose of file()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	return starlark.String(data), nil
}

func (b *builder) resolve(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	if filepath.IsAbs(p) {
		return p, nil
	}
	return filepath.Join(b.dir, p), nil
}

func each(it starlark.Iterable, fn func(starlark.Value) error) error {
	iter := it.Iterate()
	defer iter.Done()
	var v starlark.Value
	for iter.Next(&v) {
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}
