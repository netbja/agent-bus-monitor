package main

import (
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reportsTable renders recent reports one line each (oldest→newest). A report that
// retained a full text (Full non-empty) gets a compact "(+N)" marker (N = full
// rune length) signalling `agentbus reports <id>` shows more.
func reportsTable(evs []bus.Event) string {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%s  %-12s %-5s %q", e.ID, e.Agent, e.RKind, e.Message)
		if e.Full != "" {
			fmt.Fprintf(&sb, "  (+%d)", len([]rune(e.Full)))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// reportDetail returns the full retained text of a report (Full when present, else
// the preview Message) — the payload of `agentbus reports <id>`.
func reportDetail(e bus.Event) string {
	if e.Full != "" {
		return e.Full
	}
	return e.Message
}
