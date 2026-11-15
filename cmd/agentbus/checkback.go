package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus checkback <duration> "<what>" | checkback --clear | checkback
// (master-idle §4): an agent with work in flight off the bus says when to
// look again, so the stall guard sees it; a new one replaces the old one.
// With no argument it lists every agent's. Exit 0, 2 on a usage error.
func runCheckback(ctx context.Context, b *bus.Bus, self string, args []string, out, errw io.Writer) int {
	usage := func(why string) int {
		if why != "" {
			fmt.Fprintln(errw, "checkback:", why)
		}
		fmt.Fprintln(errw, `usage: checkback <duration> "<what is in flight>" | checkback --clear | checkback`)
		return 2
	}
	switch {
	case len(args) == 0:
		all, err := b.Checkbacks(ctx)
		if err != nil {
			fmt.Fprintln(errw, "checkback:", err)
			return 2
		}
		printCheckbacks(all, time.Now(), out)
		return 0
	case len(args) == 1 && args[0] == "--clear":
		ok, err := b.ClearCheckback(ctx, self)
		switch {
		case err != nil:
			fmt.Fprintln(errw, "checkback:", err)
			return 2
		case ok:
			fmt.Fprintf(out, "%s: check-back cleared\n", self)
		default:
			fmt.Fprintf(out, "%s: no check-back to clear\n", self)
		}
		return 0
	case len(args) != 2:
		return usage("")
	}
	d, err := time.ParseDuration(args[0])
	if err != nil || d < time.Minute || d > checkbackMax {
		return usage(fmt.Sprintf("the duration is from 1m to %s (30m, 2h)", checkbackMax))
	}
	c, err := b.SetCheckback(ctx, self, d, args[1])
	if err != nil {
		return usage(err.Error())
	}
	line := fmt.Sprintf("%s: check back in %s: %s", self, args[0], c.What)
	if c.Count > 1 {
		line += fmt.Sprintf(" (set %d times since %s)", c.Count, time.UnixMilli(c.FirstSetID).Local().Format("15:04"))
	}
	fmt.Fprintln(out, line)
	return 0
}

// checkbackMax bounds a check-back's delay: work in flight is looked at again
// within a week, or it is not in flight.
const checkbackMax = 7 * 24 * time.Hour

// printCheckbacks lists the check-backs, soonest due first.
func printCheckbacks(all map[string]bus.Checkback, now time.Time, out io.Writer) {
	if len(all) == 0 {
		fmt.Fprintln(out, "no check-back set")
		return
	}
	cs := make([]bus.Checkback, 0, len(all))
	for _, c := range all {
		cs = append(cs, c)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].DueMs < cs[j].DueMs })
	for _, c := range cs {
		due := time.UnixMilli(c.DueMs)
		when := "due in " + roundAge(due.Sub(now))
		if !due.After(now) {
			when = "due " + roundAge(now.Sub(due)) + " ago"
		}
		line := fmt.Sprintf("%s  %s", c.Agent, when)
		if c.Count > 1 {
			line += fmt.Sprintf(" (set %d times since %s)", c.Count, time.UnixMilli(c.FirstSetID).Local().Format("15:04"))
		}
		fmt.Fprintf(out, "%s: %s\n", line, c.What)
	}
}

// roundAge is a duration in whole minutes, hours past two.
func roundAge(d time.Duration) string {
	if d >= 2*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
