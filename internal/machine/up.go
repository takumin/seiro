package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/takumin/seiro/internal/cloudinit"
	"github.com/takumin/seiro/internal/disk"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/hostkey"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/qemu"
	"github.com/takumin/seiro/internal/spec"
	"github.com/takumin/seiro/internal/supervise"
)

// Up creates (if needed) and starts the named machines, or all declared machines.
func (m *Manager) Up(ctx context.Context, names []string) error {
	var declared []string
	for _, mc := range m.Spec.Machines {
		declared = append(declared, mc.Name)
	}
	names, err := selectNames(names, declared)
	if err != nil {
		return err
	}
	machines := make([]*spec.Machine, 0, len(names))
	for _, n := range names {
		machines = append(machines, m.Spec.Machine(n))
	}

	res, err := m.resources(ctx, machines)
	if err != nil {
		return err
	}
	if err := m.Env.Init(); err != nil {
		return err
	}

	var errs []error
	for _, mc := range machines {
		if err := m.up(ctx, mc, res); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", mc.Name, err))
		}
	}
	return errors.Join(errs...)
}

// resources checks the host and prepares the images and firmware the machines need.
func (m *Manager) resources(ctx context.Context, machines []*spec.Machine) (plan.Resources, error) {
	res := plan.Resources{Images: map[string]plan.Image{}}
	var err error
	for _, mc := range machines {
		switch mc.VMMName() {
		case spec.VMMQEMU:
			if res.QEMU == "" {
				if res.QEMU, err = qemuPath(); err != nil {
					return res, err
				}
				if res.VSockDevice, err = lookPath("vhost-device-vsock"); err != nil {
					return res, err
				}
			}
			if mc.Live == nil && res.QEMUFirmware == nil {
				if res.QEMUFirmware, err = qemu.FindFirmware(qemu.FirmwareDirs(), "x86_64", "pc-q35"); err != nil {
					return res, err
				}
			}
		default:
			if res.CloudHypervisor == "" {
				if res.CloudHypervisor, err = lookPath("cloud-hypervisor"); err != nil {
					return res, err
				}
			}
			if mc.Live == nil && res.Firmware == "" {
				if res.Firmware, err = m.Cache.Firmware(ctx); err != nil {
					return res, err
				}
			}
		}
		if len(mc.NICs) > 0 && res.Passt == "" {
			if res.Passt, err = lookPath("passt"); err != nil {
				return res, err
			}
		}
	}
	if err := checkKVM(); err != nil {
		return res, err
	}

	sub := &spec.Environment{Machines: machines}
	for _, img := range sub.Images() {
		var p string
		if img.Path != "" {
			p, err = localImage(img)
		} else {
			p, err = m.Cache.Fetch(ctx, "images", img.CacheKey(), img.URL, img.SHA256)
		}
		if err != nil {
			return res, err
		}
		info, err := disk.Inspect(p)
		if err != nil {
			return res, err
		}
		res.Images[img.Key()] = plan.Image{Path: p, Info: *info}
	}
	return res, nil
}

// qemuPath finds the QEMU system emulator for the host architecture, which
// seiro runs with KVM. Only x86_64 hosts are supported for now.
func qemuPath() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("vmm %q is not supported on %s yet", spec.VMMQEMU, runtime.GOARCH)
	}
	p, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		return "", errors.New("qemu-system-x86_64 not found in PATH (install QEMU, e.g. the qemu-system-x86 package)")
	}
	return p, nil
}

// localImage checks a local image, which is used in place rather than copied
// into the cache, against its declared checksum.
func localImage(img *spec.Image) (string, error) {
	f, err := os.Open(img.Path) // #nosec G304 -- local images are named in the Seirofile
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	if img.SHA256 == "" {
		return img.Path, nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("%s: %w", img.Path, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != img.SHA256 {
		return "", fmt.Errorf("%s: sha256 mismatch: got %s, want %s", img.Path, got, img.SHA256)
	}
	return img.Path, nil
}

func checkKVM() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("cannot open /dev/kvm (is your user in the kvm group?): %w", err)
	}
	return f.Close()
}

