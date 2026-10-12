package destroy

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
		Name:          "destroy",
		Usage:         "stop machines and delete their disks and state",
		ArgsUsage:     "[machine...]",
		ShellComplete: complete.Machines(cfg, 0),
		Flags: []cli.Flag{
			&cli.DurationFlag{Name: "timeout", Usage: "how long to wait for the guest to power off", Value: 60 * time.Second},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			return m.Destroy(ctx, cmd.Args().Slice(), cmd.Duration("timeout"))
		},
	}
}
