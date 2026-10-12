package supervise

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/supervise"
)

// NewCommand returns the hidden command that runs as the per-machine supervisor.
func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "supervise",
		Usage:     "run the supervisor of a machine (internal)",
		ArgsUsage: "<plan.json>",
		Hidden:    true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return errors.New("supervise requires the path of plan.json")
			}
			var p plan.Machine
			if err := env.ReadJSON(cmd.Args().First(), &p); err != nil {
				return err
			}
			// Signals are handled by the supervisor itself so that it can stop
			// the VM and clean up, so do not let them cancel the context.
			return supervise.Run(context.WithoutCancel(ctx), &p)
		},
	}
}
