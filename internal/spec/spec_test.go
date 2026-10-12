package spec_test

import (
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/size"
	"github.com/takumin/seiro/internal/spec"
)

const testSHA = "6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2"

func validMachine(name string) *spec.Machine {
	return &spec.Machine{
		Name:   name,
		CPUs:   1,
		Memory: 512 * size.MiB,
		Disks:  []spec.Disk{{Image: &spec.Image{URL: "https://example.com/a.img", SHA256: testSHA}, Size: 10 * size.GiB}},
		NICs:   []spec.NIC{{Name: "eth0", Network: spec.NetworkInternet}},
		Ports:  []spec.Port{{Host: 2222, Guest: 22, Protocol: "tcp"}},
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		mutate func(e *spec.Environment)
		errs   []string
	}{
		"valid": {mutate: func(e *spec.Environment) {}},
		"duplicate machine": {
			mutate: func(e *spec.Environment) { e.Machines = append(e.Machines, validMachine("a")) },
			errs:   []string{`machine "a": defined more than once`},
		},
		"bad hostname": {
			mutate: func(e *spec.Environment) { e.Machines[0].Name = "Bad_Name" },
			errs:   []string{"valid hostname"},
		},
		"cpus": {
			mutate: func(e *spec.Environment) { e.Machines[0].CPUs = 0 },
			errs:   []string{"cpus must be between"},
		},
		"memory too small": {
			mutate: func(e *spec.Environment) { e.Machines[0].Memory = 64 * size.MiB },
			errs:   []string{"memory must be at least 128MiB"},
		},
		"memory not aligned": {
			mutate: func(e *spec.Environment) { e.Machines[0].Memory = 512*size.MiB + 4*size.KiB },
			errs:   []string{"multiple of 1MiB"},
		},
		"no disks": {
			mutate: func(e *spec.Environment) { e.Machines[0].Disks = nil },
			errs:   []string{"at least one disk"},
		},
		"bad sha256": {
			mutate: func(e *spec.Environment) { e.Machines[0].Disks[0].Image.SHA256 = "ABC" },
			errs:   []string{"sha256 must be"},
		},
		"duplicate nic": {
			mutate: func(e *spec.Environment) {
				e.Machines[0].NICs = append(e.Machines[0].NICs, spec.NIC{Name: "eth0", Network: spec.NetworkInternet})
			},
			errs: []string{`nic "eth0": defined more than once`, "more than one nic attached to internet"},
		},
		"long nic name": {
			mutate: func(e *spec.Environment) { e.Machines[0].NICs[0].Name = "abcdefghijklmnop" },
			errs:   []string{"at most 15 characters"},
		},
		"unknown network": {
			mutate: func(e *spec.Environment) { e.Machines[0].NICs[0].Network = "pxe" },
			errs:   []string{`unsupported network "pxe"`, "ports require a nic attached to internet"},
		},
		"multicast mac": {
			mutate: func(e *spec.Environment) { e.Machines[0].NICs[0].MAC = "01:00:5e:00:00:01" },
			errs:   []string{"must be unicast"},
		},
		"invalid mac": {
			mutate: func(e *spec.Environment) { e.Machines[0].NICs[0].MAC = "zz" },
			errs:   []string{"invalid mac address"},
		},
		"ports without nic": {
			mutate: func(e *spec.Environment) { e.Machines[0].NICs = nil },
			errs:   []string{"ports require a nic attached to internet"},
		},
		"port zero": {
			mutate: func(e *spec.Environment) { e.Machines[0].Ports[0].Guest = 0 },
			errs:   []string{"ports must be between"},
		},
		"bad protocol": {
			mutate: func(e *spec.Environment) { e.Machines[0].Ports[0].Protocol = "sctp" },
			errs:   []string{"protocol must be"},
		},
		"duplicate host port across machines": {
			mutate: func(e *spec.Environment) { e.Machines = append(e.Machines, validMachine("b")) },
			errs:   []string{`host port tcp/2222 is already published by machine "a"`},
		},
		"same port different protocol": {
			mutate: func(e *spec.Environment) {
				b := validMachine("b")
				b.Ports[0].Protocol = "udp"
				e.Machines = append(e.Machines, b)
			},
		},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := &spec.Environment{Machines: []*spec.Machine{validMachine("a")}}
			tt.mutate(e)
			err := e.Validate()
			if len(tt.errs) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected errors %q, got nil", tt.errs)
			}
			for _, want := range tt.errs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestImages(t *testing.T) {
	t.Parallel()

	a := &spec.Image{URL: "https://example.com/a.img", SHA256: testSHA}
	b := &spec.Image{URL: "https://example.com/b.img"}
	e := &spec.Environment{Machines: []*spec.Machine{
		{Name: "x", Disks: []spec.Disk{{Image: a}, {Size: 1}}},
		{Name: "y", Disks: []spec.Disk{{Image: &spec.Image{URL: a.URL, SHA256: testSHA}}, {Image: b}}},
	}}

	images := e.Images()
	if len(images) != 2 {
		t.Fatalf("Images() returned %d images, want 2", len(images))
	}
	if images[0].CacheKey() != testSHA {
		t.Errorf("first cache key = %q, want sha256", images[0].CacheKey())
	}
	if !strings.HasPrefix(images[1].CacheKey(), "url-") {
		t.Errorf("second cache key = %q, want url- prefix", images[1].CacheKey())
	}
	if e.Machine("y") == nil || e.Machine("z") != nil {
		t.Error("Machine() lookup mismatch")
	}
}
