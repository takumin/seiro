package eval

import (
	"context"
	"encoding/json"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "eval",
		Usage: "evaluate the Seirofile and print the result as JSON",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.Root().Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(m.Spec)
		},
	}
}
