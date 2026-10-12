package cloudinit_test

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/takumin/seiro/internal/cloudinit"
)

func TestMultipart(t *testing.T) {
	t.Parallel()

	parts := []cloudinit.Part{
		{ContentType: cloudinit.TypeDetect, Content: "#cloud-config\npackages: [vim]\n"},
		{ContentType: cloudinit.TypeCloudConfig, Content: "#cloud-config\nruncmd: [[true]]\n" + strings.Repeat("x", 200)},
	}
	out, err := cloudinit.Multipart(parts...)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cloudinit.Multipart(parts...)
	if err != nil {
		t.Fatal(err)
	}
	if out != again {
		t.Error("Multipart is not deterministic")
	}

	msg, err := mail.ReadMessage(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("MIME-Version = %q", msg.Header.Get("MIME-Version"))
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type = %q, %v", mt, err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	for i, want := range parts {
		p, err := r.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type")); ct != want.ContentType {
			t.Errorf("part %d type = %q", i, ct)
		}
		// mime/multipart only decodes quoted-printable transfer encoding.
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeBase64(t, string(body)); got != want.Content {
			t.Errorf("part %d = %q, want %q", i, got, want.Content)
		}
	}
	if _, err := r.NextPart(); err != io.EOF {
		t.Errorf("extra part: %v", err)
	}
}

func TestDefaultUser(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"#cloud-config\nusers:\n  - name: dev\n  - name: ops\n":   "dev",
		"#cloud-config\nusers:\n  - default\n  - name: dev\n":     "dev",
		"#cloud-config\nusers: [default]\n":                       "",
		"#cloud-config\nusers: [default, alice]\n":                "alice",
		"#cloud-config\nusers: default, bob\n":                    "bob",
		"#cloud-config\npackages: [vim]\n":                        "",
		"#!/bin/sh\necho users\n":                                 "",
		"#cloud-config\nusers: [\n":                               "",
		"#cloud-config\nusers:\n  - sudo: ALL\n  - name: carol\n": "carol",
	}
	for in, want := range cases {
		if got := cloudinit.DefaultUser(in); got != want {
			t.Errorf("DefaultUser(%q) = %q, want %q", in, got, want)
		}
	}
}

func decodeBase64(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
