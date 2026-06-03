package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// watchOutcome is why one subscribe tick ended. It maps to the exit-code +
// rearm-sentinel contract every runtime branches on.
type watchOutcome int

const (
	outcomeCmd       watchOutcome = iota // delivered an addressed cmd
	outcomeHeartbeat                     // idle window elapsed; re-arm
	outcomeTransient                     // recoverable error; re-arm
	outcomeFatal                         // misconfiguration; do NOT re-arm
)

// rearmSentinel returns the final machine-readable stdout line and the process
// exit code for a subscribe tick. The contract every runtime follows: re-arm
// iff the line carries rearm=1 (every outcome except fatal). ref/from populate
// the cmd line; msg the error/fatal line.
func rearmSentinel(o watchOutcome, ref, from, msg string) (line string, code int) {
	switch o {
	case outcomeCmd:
		return fmt.Sprintf("__AGENTBUS__ event=cmd rearm=1 ref=%s from=%s", ref, from), 0
	case outcomeHeartbeat:
		return "__AGENTBUS__ event=heartbeat rearm=1", 64
	case outcomeTransient:
		return "__AGENTBUS__ event=error rearm=1 msg=" + msg, 75
	default:
		return "__AGENTBUS__ event=fatal rearm=0 msg=" + msg, 1
	}
}

// printCmd writes one delivered cmd entry in the human-readable form agents
// already parse. Shared by the one-shot and --loop handlers.
func printCmd(out io.Writer, e bus.Event) {
	ref := ""
	if e.Ref != "" {
		ref = " ref=" + e.Ref
	}
	fmt.Fprintf(out, "[%s %s->%s%s] %s\n", e.Type, e.From, e.Target, ref, e.Message)
}

// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. It arms a presence lease around the WatchCmd block and
// always disarms on return (the caller os.Exits on the returned code, so this
// function must never os.Exit itself — that would skip the defer). Presence is
// best-effort: a failed Arm/Disarm never blocks command delivery.
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, loop bool, out io.Writer) int {
	if !bus.ValidName(agent) {
		line, code := rearmSentinel(outcomeFatal, "", "", "invalid agent "+agent)
		fmt.Fprintln(out, line)
		return code
	}
	_ = b.Arm(ctx, agent, consumer, idle)       // best-effort observability
	defer b.Disarm(context.Background(), agent) // runs on return (never on os.Exit)

	if loop {
		// Headless continuous mode: keep the lease warm and print every addressed
		// cmd; never exit on delivery. Re-arm sentinels are for the terminal
		// wake path only, which --loop explicitly is NOT.
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			tick := idle / 2
			if tick <= 0 {
				tick = time.Second
			}
			tk := time.NewTicker(tick)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					_ = b.Arm(ctx, agent, consumer, idle)
				}
			}
		}()
		err := b.WatchCmd(ctx, agent, consumer, func(e bus.Event) bool {
			printCmd(out, e)
			return false // never "done" → consume continuously
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			line, code := rearmSentinel(outcomeTransient, "", "", err.Error())
			fmt.Fprintln(out, line)
			return code
		}
		return 0
	}

	var last bus.Event
	wctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	werr := b.WatchCmd(wctx, agent, consumer, func(e bus.Event) bool {
		last = e
		printCmd(out, e)
		return true // one-shot: stop on the first addressed entry
	})
	var line string
	var code int
	switch {
	case werr == nil:
		line, code = rearmSentinel(outcomeCmd, last.Ref, last.From, "")
	case errors.Is(werr, context.DeadlineExceeded):
		fmt.Fprintln(out, "__HEARTBEAT__") // deprecated; kept one release for existing agent loops
		line, code = rearmSentinel(outcomeHeartbeat, "", "", "")
	default:
		line, code = rearmSentinel(outcomeTransient, "", "", werr.Error())
	}
	fmt.Fprintln(out, line)
	return code
}
