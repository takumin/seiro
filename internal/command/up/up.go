package up

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:          "up",
		Usage:         "create and start machines",
		ArgsUsage:     "[machine...]",
		ShellComplete: complete.Machines(cfg, 0),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			return m.Up(ctx, cmd.Args().Slice())
		},
	}
}
