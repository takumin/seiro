package machine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/term"
)

// EscapeByte detaches from the console (Ctrl-]).
const EscapeByte = 0x1d

// tailBytes is how much of the serial log is replayed when attaching.
const tailBytes = 4 << 10

// Console attaches stdin/stdout to the machine's serial console until the
// escape byte is typed or the machine stops.
func (m *Manager) Console(ctx context.Context, name string, stdin *os.File, stdout io.Writer) error {
	st, err := m.status(name)
	if err != nil {
		return err
	}
	if st.State != StateRunning {
		return fmt.Errorf("machine %q is not running", name)
	}
	paths := m.Env.Machine(name)
	conn, err := net.Dial("unix", paths.ConsoleSocket())
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // console connection

	if tail, err := readTail(paths.SerialLog(), tailBytes); err == nil {
		stdout.Write(tail) //nolint:errcheck,gosec // best effort replay
	}
	fmt.Fprintf(stdout, "\r\n[connected to %s, press Ctrl-] to detach]\r\n", name) //nolint:errcheck // banner

	if fd := int(stdin.Fd()); term.IsTerminal(fd) { // #nosec G115 -- file descriptors fit in int
		old, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer term.Restore(fd, old) //nolint:errcheck // best effort
	}

	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(stdout, conn)
		done <- err
	}()
	go func() { done <- copyUntilEscape(conn, stdin) }()

	select {
	case err = <-done:
	case <-ctx.Done():
	}
	fmt.Fprint(stdout, "\r\n[detached]\r\n") //nolint:errcheck // banner
	if errors.Is(err, errDetached) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

var errDetached = errors.New("detached")

// copyUntilEscape copies src to dst until EscapeByte is read.
func copyUntilEscape(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if i := bytes.IndexByte(chunk, EscapeByte); i >= 0 {
				if _, werr := dst.Write(chunk[:i]); werr != nil {
					return werr
				}
				return errDetached
			}
			if _, werr := dst.Write(chunk); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- log path inside the machine state directory
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(fi.Size()-n, 0)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// Log sources available to Logs.
const (
	LogSerial    = "serial"
	LogVMM       = "vmm"
	LogSupervise = "supervise"
)

// LogPath returns the log file of a source; for passt pass the NIC name with LogPasst.
func (m *Manager) LogPath(name, source, nic string) (string, error) {
	paths := m.Env.Machine(name)
	if _, err := os.Stat(paths.State); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("machine %q has not been created", name)
	}
	switch {
	case nic != "":
		return paths.PasstLog(nic), nil
	case source == LogVMM:
		return paths.VMMLog(), nil
	case source == LogSupervise:
		return paths.SuperviseLog(), nil
	default:
		return paths.SerialLog(), nil
	}
}

// Logs writes the log at path to w, and keeps following appended data when follow is set.
func Logs(ctx context.Context, path string, follow bool, w io.Writer) error {
	f, err := os.Open(path) // #nosec G304 -- log path inside the machine state directory
	if errors.Is(err, os.ErrNotExist) && follow {
		f, err = waitOpen(ctx, path)
	}
	if err != nil {
		return err
	}
	defer func() { f.Close() }() //nolint:errcheck,gosec // read-only

	var off int64
	for {
		n, err := io.Copy(w, f)
		off += n
		if err != nil {
			return err
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
		// Reopen when the log was rotated or truncated.
		if fi, err := os.Stat(path); err == nil {
			cur, _ := f.Stat()
			if !os.SameFile(fi, cur) || fi.Size() < off {
				f.Close()                               //nolint:errcheck,gosec // replaced below
				if f, err = os.Open(path); err != nil { // #nosec G304 -- see above
					return err
				}
				off = 0
			}
		}
	}
}

func waitOpen(ctx context.Context, path string) (*os.File, error) {
	for {
		f, err := os.Open(path) // #nosec G304 -- log path inside the machine state directory
		if !errors.Is(err, os.ErrNotExist) {
			return f, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
