package size_test

import (
	"testing"

	"github.com/takumin/seiro/internal/size"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   string
		want uint64
		err  bool
	}{
		"kib":          {in: "4KiB", want: 4 * size.KiB},
		"mib":          {in: "512MiB", want: 512 * size.MiB},
		"gib":          {in: "20GiB", want: 20 * size.GiB},
		"tib":          {in: "1TiB", want: size.TiB},
		"spaces":       {in: " 2 GiB ", want: 2 * size.GiB},
		"zero":         {in: "0GiB", err: true},
		"no suffix":    {in: "1024", err: true},
		"si suffix":    {in: "1GB", err: true},
		"negative":     {in: "-1GiB", err: true},
		"fraction":     {in: "1.5GiB", err: true},
		"overflow":     {in: "99999999999TiB", err: true},
		"empty":        {in: "", err: true},
		"suffix only":  {in: "GiB", err: true},
		"lower suffix": {in: "1gib", err: true},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := size.Parse(tt.in)
			if tt.err {
				if err == nil {
					t.Fatalf("Parse(%q) = %d, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()

	cases := map[uint64]string{
		0:                "0B",
		1000:             "1000B",
		4 * size.KiB:     "4KiB",
		512 * size.MiB:   "512MiB",
		1536 * size.MiB:  "1536MiB",
		20 * size.GiB:    "20GiB",
		2 * size.TiB:     "2TiB",
		size.GiB + 1:     "1073741825B",
		3*size.MiB + 512: "3146240B",
	}

	for in, want := range cases {
		if got := size.Format(in); got != want {
			t.Errorf("Format(%d) = %q, want %q", in, got, want)
		}
	}
}
