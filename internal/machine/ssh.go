package machine

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
)

// sshOption is an ssh_config option.
type sshOption struct{ key, value string }

// sshTarget is how to reach the sshd of a machine over vsock.
type sshTarget struct {
	name    string
	user    string
	options []sshOption
}

// sshTarget reads the plan of a created machine and derives how to reach it.
func (m *Manager) sshTarget(name string) (*sshTarget, error) {
	paths := m.Env.Machine(name)
	var p plan.Machine
	if err := env.ReadJSON(paths.Plan(), &p); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("machine %q has not been created", name)
	} else if err != nil {
		return nil, err
	}
	if p.SSH == nil {
		return nil, fmt.Errorf("machine %q declares no cloud_init, so SSH over vsock is not set up", name)
	}
	user := p.SSH.User
	if user == "" {
		user = os.Getenv("USER")
	}
	// ssh expands %-tokens in both ProxyCommand and UserKnownHostsFile.
	proxy := shellQuote(m.Self) + " ssh-proxy " + shellQuote(p.VSockSocket) + " " + strconv.FormatUint(uint64(p.SSH.Port), 10)
	return &sshTarget{name: name, user: user, options: []sshOption{
		// A HostName unique to the environment keeps %h-based settings such as
		// ControlPath from mixing this machine up with a real host of the same name.
		{"HostName", name + "." + m.Env.ID + ".seiro"},
		{"ProxyCommand", strings.ReplaceAll(proxy, "%", "%%")},
		{"HostKeyAlias", name},
		{"UserKnownHostsFile", configQuote(strings.ReplaceAll(p.SSH.KnownHosts, "%", "%%"))},
		{"GlobalKnownHostsFile", "/dev/null"},
		{"StrictHostKeyChecking", "yes"},
		{"HostKeyAlgorithms", "ssh-ed25519"},
		{"UpdateHostKeys", "no"},
		{"CheckHostIP", "no"},
	}}, nil
}

// SSHArgs returns the ssh command line that logs in to the machine over vsock.
// user overrides the user derived from cloud_init; extra is appended after the
// destination (options and a remote command).
func (m *Manager) SSHArgs(name, user string, extra []string) ([]string, error) {
	st, err := m.status(name)
	if err != nil {
		return nil, err
	}
	if st.State != StateRunning {
		return nil, fmt.Errorf("machine %q is not running", name)
	}
	t, err := m.sshTarget(name)
	if err != nil {
		return nil, err
	}
	if user == "" {
		user = t.user
	}
	args := []string{"ssh"}
	for _, o := range t.options {
		args = append(args, "-o", o.key+"="+o.value)
	}
	dest := name
	if user != "" {
		dest = user + "@" + name
	}
	return append(append(args, dest), extra...), nil
}

// SSHConfig renders ssh_config Host entries for the named machines, or for
// every created machine that has SSH set up when none are named.
func (m *Manager) SSHConfig(names []string) (string, error) {
	explicit := len(names) > 0
	if !explicit {
		var err error
		if names, err = m.allNames(); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	for _, name := range names {
		t, err := m.sshTarget(name)
		if err != nil {
			if explicit {
				return "", err
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Host %s\n", name)
		if t.user != "" {
			fmt.Fprintf(&b, "  User %s\n", t.user)
		}
		for _, o := range t.options {
			fmt.Fprintf(&b, "  %s %s\n", o.key, o.value)
		}
	}
	return b.String(), nil
}

// shellQuote quotes s for the shell ssh runs ProxyCommand with.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// configQuote quotes s as an ssh_config argument when it contains whitespace.
func configQuote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
