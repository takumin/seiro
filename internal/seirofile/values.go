package seirofile

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

// The values below are opaque handles returned by the builtins. They are only
// meaningful as arguments to other builtins.

type imageValue struct{ img *spec.Image }

func (v *imageValue) String() string {
	if v.img.Path != "" {
		return fmt.Sprintf("image(path = %q)", v.img.Path)
	}
	return fmt.Sprintf("image(url = %q)", v.img.URL)
}
func (v *imageValue) Type() string          { return "image" }
func (v *imageValue) Freeze()               {}
func (v *imageValue) Truth() starlark.Bool  { return starlark.True }
func (v *imageValue) Hash() (uint32, error) { return starlark.String(v.img.Key()).Hash() }

type liveValue struct{ live *spec.Live }

func (v liveValue) String() string {
	return fmt.Sprintf("live(kernel = %q, ...)", v.live.Kernel.Source())
}
func (v liveValue) Type() string          { return "live" }
func (v liveValue) Freeze()               {}
func (v liveValue) Truth() starlark.Bool  { return starlark.True }
func (v liveValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: live") }

type diskValue struct{ disk spec.Disk }

func (v diskValue) String() string {
	if v.disk.Image == nil {
		return fmt.Sprintf("disk(size = %q)", size.Format(v.disk.Size))
	}
	return fmt.Sprintf("disk(image = %q, size = %q)", v.disk.Image.Source(), size.Format(v.disk.Size))
}
func (v diskValue) Type() string          { return "disk" }
func (v diskValue) Freeze()               {}
func (v diskValue) Truth() starlark.Bool  { return starlark.True }
func (v diskValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: disk") }

type nicValue struct{ nic spec.NIC }

func (v nicValue) String() string {
	return fmt.Sprintf("nic(%q, network = %s)", v.nic.Name, v.nic.Network)
}
func (v nicValue) Type() string          { return "nic" }
func (v nicValue) Freeze()               {}
func (v nicValue) Truth() starlark.Bool  { return starlark.True }
func (v nicValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: nic") }

type portValue struct{ port spec.Port }

func (v portValue) String() string {
	return fmt.Sprintf("port(host = %d, guest = %d, protocol = %q)", v.port.Host, v.port.Guest, v.port.Protocol)
}
func (v portValue) Type() string          { return "port" }
func (v portValue) Freeze()               {}
func (v portValue) Truth() starlark.Bool  { return starlark.True }
func (v portValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: port") }

type cloudInitValue struct{ ci *spec.CloudInit }

func (v cloudInitValue) String() string        { return "cloud_init(...)" }
func (v cloudInitValue) Type() string          { return "cloud_init" }
func (v cloudInitValue) Freeze()               {}
func (v cloudInitValue) Truth() starlark.Bool  { return starlark.True }
func (v cloudInitValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: cloud_init") }

type tweaksValue struct{ tweaks spec.Tweaks }

func (v tweaksValue) String() string {
	return fmt.Sprintf("tweaks(early_nic_names = %s, skip_network_wait = %s)",
		starlark.Bool(v.tweaks.EarlyNICNames), starlark.Bool(v.tweaks.SkipNetworkWait))
}
func (v tweaksValue) Type() string          { return "tweaks" }
func (v tweaksValue) Freeze()               {}
func (v tweaksValue) Truth() starlark.Bool  { return starlark.True }
func (v tweaksValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: tweaks") }

type networkValue struct{ name string }

func (v networkValue) String() string        { return v.name }
func (v networkValue) Type() string          { return "network" }
func (v networkValue) Freeze()               {}
func (v networkValue) Truth() starlark.Bool  { return starlark.True }
func (v networkValue) Hash() (uint32, error) { return starlark.String(v.name).Hash() }