func (m *Manager) up(ctx context.Context, mc *spec.Machine, res plan.Resources) error {
	st, err := m.status(mc.Name)
	if err != nil {
		return err
	}
	if st.State == StateRunning {
		fmt.Fprintf(m.Out, "%s: already running\n", mc.Name) //nolint:errcheck // progress output
		return nil
	}

	paths := m.Env.Machine(mc.Name)
	created := st.State == StateNotCreated
	if err := os.MkdirAll(paths.State, 0o750); err != nil {
		return err
	}
	if mc.CloudInit != nil {
		if res.HostKey, err = hostkey.Ensure(paths.HostKey()); err != nil {
			return err
		}
	}

	p, err := plan.Build(m.Env, mc, res)
	if err != nil {
		return err
	}

	if !created {
		var prev plan.Machine
		if err := env.ReadJSON(paths.Plan(), &prev); err == nil {
			if !reflect.DeepEqual(prev.Disks, p.Disks) {
				return errors.New("disk definition changed; run `seiro destroy` and `seiro up` to apply it")
			}
			if !reflect.DeepEqual(prev.Spec.CloudInit, mc.CloudInit) {
				fmt.Fprintf(m.Out, "%s: cloud_init changed; it only applies to newly created machines\n", mc.Name) //nolint:errcheck // progress output
				p.Seed = prev.Seed
				p.SSH = prev.SSH
				p.VMM.Args = plan.VMMArgs(p, mc, res)
			}
		}
	}

	if err := createDisks(p); err != nil {
		return err
	}
	if p.SSH != nil && res.HostKey != nil {
		if err := os.WriteFile(p.SSH.KnownHosts, []byte(res.HostKey.KnownHosts(mc.Name)), 0o600); err != nil {
			return err
		}
	}
	if err := env.WriteJSON(paths.Plan(), p); err != nil {
		return err
	}
	if created {
		fmt.Fprintf(m.Out, "%s: created\n", mc.Name) //nolint:errcheck // progress output
	}

	// A stale state.json from a previous run must not be mistaken for this one.
	if err := os.Remove(paths.Status()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := m.spawnSupervisor(paths); err != nil {
		return err
	}
	if err := m.waitRunning(ctx, paths, p); err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "%s: running%s\n", mc.Name, describePorts(mc.Ports)) //nolint:errcheck // progress output
	return nil
}

// createDisks creates the disks, seed and UEFI variable store that do not exist yet.
func createDisks(p *plan.Machine) error {
	for _, d := range p.Disks {
		if _, err := os.Stat(d.Path); err == nil {
			continue
		}
		var err error
		if d.Backing != "" {
			err = disk.CreateOverlay(d.Path, d.Backing, d.Size)
		} else {
			err = disk.CreateBlank(d.Path, d.Size)
		}
		if err != nil {
			return err
		}
	}
	if p.UEFI != nil {
		if _, err := os.Stat(p.UEFI.Vars); errors.Is(err, os.ErrNotExist) {
			if err := copyFile(p.UEFI.Vars, p.UEFI.VarsTemplate); err != nil {
				return err
			}
		}
	}
	if p.Seed != nil {
		if _, err := os.Stat(p.Seed.Path); errors.Is(err, os.ErrNotExist) {
			if err := cloudinit.Build(p.Seed.Path, &p.Seed.Data); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile copies src to a new file dst, which only the user can access.
func copyFile(dst, src string) error {
	in, err := os.Open(src) // #nosec G304 -- the firmware named by a QEMU firmware descriptor
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path inside the machine state directory
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()    //nolint:errcheck,gosec // already failing
		os.Remove(dst) //nolint:errcheck,gosec // best effort
		return err
	}
	return out.Close()
}

func (m *Manager) spawnSupervisor(paths *env.MachinePaths) error {
	log, err := os.OpenFile(paths.SuperviseLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- path inside the machine state directory
	if err != nil {
		return err
	}
	defer log.Close()                                      //nolint:errcheck // the child keeps its own descriptor
	cmd := exec.Command(m.Self, "supervise", paths.Plan()) // #nosec G204 -- re-executes seiro itself
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start supervisor: %w", err)
	}
	return cmd.Process.Release()
}

func (m *Manager) waitRunning(ctx context.Context, paths *env.MachinePaths, p *plan.Machine) error {
	deadline := time.Now().Add(m.StartTimeout)
	for time.Now().Before(deadline) {
		rt, err := supervise.ReadStatus(paths.Status())
		if err != nil {
			return err
		}
		if rt != nil && rt.State == supervise.StateStopped {
			return fmt.Errorf("failed to start: %s (see %s)", rt.Error, paths.SuperviseLog())
		}
		if rt != nil && rt.State == supervise.StateRunning {
			if ok, err := controlOf(p).Running(ctx); err == nil && ok {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("did not start within %s (see %s)", m.StartTimeout, paths.SuperviseLog())
}

func describePorts(ports []spec.Port) string {
	if len(ports) == 0 {
		return ""
	}
	var parts []string
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("localhost:%d->%d/%s", p.Host, p.Guest, p.Protocol))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
