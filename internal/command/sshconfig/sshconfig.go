package sshconfig

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:          "ssh-config",
		Usage:         "print ssh_config entries that reach machines over vsock",
		ArgsUsage:     "[machine...]",
		ShellComplete: complete.Machines(cfg, 0),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			out, err := m.SSHConfig(cmd.Args().Slice())
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.Root().Writer, out)
			return err
		},
	}
}
