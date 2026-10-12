package spec

import (
	"errors"
	"fmt"
	"net"
	"regexp"

	"github.com/takumin/seiro/internal/size"
)

// Limits applied by Validate.
const (
	MaxCPUs        = 256
	MinMemory      = 128 * size.MiB
	MaxNICNameLen  = 15 // IFNAMSIZ - 1
	MaxMachineName = 63
)

var (
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	nicNameRe  = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	sha256Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate checks the environment and returns all problems found, joined.
func (e *Environment) Validate() error {
	var errs []error
	names := map[string]bool{}
	hostPorts := map[string]string{}

	for _, m := range e.Machines {
		if names[m.Name] {
			errs = append(errs, fmt.Errorf("machine %q: defined more than once", m.Name))
		}
		names[m.Name] = true
		errs = append(errs, m.validate()...)

		for _, p := range m.Ports {
			key := fmt.Sprintf("%s/%d", p.Protocol, p.Host)
			if other, ok := hostPorts[key]; ok {
				errs = append(errs, fmt.Errorf("machine %q: host port %s is already published by machine %q", m.Name, key, other))
				continue
			}
			hostPorts[key] = m.Name
		}
	}
	return errors.Join(errs...)
}

func (m *Machine) validate() []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("machine %q: "+format, append([]any{m.Name}, args...)...))
	}

	if len(m.Name) > MaxMachineName || !hostnameRe.MatchString(m.Name) {
		fail("name must be a valid hostname (lowercase letters, digits and '-', at most %d characters)", MaxMachineName)
	}
	if m.CPUs < 1 || m.CPUs > MaxCPUs {
		fail("cpus must be between 1 and %d", MaxCPUs)
	}
	if m.Memory < MinMemory {
		fail("memory must be at least %s", size.Format(MinMemory))
	}
	if m.Memory%size.MiB != 0 {
		fail("memory must be a multiple of 1MiB")
	}

	if len(m.Disks) == 0 {
		fail("at least one disk is required")
	}
	for i, d := range m.Disks {
		if d.Image != nil && d.Image.SHA256 != "" && !sha256Re.MatchString(d.Image.SHA256) {
			fail("disk %d: sha256 must be 64 lowercase hexadecimal characters", i)
		}
	}

	nicNames := map[string]bool{}
	for _, n := range m.NICs {
		if nicNames[n.Name] {
			fail("nic %q: defined more than once", n.Name)
		}
		nicNames[n.Name] = true
		if len(n.Name) > MaxNICNameLen || !nicNameRe.MatchString(n.Name) {
			fail("nic %q: name must be at most %d characters of letters, digits, '_', '.' and '-'", n.Name, MaxNICNameLen)
		}
		if n.Network != NetworkInternet {
			fail("nic %q: unsupported network %q", n.Name, n.Network)
		}
		if n.MAC != "" {
			if err := validateMAC(n.MAC); err != nil {
				fail("nic %q: %v", n.Name, err)
			}
		}
	}

	if len(m.Ports) > 0 {
		switch len(m.InternetNICs()) {
		case 0:
			fail("ports require a nic attached to internet")
		case 1:
		default:
			fail("ports cannot be published on a machine with more than one nic attached to internet")
		}
	}
	for _, p := range m.Ports {
		if p.Host == 0 || p.Guest == 0 {
			fail("port %d:%d: ports must be between 1 and 65535", p.Host, p.Guest)
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			fail("port %d:%d: protocol must be \"tcp\" or \"udp\"", p.Host, p.Guest)
		}
	}
	return errs
}

func validateMAC(s string) error {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("invalid mac address %q", s)
	}
	if hw[0]&1 != 0 {
		return fmt.Errorf("mac address %q must be unicast", s)
	}
	return nil
}
