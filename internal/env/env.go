// Package env derives environment identities and the on-disk layout of their files.
package env

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Dirs are the base directories seiro stores files under.
type Dirs struct {
	State   string // $XDG_STATE_HOME/seiro
	Runtime string // $XDG_RUNTIME_DIR/seiro
	Cache   string // $XDG_CACHE_HOME/seiro
}

// DefaultDirs resolves the base directories from the XDG environment variables.
func DefaultDirs() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	xdg := func(name, fallback string) string {
		if v := os.Getenv(name); v != "" && filepath.IsAbs(v) {
			return v
		}
		return fallback
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" || !filepath.IsAbs(runtime) {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("seiro-%d", os.Getuid()))
	}
	return Dirs{
		State:   filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "seiro"),
		Runtime: filepath.Join(runtime, "seiro"),
		Cache:   filepath.Join(xdg("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "seiro"),
	}, nil
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ID returns the environment ID for a Seirofile: the directory name followed by
// the first 8 hex digits of the SHA-256 of the Seirofile's absolute path.
func ID(seirofile string) (string, error) {
	abs, err := filepath.Abs(seirofile)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	name := unsafeChars.ReplaceAllString(filepath.Base(filepath.Dir(abs)), "_")
	name = strings.Trim(name, "._-")
	if len(name) > 32 {
		name = name[:32]
	}
	if name == "" {
		name = "env"
	}
	return name + "-" + hex.EncodeToString(sum[:])[:8], nil
}

// Env is an environment rooted at a Seirofile.
type Env struct {
	ID        string
	Seirofile string
	Dirs      Dirs
}

// New returns the environment for the given Seirofile.
func New(dirs Dirs, seirofile string) (*Env, error) {
	abs, err := filepath.Abs(seirofile)
	if err != nil {
		return nil, err
	}
	id, err := ID(abs)
	if err != nil {
		return nil, err
	}
	return &Env{ID: id, Seirofile: abs, Dirs: dirs}, nil
}

// StateDir is where persistent state of the environment lives.
func (e *Env) StateDir() string { return filepath.Join(e.Dirs.State, "envs", e.ID) }

// RuntimeDir is where sockets of the environment live.
func (e *Env) RuntimeDir() string { return filepath.Join(e.Dirs.Runtime, e.ID) }

// Machine returns the file layout of a machine in the environment.
func (e *Env) Machine(name string) *MachinePaths {
	return &MachinePaths{
		State:   filepath.Join(e.StateDir(), "machines", name),
		Runtime: filepath.Join(e.RuntimeDir(), name),
	}
}

// MachineNames lists machines that have state in the environment.
func (e *Env) MachineNames() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(e.StateDir(), "machines"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range entries {
		if ent.IsDir() {
			names = append(names, ent.Name())
		}
	}
	return names, nil
}

// Meta is the content of env.json.
type Meta struct {
	Seirofile string    `json:"seirofile"`
	CreatedAt time.Time `json:"created_at"`
}

// Init creates the state directory and env.json if they do not exist yet.
func (e *Env) Init() error {
	if err := os.MkdirAll(e.StateDir(), 0o750); err != nil {
		return err
	}
	p := filepath.Join(e.StateDir(), "env.json")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return WriteJSON(p, Meta{Seirofile: e.Seirofile, CreatedAt: time.Now().UTC()})
}

// Remove deletes the state and runtime directories of the environment.
func (e *Env) Remove() error {
	return errors.Join(os.RemoveAll(e.StateDir()), os.RemoveAll(e.RuntimeDir()))
}

// List returns all environments that have state under dirs.
func List(dirs Dirs) ([]*Env, error) {
	entries, err := os.ReadDir(filepath.Join(dirs.State, "envs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envs []*Env
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		var meta Meta
		if err := ReadJSON(filepath.Join(dirs.State, "envs", ent.Name(), "env.json"), &meta); err != nil {
			return nil, fmt.Errorf("environment %s: %w", ent.Name(), err)
		}
		envs = append(envs, &Env{ID: ent.Name(), Seirofile: meta.Seirofile, Dirs: dirs})
	}
	return envs, nil
}

// MachinePaths is the file layout of a machine.
type MachinePaths struct {
	State   string
	Runtime string
}

// Disk is the path of the i-th declared disk with the given extension.
func (p *MachinePaths) Disk(i int, ext string) string {
	return filepath.Join(p.State, fmt.Sprintf("disk%d.%s", i, ext))
}

// Seed is the cloud-init seed image.
func (p *MachinePaths) Seed() string { return filepath.Join(p.State, "seed.iso") }

// Plan is the derived execution plan written by up and read by the supervisor.
func (p *MachinePaths) Plan() string { return filepath.Join(p.State, "plan.json") }

// Status is the runtime status written by the supervisor.
func (p *MachinePaths) Status() string { return filepath.Join(p.State, "state.json") }

// SerialLog is the captured serial output.
func (p *MachinePaths) SerialLog() string { return filepath.Join(p.State, "serial.log") }

// NVRAM is the UEFI variable store of a machine run by QEMU.
func (p *MachinePaths) NVRAM() string { return filepath.Join(p.State, "nvram.fd") }

// VMMLog is the output of the virtual machine monitor.
func (p *MachinePaths) VMMLog() string { return filepath.Join(p.State, "vmm.log") }

// PasstLog is the passt output for a NIC.
func (p *MachinePaths) PasstLog(nic string) string {
	return filepath.Join(p.State, "passt-"+nic+".log")
}

// HostKey is the SSH host key given to the guest.
func (p *MachinePaths) HostKey() string { return filepath.Join(p.State, "ssh_host_ed25519_key") }

// KnownHosts is the known_hosts file that trusts the guest's host key.
func (p *MachinePaths) KnownHosts() string { return filepath.Join(p.State, "known_hosts") }

// SuperviseLog is the supervisor's own log.
func (p *MachinePaths) SuperviseLog() string { return filepath.Join(p.State, "supervise.log") }

// VSockDeviceLog is the output of the vhost-user vsock device.
func (p *MachinePaths) VSockDeviceLog() string { return filepath.Join(p.State, "vsock-device.log") }

// APISocket is the control socket of the virtual machine monitor: the
// cloud-hypervisor API or QEMU's QMP.
func (p *MachinePaths) APISocket() string { return filepath.Join(p.Runtime, "api.sock") }

// SerialSocket is the serial socket of the virtual machine monitor.
func (p *MachinePaths) SerialSocket() string { return filepath.Join(p.Runtime, "serial.sock") }

// ConsoleSocket is the console socket served by the supervisor.
func (p *MachinePaths) ConsoleSocket() string { return filepath.Join(p.Runtime, "console.sock") }

// VSockSocket is the hybrid vsock socket, served by cloud-hypervisor or by
// the vhost-user vsock device.
func (p *MachinePaths) VSockSocket() string { return filepath.Join(p.Runtime, "vsock.sock") }

// VSockDeviceSocket is the vhost-user socket of the vsock device.
func (p *MachinePaths) VSockDeviceSocket() string {
	return filepath.Join(p.Runtime, "vsock-device.sock")
}

// PasstSocket is the vhost-user socket of a NIC.
func (p *MachinePaths) PasstSocket(nic string) string {
	return filepath.Join(p.Runtime, "passt-"+nic+".sock")
}

// WriteJSON atomically writes v as indented JSON to path.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close() //nolint:errcheck,gosec // already failing
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadJSON reads JSON from path into v.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- paths are derived from the environment layout
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
