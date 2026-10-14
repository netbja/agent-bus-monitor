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

const (
	agentIdleAfter  = 2 * time.Minute
	agentStaleAfter = 10 * time.Minute
)

// humanAge renders a duration as a compact "Ns/Nm/Nh ago".
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// agentPane returns the agent's registered herdr pane and whether the agent is
// known with a non-empty pane.
func agentPane(m map[string]bus.AgentSnapshot, agent string) (string, bool) {
	s, ok := m[agent]
	if !ok || s.Pane == "" {
		return "", false
	}
	return s.Pane, true
}

// agentsTable renders the current state of every agent, one aged line each,
// sorted by name. Entries older than agentIdleAfter/agentStaleAfter are marked
// idle/offline (never deleted — age is the staleness signal).
func agentsTable(m map[string]bus.AgentSnapshot, now time.Time) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		age := now.Sub(time.UnixMilli(s.TS))
		when := humanAge(age)
		marker := ""
		switch {
		case s.TS == 0: // bound (agent-launch, bind) but no status declared yet
			when = "never"
		case age > agentStaleAfter:
			marker = "  · bus activity stale"
		case age > agentIdleAfter:
			marker = "  · bus activity quiet"
		}
		msg := ""
		if s.Message != "" {
			msg = "  (" + s.Message + ")"
		}
		pane := ""
		if s.Pane != "" {
			pane = "  ⧉" + s.Pane
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-9s%s%s%s\n", n, s.State, when, marker, pane, msg)
	}
	return sb.String()
}

// forgetDeps is what `agents forget` reads and writes, injected for tests.
type forgetDeps struct {
	Agents func(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Armed  func(ctx context.Context, agent string) (bool, error)
	Forget func(ctx context.Context, agent string) (bool, error)
	Now    func() time.Time
}

// runAgentsForget retires agents that are gone (a replaced team, a renamed
// role): their snapshot, usage, wake record, armed/gate/receiver keys and cmd
// group. It refuses an agent that is listening or declared a status in the
// last agentStaleAfter, unless --force: forgetting a live agent only makes it
// reappear at its next status, without its consumer group.
func runAgentsForget(ctx context.Context, d forgetDeps, args []string, out, errw io.Writer) int {
	args, force := extractBool(args, "--force")
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: agents forget <agent>... [--force]")
		return 1
	}
	for _, a := range args {
		if !bus.ValidName(a) {
			fmt.Fprintf(errw, "agents forget: invalid agent name %q\n", a)
			return 1
		}
	}
	snaps, err := d.Agents(ctx)
	if err != nil {
		fmt.Fprintf(errw, "agents forget: %v\n", err)
		return 1
	}
	code := 0
	for _, a := range args {
		if !force {
			if armed, err := d.Armed(ctx, a); err != nil || armed {
				fmt.Fprintf(errw, "%s: still listening on the bus, not forgotten (--force to override)\n", a)
				code = 1
				continue
			}
			if s, ok := snaps[a]; ok && s.TS != 0 && d.Now().Sub(time.UnixMilli(s.TS)) < agentStaleAfter {
				fmt.Fprintf(errw, "%s: declared a status %s, not forgotten (--force to override)\n", a, humanAge(d.Now().Sub(time.UnixMilli(s.TS))))
				code = 1
				continue
			}
		}
		found, err := d.Forget(ctx, a)
		switch {
		case err != nil:
			fmt.Fprintf(errw, "%s: %v\n", a, err)
			code = 1
		case found:
			fmt.Fprintf(out, "%s: forgotten\n", a)
		default:
			fmt.Fprintf(out, "%s: nothing to forget\n", a)
		}
	}
	return code
}
