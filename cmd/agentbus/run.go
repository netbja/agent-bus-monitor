package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// agentbus run | run reset (run-budget §5, spec
// docs/superpowers/specs/2026-11-15-run-budget-design.md): the run's state,
// its time and deliveries against run.max_duration and run.max_deliveries;
// reset, the person's only, deletes it and the dispatcher's next
// merge-queue pass starts a new run with a new baseline. Exit 0, 2 on a
// usage error or a refusal.
func runRun(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, out, errw io.Writer) int {
	switch {
	case len(args) == 0:
		return printRun(ctx, b, getenv, out, errw)
	case len(args) == 1 && args[0] == "reset":
		// The person is a caller outside the agents' environment, as deliver
		// recognises them: defence in depth, not a boundary (§3, §5).
		if a := getenv("AGENT_BUS_AGENT"); a != "" {
			fmt.Fprintf(errw, "run reset: only the person restarts a run (this is %s's agent environment, AGENT_BUS_AGENT)\n", a)
			return 2
		}
		if err := b.ResetRun(ctx); err != nil {
			fmt.Fprintln(errw, "run reset:", err)
			return 2
		}
		fmt.Fprintln(out, "run reset: the next merge-queue pass starts a new run (its baseline: the heads already delivered)")
		return 0
	}
	fmt.Fprintln(errw, "usage: run | run reset")
	return 2
}

// printRun prints the run against its limits; a limit reached is named.
func printRun(ctx context.Context, b *bus.Bus, getenv func(string) string, out, errw io.Writer) int {
	rec, err := b.Run(ctx)
	if err != nil {
		fmt.Fprintln(errw, "run:", err)
		return 2
	}
	if rec == nil {
		fmt.Fprintln(out, "no run yet: the dispatch service's next merge-queue pass starts one")
		return 0
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		fmt.Fprintln(errw, "run:", err)
		return 2
	}
	r := config.Machine(getenv)
	maxDur, maxDel := r.Duration("run.max_duration"), r.Int("run.max_deliveries")
	elapsed := rec.Elapsed(now.UnixMilli())
	fmt.Fprintf(out, "state       %s since %s\n", rec.State, time.UnixMilli(rec.Since).Local().Format("2006-01-02 15:04"))
	if rec.Why != "" {
		fmt.Fprintf(out, "why         %s\n", rec.Why)
	}
	var reached []string
	if maxDur > 0 {
		fmt.Fprintf(out, "elapsed     %s of %s\n", runDuration(elapsed), runDuration(maxDur))
		if elapsed >= maxDur {
			reached = append(reached, "duration")
		}
	} else {
		fmt.Fprintf(out, "elapsed     %s (no limit)\n", runDuration(elapsed))
	}
	if maxDel > 0 {
		fmt.Fprintf(out, "deliveries  %d of %d\n", rec.Deliveries, maxDel)
		if rec.Deliveries >= maxDel {
			reached = append(reached, "deliveries")
		}
	} else {
		fmt.Fprintf(out, "deliveries  %d (no limit)\n", rec.Deliveries)
	}
	for _, why := range reached {
		fmt.Fprintf(out, "limit reached: %s\n", why)
	}
	return 0
}

// runDuration is 3h10m, 45m, or 0m.
func runDuration(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}
