package cache

import (
	"context"
	"fmt"
)

// Firmware pinned by seiro.
const (
	FirmwareVersion = "ch-97eeb7b09"
	FirmwareSHA256  = "dc2fc8f0e43b96712d9fccc52a3a590769606412b3e1bc911d217addd3bef624"
)

// FirmwareURL is the download URL of the pinned firmware.
var FirmwareURL = fmt.Sprintf("https://github.com/cloud-hypervisor/edk2/releases/download/%s/CLOUDHV.fd", FirmwareVersion)

// Firmware returns the path of the pinned CLOUDHV.fd, downloading it if needed.
func (s *Store) Firmware(ctx context.Context) (string, error) {
	return s.Fetch(ctx, "firmware", FirmwareVersion, FirmwareURL, FirmwareSHA256)
}
