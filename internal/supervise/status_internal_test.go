package supervise

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestExitAttrs(t *testing.T) {
	t.Parallel()

	zero := 0
	cases := map[string]struct {
		status Status
		want   string
	}{
		"code":   {status: Status{ExitCode: &zero}, want: "msg=exited exit_code=0\n"},
		"signal": {status: Status{ExitSignal: "killed"}, want: "msg=exited signal=killed\n"},
		"error":  {status: Status{Error: "wait: boom"}, want: `msg=exited error="wait: boom"` + "\n"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			log := slog.New(slog.NewTextHandler(&b, &slog.HandlerOptions{
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
						return slog.Attr{}
					}
					return a
				},
			}))
			log.Info("exited", tt.status.exitAttrs()...)
			if got := b.String(); got != tt.want {
				t.Errorf("log = %q, want %q", got, tt.want)
			}
			if strings.Contains(b.String(), "0x") {
				t.Errorf("log contains a pointer: %q", b.String())
			}
		})
	}
}
