package command

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/takumin/seiro/internal/command/console"
	"github.com/takumin/seiro/internal/command/destroy"
	"github.com/takumin/seiro/internal/command/down"
	"github.com/takumin/seiro/internal/command/envcmd"
	"github.com/takumin/seiro/internal/command/eval"
	"github.com/takumin/seiro/internal/command/logs"
	"github.com/takumin/seiro/internal/command/ssh"
	"github.com/takumin/seiro/internal/command/sshconfig"
	"github.com/takumin/seiro/internal/command/sshproxy"
	"github.com/takumin/seiro/internal/command/status"
	"github.com/takumin/seiro/internal/command/supervise"
	"github.com/takumin/seiro/internal/command/up"
	"github.com/takumin/seiro/internal/config"
	"github.com/takumin/seiro/internal/metadata"
	"github.com/takumin/seiro/internal/version"
)

const (
	ExitOK int = 0
	ExitNG int = 1
)

func Main(stdout io.Writer, stderr io.Writer, stdin io.Reader, args []string) int {
	cfg := config.NewConfig(
		config.LogLevel("info"),
		config.LogFormat("text"),
	)

	// Logs go to stderr so that command output on stdout stays machine readable.
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, nil)))

	flags := []cli.Flag{
		&cli.StringFlag{
			Name:        "log-level",
			Aliases:     []string{"l"},
			Usage:       "log level",
			Sources:     cli.EnvVars("LOG_LEVEL"),
			Value:       cfg.LogLevel,
			Destination: &cfg.LogLevel,
			Action: func(ctx context.Context, cmd *cli.Command, s string) error {
				switch cfg.LogLevel {
				case "debug":
					slog.SetLogLoggerLevel(slog.LevelDebug)
				case "info":
					slog.SetLogLoggerLevel(slog.LevelInfo)
				case "warn":
					slog.SetLogLoggerLevel(slog.LevelWarn)
				case "error":
					slog.SetLogLoggerLevel(slog.LevelError)
				default:
					return fmt.Errorf("unknown log level: %s", cfg.LogLevel)
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:        "log-format",
			Usage:       "log format (text or json)",
			Sources:     cli.EnvVars("LOG_FORMAT"),
			Value:       cfg.LogFormat,
			Destination: &cfg.LogFormat,
			Action: func(ctx context.Context, cmd *cli.Command, s string) error {
				switch cfg.LogFormat {
				case "text":
					slog.SetDefault(slog.New(slog.NewTextHandler(cmd.Root().ErrWriter, nil)))
				case "json":
					slog.SetDefault(slog.New(slog.NewJSONHandler(cmd.Root().ErrWriter, nil)))
				default:
					return fmt.Errorf("unknown log format: %s", cfg.LogFormat)
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:        "file",
			Usage:       "path to the Seirofile (default: search the current directory and its parents)",
			Sources:     cli.EnvVars("SEIROFILE"),
			Destination: &cfg.File,
		},
	}

	cmds := []*cli.Command{
		up.NewCommand(cfg),
		down.NewCommand(cfg),
		destroy.NewCommand(cfg),
		status.NewCommand(cfg),
		console.NewCommand(cfg),
		logs.NewCommand(cfg),
		ssh.NewCommand(cfg),
		sshconfig.NewCommand(cfg),
		eval.NewCommand(cfg),
		envcmd.NewCommand(cfg),
		supervise.NewCommand(cfg),
		sshproxy.NewCommand(cfg),
	}

	// MEMO: Authors field is invalid in urfave/cli/v3 v3.1.0
	app := &cli.Command{
		Name:                  metadata.AppName(),
		Usage:                 metadata.AppDesc(),
		Version:               fmt.Sprintf("%s (%s)", version.Version(), version.Revision()),
		Flags:                 flags,
		Commands:              cmds,
		EnableShellCompletion: true,
		Reader:                stdin,
		Writer:                stdout,
		ErrWriter:             stderr,
		ExitErrHandler:        func(ctx context.Context, cmd *cli.Command, err error) {},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, args); err != nil {
		slog.ErrorContext(ctx, "failed application", slog.Any("error", err))
		return ExitNG
	}

	return ExitOK
}
