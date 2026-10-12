package cloudinit

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Content types of user-data parts. cloud-init detects the type of a
// text/plain part from its first line (#cloud-config, #!, ...).
const (
	TypeDetect      = "text/plain"
	TypeCloudConfig = "text/cloud-config"
)

// Part is one part of a multipart user-data.
type Part struct {
	ContentType string
	Content     string
}

// Multipart combines parts into a MIME multipart user-data, which cloud-init
// processes in order. The output is deterministic for the same parts.
func Multipart(parts ...Part) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	sum := sha256.New()
	for _, p := range parts {
		sum.Write([]byte(p.ContentType + "\x00" + p.Content + "\x00"))
	}
	if err := w.SetBoundary("seiro-" + hex.EncodeToString(sum.Sum(nil))[:32]); err != nil {
		return "", err
	}
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", p.ContentType+`; charset="utf-8"`)
		h.Set("Content-Transfer-Encoding", "base64")
		pw, err := w.CreatePart(h)
		if err != nil {
			return "", err
		}
		if _, err := pw.Write([]byte(wrap(base64.StdEncoding.EncodeToString([]byte(p.Content)), 76))); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	// cloud-init only treats user-data as MIME when it has a MIME-Version header.
	return fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q\r\nMIME-Version: 1.0\r\n\r\n%s", w.Boundary(), body.String()), nil
}

func wrap(s string, n int) string {
	var b strings.Builder
	for len(s) > n {
		b.WriteString(s[:n] + "\r\n")
		s = s[n:]
	}
	b.WriteString(s + "\r\n")
	return b.String()
}

// DefaultUser returns the first user created by a #cloud-config user-data,
// or "" when it creates none (the distribution's default user does not count).
func DefaultUser(userData string) string {
	if !strings.HasPrefix(userData, "#cloud-config") {
		return ""
	}
	var cfg struct {
		Users any `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(userData), &cfg); err != nil {
		return ""
	}
	var users []any
	switch v := cfg.Users.(type) {
	case string:
		for _, s := range strings.Split(v, ",") {
			users = append(users, strings.TrimSpace(s))
		}
	case []any:
		users = v
	}
	for _, u := range users {
		var name string
		switch v := u.(type) {
		case string:
			name = v
		case map[string]any:
			name, _ = v["name"].(string)
		}
		if name != "" && name != "default" {
			return name
		}
	}
	return ""
}
