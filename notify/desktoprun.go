package notify

import (
	"context"
	"os/exec"
	"time"
)

// Moved from cmd/agentbus unchanged (dispatchsvc P6). Both run a program:
// notify is one of the packages the dispatch service's guard lists as such.

// NotifySend is notify-send with an urgency, bounded to 5 s; nil when it is
// not on PATH.
func NotifySend(lookPath func(string) (string, error), run func(ctx context.Context, bin string, args ...string) error) DesktopRun {
	bin, err := lookPath("notify-send")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body, urgency string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return run(ctx, bin, "-a", "agentbus", "-u", urgency, title, body)
	}
}

// RunQuiet runs a command and discards its output.
func RunQuiet(ctx context.Context, bin string, args ...string) error {
	return exec.CommandContext(ctx, bin, args...).Run()
}
