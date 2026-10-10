package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

const dispatchUsage = "usage: dispatch [--horizon D] [--escalate-after D] [--escalate-to NAME] | dispatch --status"

// runDispatch is `agentbus dispatch`: run the reconciliation loop in the
// foreground (JSON decision lines on stderr), or with --status report whether
// any dispatcher holds the project's lease (exit 0 up, 1 down). A dispatcher
// that finds another running, or loses its lease, exits 0: that is the lease
// doing its job, not a failure. Bad flags exit 2.
func runDispatch(ctx context.Context, b *bus.Bus, args []string, rt dispatch.Runtime, stdout, stderr io.Writer) int {
	cfg := dispatch.DefaultConfig()
	cfg.Log = stderr
	status := false
	usage := func(msg string) int {
		fmt.Fprintln(stderr, "error: "+msg)
		fmt.Fprintln(stderr, dispatchUsage)
		return 2
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--status" {
			status = true
			continue
		}
		var dst *time.Duration
		switch a {
		case "--horizon":
			dst = &cfg.Horizon
		case "--escalate-after":
			dst = &cfg.EscalateAfter
		case "--escalate-to":
		default:
			return usage(fmt.Sprintf("dispatch: unknown argument %q", a))
		}
		if i+1 >= len(args) {
			return usage(a + " needs a value")
		}
		i++
		v := args[i]
		if dst == nil {
			if !bus.ValidName(v) {
				return usage(fmt.Sprintf("--escalate-to: invalid agent name %q", v))
			}
			cfg.EscalateTo = v
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return usage(fmt.Sprintf("%s: want a positive duration like 10m, got %q", a, v))
		}
		*dst = d
	}

	if status {
		alive, err := b.DispatchAlive(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		}
		if alive {
			fmt.Fprintln(stdout, "dispatch up")
			return 0
		}
		fmt.Fprintln(stdout, "dispatch down")
		return 1
	}

	err := dispatch.Run(ctx, b, rt, cfg)
	switch {
	case errors.Is(err, dispatch.ErrAlreadyRunning):
		fmt.Fprintf(stderr, "dispatch already running for project %q; exiting\n", b.Project())
		return 0
	case errors.Is(err, dispatch.ErrLeaseLost):
		fmt.Fprintf(stderr, "dispatch lease lost for project %q; exiting\n", b.Project())
		return 0
	case err != nil:
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	return 0
}
