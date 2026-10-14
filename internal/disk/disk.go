// Package disk inspects images and creates the disks attached to machines.
package disk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dypflying/go-qcow2lib/qcow2"
)

// Image formats understood by seiro and the VMMs.
const (
	FormatRaw   = "raw"
	FormatQCOW2 = "qcow2"
)

// Filesystems detected in raw images.
const FilesystemSquashfs = "squashfs"

var (
	qcow2Magic    = []byte{'Q', 'F', 'I', 0xfb}
	squashfsMagic = []byte{'h', 's', 'q', 's'}
)

// Info describes an existing image file.
type Info struct {
	Format      string `json:"format"`
	VirtualSize uint64 `json:"virtual_size"`
	// Filesystem is set when a raw image is a filesystem seiro recognizes.
	Filesystem string `json:"filesystem,omitempty"`
}

// Inspect detects the format and virtual size of an image file.
func Inspect(path string) (*Info, error) {
	f, err := os.Open(path) // #nosec G304 -- image paths come from the cache
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	header := make([]byte, 32)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n >= 32 && bytes.Equal(header[:4], qcow2Magic) {
		// QCOW2 header: version at offset 4, virtual size at offset 24 (big endian).
		return &Info{Format: FormatQCOW2, VirtualSize: binary.BigEndian.Uint64(header[24:32])}, nil
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	info := &Info{Format: FormatRaw, VirtualSize: uint64(fi.Size())} // #nosec G115 -- file sizes are non-negative
	if n >= 4 && bytes.Equal(header[:4], squashfsMagic) {
		info.Filesystem = FilesystemSquashfs
	}
	return info, nil
}

// CreateOverlay creates a qcow2 image of the given virtual size that uses
// backing as its backing file. backing must be an absolute path.
//
// It is a variable so that the qcow2 implementation can be replaced.
var CreateOverlay = func(path, backing string, size uint64) error {
	if err := qcow2.Blk_Create(path, map[string]any{
		qcow2.OPT_FMT:     FormatQCOW2,
		qcow2.OPT_SIZE:    size,
		qcow2.OPT_BACKING: backing,
	}); err != nil {
		return fmt.Errorf("create overlay %s: %w", path, err)
	}
	// go-qcow2lib creates files with mode 0755.
	return os.Chmod(path, 0o600)
}

// CreateBlank creates a sparse raw image of the given size.
func CreateBlank(path string, size uint64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path inside the machine state directory
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(size)); err != nil { // #nosec G115 -- sizes are validated to fit
		return errors.Join(err, f.Close(), os.Remove(path))
	}
	return f.Close()
}
