// Package chapi is a minimal client for the cloud-hypervisor REST API.
package chapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to a cloud-hypervisor API socket.
type Client struct {
	http *http.Client
}

// New returns a client for the API socket at path.
func New(path string) *Client {
	return &Client{http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}}
}

// Info is the subset of vm.info used by seiro.
type Info struct {
	State string `json:"state"`
}

func (c *Client) do(ctx context.Context, method, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost/api/v1/"+endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// Ping checks that the VMM is responding.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "vmm.ping", nil)
}

// Info returns information about the VM.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	var info Info
	if err := c.do(ctx, http.MethodGet, "vm.info", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// PowerButton triggers an ACPI power button event in the guest.
func (c *Client) PowerButton(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vm.power-button", nil)
}

// Shutdown stops the VMM process.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vmm.shutdown", nil)
}
