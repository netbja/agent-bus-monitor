package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reportHeadline is what the table shows of a report (report-one-row §4):
// the headline of its full text when it keeps one, else of its preview; the
// preview when it has none.
func reportHeadline(e bus.Event) string {
	text := e.Message
	if e.Full != "" {
		text = e.Full
	}
	if h := bus.ReportHeadline(text); h != "" {
		return bus.SanitizeReportMessage(h)
	}
	return e.Message
}

// reportsTable renders recent reports one line each (oldest→newest). A report that
// retained a full text (Full non-empty) gets a compact "(+N)" marker (N = full
// rune length) signalling `agentbus reports <id>` shows more.
func reportsTable(evs []bus.Event) string {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%s  %-12s %-5s %q", e.ID, e.Agent, e.RKind, reportHeadline(e))
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

// reportNotice is the directive report --to sends: the report's id (its full
// text is "agentbus reports <id>") and its headline (bus.ReportHeadline,
// report-one-row §4), else its first line; sanitised and cut at 300 runes.
func reportNotice(id, msg string) string {
	first := bus.ReportHeadline(msg)
	if first == "" {
		first, _, _ = strings.Cut(msg, "\n")
	}
	first = bus.SanitizeReportMessage(first)
	if r := []rune(first); len(r) > 300 {
		first = string(r[:300]) + "…"
	}
	return "Report " + id + " for you (full text: agentbus reports " + id + "): " + first
}

// sendReport is `agentbus report <agent> [--auto] [--needs answer|read] [--to <name>]... <message>`:
// the report, its addressees named on it (busmon-reply-target), then one
// notice per addressee, since a report wakes nobody. It returns the entry id.
func sendReport(ctx context.Context, b *bus.Bus, rest []string, stderr io.Writer) (string, error) {
	rest, auto := extractBool(rest, "--auto")
	rest, needs := extractFlag(rest, "--needs")
	if !bus.ValidNeeds(needs) {
		return "", fmt.Errorf("report: --needs %q: answer or read", needs)
	}
	var to []string
	for {
		var t string
		if rest, t = extractFlag(rest, "--to"); t == "" {
			break
		}
		to = append(to, t)
	}
	if len(rest) < 2 {
		return "", errors.New("usage: report <agent> [--auto] [--needs answer|read] [--to <agent>]... <message>")
	}
	for _, t := range to {
		if !bus.ValidName(t) {
			return "", fmt.Errorf("report: --to %q: invalid agent name", t)
		}
		if err := refuseAnonymousTarget(t); err != nil { // nothing is addressed to anonymous (P2-11)
			return "", fmt.Errorf("report: --to: %w", err)
		}
	}
	kind := bus.ReportNote
	if auto {
		kind = bus.ReportAuto
	}
	msg := strings.Join(rest[1:], " ")
	if note := reportShapeNote(msg); note != "" {
		fmt.Fprintln(stderr, note)
	}
	id, err := b.ReportNeeds(ctx, rest[0], kind, msg, to, needs)
	if err != nil {
		return "", err
	}
	for _, t := range to {
		if _, err := b.ReportNotice(ctx, rest[0], t, id, reportNotice(id, msg), needs); err != nil {
			return "", fmt.Errorf("report %s published, but telling %s failed: %v", id, t, err)
		}
	}
	return id, nil
}
