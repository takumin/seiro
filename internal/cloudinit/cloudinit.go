// Package cloudinit builds NoCloud seed images for cloud-init.
package cloudinit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// VolumeLabel is the label cloud-init's NoCloud datasource looks for.
const VolumeLabel = "cidata"

// ISO9660 requires 2048-byte blocks.
const blockSize = 2048

// Seed is the content of a NoCloud seed.
type Seed struct {
	UserData      string `json:"user_data"`
	MetaData      string `json:"meta_data"`
	NetworkConfig string `json:"network_config,omitempty"`
}

func (s *Seed) files() map[string]string {
	files := map[string]string{
		"/user-data": s.UserData,
		"/meta-data": s.MetaData,
	}
	if s.NetworkConfig != "" {
		files["/network-config"] = s.NetworkConfig
	}
	return files
}

// Build writes the seed as an ISO9660 image to path, replacing any existing file.
func Build(path string, seed *Seed) error {
	files := seed.files()
	var total int64
	for _, body := range files {
		total += int64(len(body))
	}
	// Leave room for the volume descriptors, directory records and Rock Ridge entries.
	imageSize := roundUp(total+1<<20, blockSize)

	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	defer os.Remove(tmp) //nolint:errcheck // no-op after a successful rename

	d, err := diskfs.Create(tmp, imageSize, diskfs.SectorSize(blockSize))
	if err != nil {
		return fmt.Errorf("create seed image: %w", err)
	}
	closed := false
	defer func() {
		// Disk.Close zeroes the Disk, so it must not be called twice.
		if !closed {
			d.Close() //nolint:errcheck,gosec // already failing
		}
	}()

	fs, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeISO9660, VolumeLabel: VolumeLabel})
	if err != nil {
		return fmt.Errorf("create seed filesystem: %w", err)
	}
	for name, body := range files {
		f, err := fs.OpenFile(name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("unexpected filesystem type %T", fs)
	}
	if err := iso.Finalize(iso9660.FinalizeOptions{RockRidge: true, VolumeIdentifier: VolumeLabel}); err != nil {
		return fmt.Errorf("finalize seed image: %w", err)
	}
	closed = true
	if err := d.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func roundUp(n, m int64) int64 {
	return (n + m - 1) / m * m
}
