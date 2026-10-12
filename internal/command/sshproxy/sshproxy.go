package sshproxy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/vsock"
)

// NewCommand returns the hidden command ssh runs as its ProxyCommand.
func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "ssh-proxy",
		Usage:     "relay stdin and stdout to a guest vsock port (internal)",
		ArgsUsage: "<vsock socket> <port>",
		Hidden:    true,
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "wait",
				Usage: "how long to keep retrying while the guest is not listening yet",
				// A guest without a NIC spends minutes in systemd-networkd-wait-online.
				Value: 3 * time.Minute,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 2 {
				return errors.New("ssh-proxy requires the vsock socket and the port")
			}
			port, err := strconv.ParseUint(cmd.Args().Get(1), 10, 32)
			if err != nil {
				return err
			}
			conn, err := vsock.DialWait(ctx, cmd.Args().First(), uint32(port), cmd.Duration("wait"), func() {
				// ssh shows the proxy's stderr, so this reaches the user.
				fmt.Fprintf(cmd.Root().ErrWriter, "seiro: waiting up to %s for sshd in the guest to listen on vsock port %d...\n", cmd.Duration("wait"), port) //nolint:errcheck // progress output
			})
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // proxy connection
			return vsock.Relay(conn, cmd.Root().Reader, cmd.Root().Writer)
		},
	}
}
