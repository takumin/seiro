package down

import (
	"context"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:          "down",
		Usage:         "stop machines",
		ArgsUsage:     "[machine...]",
		ShellComplete: complete.Machines(cfg, 0),
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force", Usage: "stop the VMM without asking the guest to power off"},
			&cli.DurationFlag{Name: "timeout", Usage: "how long to wait for the guest to power off", Value: 60 * time.Second},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			return m.Down(ctx, cmd.Args().Slice(), cmd.Bool("force"), cmd.Duration("timeout"))
		},
	}
}
