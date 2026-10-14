package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// shutdownBlockers lists why a global shutdown must be refused: board tasks
// that are not done, and peers still working or blocked. self (the agent
// running the shutdown, usually master) is excluded — it is working by
// definition. A peer whose last heartbeat is older than agentStaleAfter is
// dead, not busy, and does not block. Sorted for stable output.
func shutdownBlockers(board map[string]bus.BoardEntry, agents map[string]bus.AgentSnapshot, self string, now time.Time) []string {
	var out []string
	for task, e := range board {
		if e.State != "done" {
			out = append(out, fmt.Sprintf("task %s is %s (%s)", task, e.State, e.Owner))
		}
	}
	for name, a := range agents {
		if name == self {
			continue
		}
		if a.State != "working" && a.State != "blocked" && a.State != "waiting" {
			continue
		}
		if now.Sub(time.UnixMilli(a.TS)) > agentStaleAfter {
			continue // stale heartbeat: dead, not busy
		}
		out = append(out, fmt.Sprintf("agent %s is %s", name, a.State))
	}
	sort.Strings(out)
	return out
}

// shutdownTTL bounds how long a shutdown directive stays actionable. Past it the
// entry drains as a payload-less "expired" error, so an agent's next session
// is never stopped by the previous session's unread shutdown.
var shutdownTTL = 10 * time.Minute

// broadcastShutdown sends the shutdown directive to every peer except self and
// returns how many were sent. It is wake=no: a stopping team must not be woken
// by its own stop signal; and it expires after shutdownTTL (Redis time).
// Per-peer failures are reported to errw and skipped.
func broadcastShutdown(ctx context.Context, b *bus.Bus, self string, agents map[string]bus.AgentSnapshot, msg string, errw io.Writer) int {
	n := 0
	for name := range agents {
		if name == self {
			continue // whoever runs this shuts down last, by hand
		}
		if _, err := b.CmdNoWakeTTL(ctx, self, name, bus.CmdDirective, "", msg, shutdownTTL); err != nil {
			fmt.Fprintf(errw, "shutdown: cmd to %s failed: %v\n", name, err)
			continue
		}
		n++
	}
	return n
}

// runCmd implements `agentbus cmd [--ref T] [--no-wake] <target> <command>`
// and prints the new entry id (the thread root).
func runCmd(ctx context.Context, b *bus.Bus, self string, args []string, out io.Writer) error {
	args, ref := extractFlag(args, "--ref")
	args, noWake := extractBool(args, "--no-wake")
	if len(args) < 2 {
		return fmt.Errorf("usage: cmd [--ref T] [--no-wake] <target> <command>")
	}
	send := b.Cmd
	if noWake {
		send = b.CmdNoWake
	}
	id, err := send(ctx, self, args[0], bus.CmdDirective, ref, strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, id)
	return err
}
