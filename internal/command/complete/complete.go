// Package complete provides shell completion functions shared by commands.
package complete

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/seirofile"
)

// Machines returns a completion function that suggests the machine names
// declared in the Seirofile. limit limits the number of machine arguments the
// command takes (0 means unlimited); names already given are not suggested again.
func Machines(cfg *config.Config, limit int) cli.ShellCompleteFunc {
	return func(ctx context.Context, cmd *cli.Command) {
		args := cmd.Args().Slice()
		if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
			cli.DefaultCompleteWithFlags(ctx, cmd)
			return
		}
		if limit > 0 && len(args) >= limit {
			return
		}
		// Completion must stay quiet: a missing or broken Seirofile simply yields no candidates.
		path, err := seirofile.Resolve(cfg.File)
		if err != nil {
			return
		}
		env, err := seirofile.Load(path)
		if err != nil {
			return
		}
		for _, m := range env.Machines {
			if slices.Contains(args, m.Name) {
				continue
			}
			fmt.Fprintln(cmd.Root().Writer, m.Name) //nolint:errcheck // completion output
		}
	}
}
