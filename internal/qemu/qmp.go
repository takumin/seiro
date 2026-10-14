package qemu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// QMP talks to a QEMU QMP socket. Each call uses its own connection, so that
// a client never holds the socket, which serves one connection at a time.
type QMP struct {
	path    string
	timeout time.Duration
}

// NewQMP returns a client for the QMP socket at path.
func NewQMP(path string) *QMP {
	return &QMP{path: path, timeout: 10 * time.Second}
}

type qmpResponse struct {
	QMP    json.RawMessage `json:"QMP"`
	Event  string          `json:"event"`
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
}

// Execute runs a QMP command and decodes its return value into out, unless out is nil.
func (q *QMP) Execute(ctx context.Context, command string, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", q.path)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // one connection per command
	deadline := time.Now().Add(q.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	dec := json.NewDecoder(bufio.NewReader(conn))
	var greeting qmpResponse
	if err := dec.Decode(&greeting); err != nil {
		return fmt.Errorf("qmp: greeting: %w", err)
	}
	if greeting.QMP == nil {
		return errors.New("qmp: unexpected greeting")
	}
	for _, cmd := range []string{"qmp_capabilities", command} {
		if err := json.NewEncoder(conn).Encode(map[string]string{"execute": cmd}); err != nil {
			return err
		}
		// Events may arrive before the reply.
		var resp qmpResponse
		for {
			// Decode into a fresh value: fields absent from a reply keep
			// what an earlier event set.
			resp = qmpResponse{}
			if err := dec.Decode(&resp); err != nil {
				return fmt.Errorf("qmp: %s: %w", cmd, err)
			}
			if resp.Event == "" {
				break
			}
		}
		if resp.Error != nil {
			return fmt.Errorf("qmp: %s: %s: %s", cmd, resp.Error.Class, resp.Error.Desc)
		}
		if cmd == command && out != nil {
			return json.Unmarshal(resp.Return, out)
		}
	}
	return nil
}

// Status is the subset of query-status used by seiro.
type Status struct {
	Running bool   `json:"running"`
	Status  string `json:"status"`
}

// Status returns the run state of the VM.
func (q *QMP) Status(ctx context.Context) (*Status, error) {
	var st Status
	if err := q.Execute(ctx, "query-status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// PowerDown triggers an ACPI power button event in the guest.
func (q *QMP) PowerDown(ctx context.Context) error {
	return q.Execute(ctx, "system_powerdown", nil)
}

// Quit stops QEMU.
func (q *QMP) Quit(ctx context.Context) error {
	err := q.Execute(ctx, "quit", nil)
	if errors.Is(err, io.EOF) {
		// QEMU exited before replying.
		return nil
	}
	return err
}
