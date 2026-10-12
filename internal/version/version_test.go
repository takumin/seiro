package version_test

import (
	"regexp"
	"testing"

	"github.com/takumin/seiro/internal/version"
)

func TestVersion(t *testing.T) {
	reg := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	if got := version.Version(); got != "unknown" {
		if !reg.MatchString(got) {
			t.Errorf("expected version to be 'unknown' or '^[0-9]+\\.[0-9]+\\.[0-9]+$', but got '%s'", got)
		}
	}
}

func TestRevision(t *testing.T) {
	reg := regexp.MustCompile(`^[0-9a-z]{40}$`)
	if got := version.Revision(); got != "unknown" {
		if !reg.MatchString(got) {
			t.Errorf("expected revision to be 'unknown' or '^[0-9a-z]{40}$', but got '%s'", got)
		}
	}
}
