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
// that finds another running exits 0: that is the lease doing its job. One that
// loses its lease re-acquires while the project exists, and exits 0 once the
// project is deleted (superviseDispatch). Bad flags exit 2.
// dispatchBaseConfig is the starting config; tests shrink its lease timings.
var dispatchBaseConfig = dispatch.DefaultConfig

func runDispatch(ctx context.Context, b *bus.Bus, args []string, rt dispatch.Runtime, stdout, stderr io.Writer) int {
	cfg := dispatchBaseConfig()
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

	return superviseDispatch(ctx, b, rt, cfg, stderr)
}

// Re-acquire backoff after a lease loss: 1s doubling to 30s (spec §6).
const (
	dispatchRetryMin = time.Second
	dispatchRetryMax = 30 * time.Second
)

// superviseDispatch runs dispatch.Run and, when its lease is lost while the
// project lives on (a lapse after suspend or a broker restart), backs off and
// runs it again — Run re-acquires. A deleted project (no key besides the lease)
// or another dispatcher already holding the lease exits 0. Once a lease has
// been lost, errors (a broker still restarting) are retried, never fatal.
func superviseDispatch(ctx context.Context, b *bus.Bus, rt dispatch.Runtime, cfg dispatch.Config, stderr io.Writer) int {
	retrying := false
	var backoff time.Duration
	for {
		started := time.Now()
		err := dispatch.Run(ctx, b, rt, cfg)
		if ctx.Err() != nil {
			return 0
		}
		switch {
		case errors.Is(err, dispatch.ErrAlreadyRunning):
			fmt.Fprintf(stderr, "dispatch already running for project %q; exiting\n", b.Project())
			return 0
		case errors.Is(err, dispatch.ErrLeaseLost):
			retrying = true
			if time.Since(started) >= dispatchRetryMax {
				backoff = 0 // it held the lease a while: start the backoff over
			}
		case err == nil:
			return 0
		case !retrying:
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		default:
			fmt.Fprintf(stderr, "dispatch re-acquire failed for project %q: %v; retrying\n", b.Project(), err)
		}
		// Is the project still there? A transient error here is retried too.
		for {
			alive, kerr := b.HasKeysBesidesDispatch(ctx)
			if ctx.Err() != nil {
				return 0
			}
			if kerr == nil && !alive {
				fmt.Fprintf(stderr, "dispatch lease lost and project %q is gone; exiting\n", b.Project())
				return 0
			}
			backoff = min(max(2*backoff, dispatchRetryMin), dispatchRetryMax)
			if kerr != nil {
				fmt.Fprintf(stderr, "dispatch: project check failed for %q: %v; retrying in %v\n", b.Project(), kerr, backoff)
			} else {
				fmt.Fprintf(stderr, "dispatch lease lost for project %q; re-acquiring in %v\n", b.Project(), backoff)
			}
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(backoff):
			}
			if kerr == nil {
				break
			}
		}
	}
}
