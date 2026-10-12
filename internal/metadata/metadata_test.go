package metadata_test

import (
	"testing"

	"github.com/takumin/seiro/internal/metadata"
)

func TestAppName(t *testing.T) {
	if len(metadata.AppName()) == 0 {
		t.Error("expected application description to not be empty")
	}
}

func TestAppDesc(t *testing.T) {
	if len(metadata.AppDesc()) == 0 {
		t.Error("expected application description to not be empty")
	}
}

func TestAuthorName(t *testing.T) {
	if len(metadata.AuthorName()) == 0 {
		t.Error("expected author name to not be empty")
	}
}
