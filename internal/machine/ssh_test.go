package machine

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/supervise"
)

func writePlan(t *testing.T, m *Manager, name string, ssh *plan.SSH) {
	t.Helper()
	paths := m.Env.Machine(name)
	if err := os.MkdirAll(paths.State, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := env.WriteJSON(paths.Plan(), plan.Machine{Name: name, VSockSocket: paths.VSockSocket(), SSH: ssh}); err != nil {
		t.Fatal(err)
	}
}

func TestSSHConfig(t *testing.T) {
	t.Setenv("USER", "local")

	m := testManager(t)
	m.Self = "/opt/my seiro/seiro"
	writePlan(t, m, "a", &plan.SSH{User: "dev", Port: 10022, KnownHosts: m.Env.Machine("a").KnownHosts()})
	writePlan(t, m, "b", &plan.SSH{Port: 10022, KnownHosts: "/k/50%/known_hosts"})
	writePlan(t, m, "nossh", nil)

	got, err := m.SSHConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	wantA := "Host a\n  User dev\n  HostName a." + m.Env.ID + ".seiro\n  ProxyCommand '/opt/my seiro/seiro' ssh-proxy " + m.Env.Machine("a").VSockSocket() + " 10022\n" +
		"  HostKeyAlias a\n  UserKnownHostsFile " + m.Env.Machine("a").KnownHosts() + "\n"
	if !strings.HasPrefix(got, wantA) {
		t.Errorf("ssh-config:\n%s\nwant prefix:\n%s", got, wantA)
	}
	if !strings.Contains(got, "\nHost b\n  User local\n") || !strings.Contains(got, "UserKnownHostsFile /k/50%%/known_hosts\n") {
		t.Errorf("ssh-config for b:\n%s", got)
	}
	if strings.Contains(got, "nossh") {
		t.Errorf("machine without SSH listed:\n%s", got)
	}

	if _, err := m.SSHConfig([]string{"nossh"}); err == nil || !strings.Contains(err.Error(), "no cloud_init") {
		t.Errorf("expected no cloud_init error, got %v", err)
	}
	if _, err := m.SSHConfig([]string{"missing"}); err == nil || !strings.Contains(err.Error(), "has not been created") {
		t.Errorf("expected not created error, got %v", err)
	}
}

func TestSSHArgs(t *testing.T) {
	t.Parallel()

	m := testManager(t)
	m.Self = "/bin/seiro"
	writePlan(t, m, "a", &plan.SSH{User: "dev", Port: 10022, KnownHosts: "/k"})
	if _, err := m.SSHArgs("a", "", nil); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected not running error, got %v", err)
	}

	// A process whose command line contains "supervise" passes for a supervisor.
	fake := exec.Command("sh", "-c", "sleep 30", "supervise")
	if err := fake.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fake.Process.Kill() //nolint:errcheck,gosec // test
		fake.Wait()         //nolint:errcheck,gosec // test
	})
	if err := env.WriteJSON(m.Env.Machine("a").Status(), supervise.Status{State: supervise.StateRunning, SupervisorPID: fake.Process.Pid}); err != nil {
		t.Fatal(err)
	}
	args, err := m.SSHArgs("a", "", []string{"uptime"})
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "ssh" || args[len(args)-2] != "dev@a" || args[len(args)-1] != "uptime" {
		t.Errorf("args = %q", args)
	}
	args, err = m.SSHArgs("a", "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if args[len(args)-1] != "root@a" {
		t.Errorf("args = %q", args)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/usr/bin/seiro": "/usr/bin/seiro",
		"/a b/seiro":     "'/a b/seiro'",
		"/it's":          `'/it'\''s'`,
		"":               "''",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
