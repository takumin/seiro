// Package size parses and formats byte sizes with binary suffixes.
package size

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Binary units.
const (
	KiB uint64 = 1 << 10
	MiB uint64 = 1 << 20
	GiB uint64 = 1 << 30
	TiB uint64 = 1 << 40
)

var units = []struct {
	suffix string
	factor uint64
}{
	{"TiB", TiB},
	{"GiB", GiB},
	{"MiB", MiB},
	{"KiB", KiB},
}

// Parse parses a size string such as "512MiB" or "20GiB" into bytes.
// Only the binary suffixes KiB, MiB, GiB and TiB are accepted.
func Parse(s string) (uint64, error) {
	t := strings.TrimSpace(s)
	for _, u := range units {
		num, ok := strings.CutSuffix(t, u.suffix)
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(num), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size %q: %w", s, err)
		}
		if n == 0 {
			return 0, fmt.Errorf("invalid size %q: must be greater than zero", s)
		}
		if n > math.MaxUint64/u.factor {
			return 0, fmt.Errorf("invalid size %q: too large", s)
		}
		return n * u.factor, nil
	}
	return 0, fmt.Errorf("invalid size %q: must end with KiB, MiB, GiB or TiB", s)
}

// Format formats bytes using the largest binary unit that divides it exactly.
func Format(b uint64) string {
	for _, u := range units {
		if b != 0 && b%u.factor == 0 {
			return fmt.Sprintf("%d%s", b/u.factor, u.suffix)
		}
	}
	return fmt.Sprintf("%dB", b)
}
