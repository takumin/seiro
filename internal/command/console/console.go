package console

import (
	"context"
	"errors"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:          "console",
		Usage:         "attach to the serial console of a machine (detach with Ctrl-])",
		ArgsUsage:     "<machine>",
		ShellComplete: complete.Machines(cfg, 1),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return errors.New("console requires exactly one machine name")
			}
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			stdin, ok := cmd.Root().Reader.(*os.File)
			if !ok {
				return errors.New("console requires a terminal or file on stdin")
			}
			return m.Console(ctx, cmd.Args().First(), stdin, cmd.Root().Writer)
		},
	}
}
