package main

import (
	"fmt"
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
		marker := ""
		switch {
		case age > agentStaleAfter:
			marker = "  · offline"
		case age > agentIdleAfter:
			marker = "  · idle"
		}
		msg := ""
		if s.Message != "" {
			msg = "  (" + s.Message + ")"
		}
		pane := ""
		if s.Pane != "" {
			pane = "  ⧉" + s.Pane
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-9s%s%s%s\n", n, s.State, humanAge(age), marker, pane, msg)
	}
	return sb.String()
}
