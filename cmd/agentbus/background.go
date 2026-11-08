package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// backgroundDeps is what background needs from the bus (progress-line §4).
type backgroundDeps struct {
	Self  string // the writer, recorded as by
	Set   func(ctx context.Context, agent, text, by string, d time.Duration) error
	Clear func(ctx context.Context, agent string) error
}

const backgroundUsage = `usage: background <agent> "<text>" [--for <duration>]   (default 30m, at most 24h)
       background <agent> --clear`

// runBackground sets or clears an agent's background work: busmon shows it
// on the agent's row, in the header and in the idle view until it is
// cleared or its time runs out.
func runBackground(ctx context.Context, d backgroundDeps, args []string, stdout, stderr io.Writer) int {
	args, clear := extractBool(args, "--clear")
	args, forText := extractFlag(args, "--for")
	usage := func() int { fmt.Fprintln(stderr, backgroundUsage); return 2 }
	if len(args) == 0 {
		return usage()
	}
	agent, text := args[0], strings.Join(args[1:], " ")
	if clear {
		if text != "" || forText != "" {
			return usage()
		}
		if err := d.Clear(ctx, agent); err != nil {
			fmt.Fprintln(stderr, "background:", err)
			return 1
		}
		fmt.Fprintf(stdout, "background cleared: %s\n", agent)
		return 0
	}
	if text == "" {
		return usage()
	}
	dur := bus.BackgroundDefaultFor
	if forText != "" {
		v, err := time.ParseDuration(forText)
		if err != nil {
			fmt.Fprintf(stderr, "background: --for %q is not a duration (20m, 1h)\n", forText)
			return usage()
		}
		dur = v
	}
	if err := d.Set(ctx, agent, text, d.Self, dur); err != nil {
		fmt.Fprintln(stderr, "background:", err)
		return 1
	}
	fmt.Fprintf(stdout, "background set: %s %q for %s (until it is cleared or the time runs out)\n", agent, text, dur)
	return 0
}
