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
		if e.TextComplete == "" {
			sb.WriteString("  [completeness unknown]")
		} else if e.TextComplete == "no" {
			sb.WriteString("  [truncated at publication]")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// reportDetail returns the full retained text of a report (Full when present, else
// the preview Message) — the payload of `agentbus reports <id>`.
func reportDetail(e bus.Event) string {
	body := e.Message
	if e.Full != "" {
		body = e.Full
	}
	if e.TextComplete == "" {
		return "[completeness unknown: legacy entry]\n" + body
	}
	if e.TextComplete == "no" {
		return "[truncated at publication]\n" + body
	}
	return body
}
