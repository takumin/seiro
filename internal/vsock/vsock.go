// Package vsock connects to guest vsock ports through cloud-hypervisor's
// hybrid vsock socket, which speaks the Firecracker "CONNECT <port>" protocol.
package vsock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// HandshakeTimeout bounds one CONNECT exchange.
var HandshakeTimeout = 5 * time.Second

// ErrNotRunning is returned when the hybrid vsock socket does not exist.
var ErrNotRunning = errors.New("vsock socket does not exist (is the machine running?)")

// Dial connects to port in the guest through the hybrid vsock socket.
func Dial(ctx context.Context, socket string, port uint32) (*net.UnixConn, error) {
	if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	conn, ok := c.(*net.UnixConn)
	if !ok {
		c.Close() //nolint:errcheck,gosec // unexpected type
		return nil, fmt.Errorf("unexpected connection type %T", c)
	}
	if err := handshake(conn, port); err != nil {
		conn.Close() //nolint:errcheck,gosec // already failing
		return nil, err
	}
	return conn, nil
}

func handshake(conn net.Conn, port uint32) error {
	if err := conn.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return err
	}
	// Read byte by byte: anything after the reply line belongs to the guest.
	var line strings.Builder
	buf := make([]byte, 1)
	for {
		if _, err := conn.Read(buf); err != nil {
			return fmt.Errorf("vsock port %d: no reply (is anything listening in the guest?): %w", port, err)
		}
		if buf[0] == '\n' {
			break
		}
		if line.Len() > 64 {
			return errors.New("vsock: reply too long")
		}
		line.WriteByte(buf[0])
	}
	if !strings.HasPrefix(line.String(), "OK ") {
		return fmt.Errorf("vsock port %d: unexpected reply %q", port, line.String())
	}
	return conn.SetDeadline(time.Time{})
}

// DialWait retries Dial until it succeeds, the socket disappears, or wait elapses.
// It covers the time between the VM starting and the guest listening.
// waiting is called once when the first attempt fails and a retry follows.
func DialWait(ctx context.Context, socket string, port uint32, wait time.Duration, waiting func()) (*net.UnixConn, error) {
	deadline := time.Now().Add(wait)
	for attempt := 0; ; attempt++ {
		conn, err := Dial(ctx, socket, port)
		if err == nil || errors.Is(err, ErrNotRunning) || time.Now().After(deadline) {
			return conn, err
		}
		if attempt == 0 && waiting != nil {
			waiting()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Relay copies stdin to conn and conn to stdout until the guest closes the
// connection. EOF on stdin is forwarded as a half close.
func Relay(conn *net.UnixConn, stdin io.Reader, stdout io.Writer) error {
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, stdin)
		if err == nil {
			err = conn.CloseWrite()
		}
		if err != nil {
			done <- err
		}
	}()
	go func() {
		_, err := io.Copy(stdout, conn)
		done <- err
	}()
	return <-done
}
