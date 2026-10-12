// Package envcmd implements the "env" command group.
package envcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/machine"
	"github.com/takumin/seiro/internal/spec"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "env",
		Usage: "manage environments",
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "list environments that have state",
				Action: list,
			},
			{
				Name:   "prune",
				Usage:  "remove environments whose Seirofile no longer exists",
				Action: prune,
			},
		},
	}
}

type entry struct {
	env      *env.Env
	missing  bool
	machines int
	running  int
}

func entries() ([]entry, error) {
	dirs, err := env.DefaultDirs()
	if err != nil {
		return nil, err
	}
	envs, err := env.List(dirs)
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, e := range envs {
		ent := entry{env: e}
		if _, err := os.Stat(e.Seirofile); errors.Is(err, os.ErrNotExist) {
			ent.missing = true
		}
		mgr := &machine.Manager{Env: e, Spec: &spec.Environment{}, Out: os.Stdout}
		sts, err := mgr.Status()
		if err != nil {
			return nil, err
		}
		for _, st := range sts {
			ent.machines++
			if st.State == machine.StateRunning {
				ent.running++
			}
		}
		out = append(out, ent)
	}
	return out, nil
}

func list(ctx context.Context, cmd *cli.Command) error {
	ents, err := entries()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.Root().Writer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tMACHINES\tRUNNING\tSEIROFILE") //nolint:errcheck // table output
	for _, ent := range ents {
		seirofile := ent.env.Seirofile
		if ent.missing {
			seirofile += " (missing)"
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", ent.env.ID, ent.machines, ent.running, seirofile) //nolint:errcheck // table output
	}
	return w.Flush()
}

func prune(ctx context.Context, cmd *cli.Command) error {
	ents, err := entries()
	if err != nil {
		return err
	}
	for _, ent := range ents {
		if !ent.missing {
			continue
		}
		if ent.running > 0 {
			fmt.Fprintf(cmd.Root().Writer, "%s: skipped, %d machine(s) still running\n", ent.env.ID, ent.running) //nolint:errcheck // progress output
			continue
		}
		if err := ent.env.Remove(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.Root().Writer, "%s: removed\n", ent.env.ID) //nolint:errcheck // progress output
	}
	return nil
}
