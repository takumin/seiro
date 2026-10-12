package logs

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:          "logs",
		Usage:         "show the serial output (or another log) of a machine",
		ArgsUsage:     "<machine>",
		ShellComplete: complete.Machines(cfg, 1),
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "keep printing appended output"},
			&cli.BoolFlag{Name: "vmm", Usage: "show the cloud-hypervisor log"},
			&cli.StringFlag{Name: "passt", Usage: "show the passt log of the given NIC"},
			&cli.BoolFlag{Name: "supervise", Usage: "show the supervisor log"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return errors.New("logs requires exactly one machine name")
			}
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			source := machine.LogSerial
			switch {
			case cmd.Bool("vmm"):
				source = machine.LogVMM
			case cmd.Bool("supervise"):
				source = machine.LogSupervise
			}
			path, err := m.LogPath(cmd.Args().First(), source, cmd.String("passt"))
			if err != nil {
				return err
			}
			return machine.Logs(ctx, path, cmd.Bool("follow"), cmd.Root().Writer)
		},
	}
}
