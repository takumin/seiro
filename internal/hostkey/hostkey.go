// Package hostkey manages the SSH host key seiro gives a machine, so that the
// host can verify the guest without trusting it on first use.
package hostkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Key is an ed25519 host key in the formats cloud-init and OpenSSH expect.
type Key struct {
	// Private is the private key in OpenSSH PEM format.
	Private string `json:"private"`
	// Public is the public key in authorized_keys format, without a trailing newline.
	Public string `json:"public"`
}

// Ensure loads the private key at path, generating and saving a new one when
// the file does not exist yet.
func Ensure(path string) (*Key, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path inside the machine state directory
	if errors.Is(err, os.ErrNotExist) {
		return create(path)
	}
	if err != nil {
		return nil, err
	}
	return parse(data)
}

func create(path string) (*Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(block)
	key, err := parse(data)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func parse(data []byte) (*Key, error) {
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse host key: %w", err)
	}
	pub := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(signer.PublicKey())), "\n")
	return &Key{Private: string(data), Public: pub}, nil
}

// KnownHosts renders a known_hosts file that trusts the key for alias.
func (k *Key) KnownHosts(alias string) string {
	return alias + " " + k.Public + "\n"
}
