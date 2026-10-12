package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/command"
)

func TestRun(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stdout string
		stderr string
		stdin  string
		args   string
		exit   int
	}{
		"empty":              {"", "", "", "", command.ExitOK},
		"unknown":            {"", "", "", "a unknown", command.ExitNG},
		"log-level-debug":    {"", "", "", "a -l debug", command.ExitOK},
		"log-level-info":     {"", "", "", "a -l info", command.ExitOK},
		"log-level-warn":     {"", "", "", "a -l warn", command.ExitOK},
		"log-level-error":    {"", "", "", "a -l error", command.ExitOK},
		"log-level-unknown":  {"", "", "", "a -l unknown", command.ExitNG},
		"log-format-text":    {"", "", "", "a --log-format text", command.ExitOK},
		"log-format-json":    {"", "", "", "a --log-format json", command.ExitOK},
		"log-format-unknown": {"", "", "", "a --log-format unknown", command.ExitNG},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			stdin := strings.NewReader(tt.stdin)
			args := strings.Split(tt.args, " ")
			exit := command.Main(&stdout, &stderr, stdin, args)

			switch {
			case tt.exit == command.ExitOK && exit == command.ExitNG:
				t.Error("unexpected error:", stdout, stderr)
			case tt.exit == command.ExitNG && exit == command.ExitOK:
				t.Error("unexpected error:", stdout, stderr)
			}
		})
	}
}

func TestEvalAndStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "run"))
	seirofile := filepath.Join(dir, "Seirofile")
	src := `machine(name = "dev", disks = [disk(size = "1GiB")])`
	if err := os.WriteFile(seirofile, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if exit := command.Main(&stdout, &stderr, strings.NewReader(""), []string{"seiro", "--file", seirofile, "eval"}); exit != command.ExitOK {
		t.Fatalf("eval failed: %s", stderr.String())
	}
	var got struct {
		Machines []struct {
			Name   string `json:"name"`
			CPUs   int    `json:"cpus"`
			Memory uint64 `json:"memory"`
		} `json:"machines"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("eval output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Machines) != 1 || got.Machines[0].Name != "dev" || got.Machines[0].CPUs != 1 || got.Machines[0].Memory != 512<<20 {
		t.Errorf("eval output = %+v", got)
	}

	stdout.Reset()
	if exit := command.Main(&stdout, &stderr, strings.NewReader(""), []string{"seiro", "--file", seirofile, "status"}); exit != command.ExitOK {
		t.Fatalf("status failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "dev   not created") {
		t.Errorf("status output = %q", stdout.String())
	}

	stdout.Reset()
	if exit := command.Main(&stdout, &stderr, strings.NewReader(""), []string{"seiro", "--file", filepath.Join(dir, "missing"), "eval"}); exit != command.ExitNG {
		t.Error("eval of a missing Seirofile succeeded")
	}
}

func TestCompletion(t *testing.T) {
	dir := t.TempDir()
	seirofile := filepath.Join(dir, "Seirofile")
	src := `
machine(name = "web", disks = [disk(size = "1GiB")])
machine(name = "db", disks = [disk(size = "1GiB")])
`
	if err := os.WriteFile(seirofile, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		file string
		args string
		want string
	}{
		"up":            {seirofile, "up", "web\ndb\n"},
		"up-given":      {seirofile, "up web", "db\n"},
		"destroy":       {seirofile, "destroy db", "web\n"},
		"console":       {seirofile, "console", "web\ndb\n"},
		"console-given": {seirofile, "console web", ""},
		"logs-flag":     {seirofile, "logs --pas", "--passt:"},
		"missing-file":  {filepath.Join(dir, "missing"), "up", ""},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"seiro", "--file", tt.file}, strings.Fields(tt.args)...)
			args = append(args, "--generate-shell-completion")
			if exit := command.Main(&stdout, &stderr, strings.NewReader(""), args); exit != command.ExitOK {
				t.Fatalf("completion failed: %s", stderr.String())
			}
			got := stdout.String()
			if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
				t.Errorf("completion of %q = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
