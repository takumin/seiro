package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/complete"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/machine"
)

func NewCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "ssh",
		Usage: "log in to a machine with ssh over vsock",
		Description: "The user defaults to the first user created by cloud_init, or $USER.\n" +
			"Arguments after the machine are passed to ssh, e.g. `seiro ssh dev -L 8080:localhost:80` or `seiro ssh dev uptime`.",
		ArgsUsage:       "[user@]<machine> [ssh arguments...]",
		SkipFlagParsing: true,
		ShellComplete:   complete.Machines(cfg, 1),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() < 1 || strings.HasPrefix(cmd.Args().First(), "-") {
				return errors.New("ssh requires a machine name as its first argument")
			}
			user, name, ok := strings.Cut(cmd.Args().First(), "@")
			if !ok {
				user, name = "", user
			}
			m, err := machine.Open(cfg.File, cmd.Root().Writer)
			if err != nil {
				return err
			}
			args, err := m.SSHArgs(name, user, cmd.Args().Tail())
			if err != nil {
				return err
			}
			path, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH: %w", err)
			}
			// Replace seiro so that ssh owns the terminal and its exit status is ours.
			return syscall.Exec(path, args, os.Environ()) // #nosec G204 -- runs the user's ssh
		},
	}
}
