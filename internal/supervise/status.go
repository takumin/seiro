// Package supervise implements the per-machine supervisor process. It runs the
// backends (passt and the vsock device) and the VMM as children, relays the
// serial console to a log file and to console clients, and cleans up when the
// VM exits.
package supervise

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/takumin/seiro/internal/env"
)

// Machine states recorded in state.json.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopped  = "stopped"
)

// VSockBackend is the key of the vsock device in Status.BackendPIDs, which
// are otherwise keyed by NIC name. It is longer than any NIC name.
const VSockBackend = "vhost-device-vsock"

// Status is the content of state.json. Only the supervisor writes it.
type Status struct {
	State         string         `json:"state"`
	SupervisorPID int            `json:"supervisor_pid,omitempty"`
	VMMPID        int            `json:"vmm_pid,omitempty"`
	BackendPIDs   map[string]int `json:"backend_pids,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	StoppedAt     *time.Time     `json:"stopped_at,omitempty"`
	ExitCode      *int           `json:"exit_code,omitempty"`
	ExitSignal    string         `json:"exit_signal,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// exitAttrs describes how the VMM exited, for the log: its exit code, the
// signal that killed it, or the error waiting for it.
func (s *Status) exitAttrs() []any {
	var attrs []any
	if s.ExitCode != nil {
		attrs = append(attrs, slog.Int("exit_code", *s.ExitCode))
	}
	if s.ExitSignal != "" {
		attrs = append(attrs, slog.String("signal", s.ExitSignal))
	}
	if s.Error != "" {
		attrs = append(attrs, slog.String("error", s.Error))
	}
	return attrs
}

// ReadStatus reads state.json. It returns nil and no error when the file does not exist.
func ReadStatus(path string) (*Status, error) {
	var s Status
	if err := env.ReadJSON(path, &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}
