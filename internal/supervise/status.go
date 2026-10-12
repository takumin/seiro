// Package supervise implements the per-machine supervisor process. It runs the
// passt backends and cloud-hypervisor as children, relays the serial console to
// a log file and to console clients, and cleans up when the VM exits.
package supervise

import (
	"errors"
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
