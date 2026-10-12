package status

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
	"github.com/takumin/seiro/internal/size"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "show the state of machines",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			sts, err := m.Status()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.Root().Writer, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tCPUS\tMEMORY\tPORTS\tNOTE") //nolint:errcheck // table output
			for _, st := range sts {
				cpus, memory, ports := "-", "-", "-"
				if st.Spec != nil {
					cpus = fmt.Sprint(st.Spec.CPUs)
					memory = size.Format(st.Spec.Memory)
					var ps []string
					for _, p := range st.Spec.Ports {
						ps = append(ps, fmt.Sprintf("%d->%d/%s", p.Host, p.Guest, p.Protocol))
					}
					if len(ps) > 0 {
						ports = strings.Join(ps, ",")
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", st.Name, st.State, cpus, memory, ports, note(st)) //nolint:errcheck // table output
			}
			return w.Flush()
		},
	}
}

func note(st machine.Status) string {
	var notes []string
	if !st.Declared {
		notes = append(notes, "not in Seirofile")
	}
	if rt := st.Runtime; rt != nil && st.State == machine.StateStopped {
		switch {
		case rt.Error != "":
			notes = append(notes, "error: "+rt.Error)
		case rt.ExitSignal != "":
			notes = append(notes, "killed by "+rt.ExitSignal)
		case rt.ExitCode != nil && *rt.ExitCode != 0:
			notes = append(notes, fmt.Sprintf("exit code %d", *rt.ExitCode))
		}
	}
	return strings.Join(notes, "; ")
}
