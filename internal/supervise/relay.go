package supervise

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// clientBuffer is how many output chunks may queue for a console client
// before it is considered too slow and disconnected.
const clientBuffer = 1024

// Relay fans the serial output out to a log and any number of console clients,
// and forwards input from every client to the serial port.
type Relay struct {
	serial io.ReadWriter
	log    io.Writer

	mu      sync.Mutex
	clients map[net.Conn]chan []byte
	writeMu sync.Mutex
}

// NewRelay returns a relay between serial and log.
func NewRelay(serial io.ReadWriter, log io.Writer) *Relay {
	return &Relay{serial: serial, log: log, clients: map[net.Conn]chan []byte{}}
}

// Run copies serial output until the serial port is closed, then disconnects
// all clients.
func (r *Relay) Run() error {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.serial.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if _, werr := r.log.Write(chunk); werr != nil {
				slog.Warn("failed to write serial log", slog.Any("error", werr))
			}
			r.broadcast(chunk)
		}
		if err != nil {
			r.closeClients()
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

func (r *Relay) broadcast(chunk []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for conn, ch := range r.clients {
		select {
		case ch <- chunk:
		default:
			slog.Warn("disconnecting slow console client")
			r.drop(conn)
		}
	}
}

// drop must be called with r.mu held.
func (r *Relay) drop(conn net.Conn) {
	if ch, ok := r.clients[conn]; ok {
		close(ch)
		delete(r.clients, conn)
	}
}

func (r *Relay) closeClients() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for conn := range r.clients {
		r.drop(conn)
	}
}

// Serve accepts console clients until l is closed.
func (r *Relay) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		r.attach(conn)
	}
}

func (r *Relay) attach(conn net.Conn) {
	ch := make(chan []byte, clientBuffer)
	r.mu.Lock()
	r.clients[conn] = ch
	r.mu.Unlock()

	// Output: deliver queued chunks; the channel is closed when the client is dropped.
	go func() {
		defer conn.Close() //nolint:errcheck // client connection
		for chunk := range ch {
			if _, err := conn.Write(chunk); err != nil {
				break
			}
		}
	}()

	// Input: forward keystrokes to the serial port until the client disconnects.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				r.writeMu.Lock()
				_, werr := r.serial.Write(buf[:n])
				r.writeMu.Unlock()
				if werr != nil {
					err = werr
				}
			}
			if err != nil {
				r.mu.Lock()
				r.drop(conn)
				r.mu.Unlock()
				return
			}
		}
	}()
}
