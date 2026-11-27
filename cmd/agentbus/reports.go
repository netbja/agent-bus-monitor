package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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

// sendNotice sends report --to's notice (a seam for the failure test).
var sendNotice = func(ctx context.Context, b *bus.Bus, from, to, id, text, needs string) (string, error) {
	return b.ReportNotice(ctx, from, to, id, text, needs)
}

// sendReport is `agentbus report <agent> [--auto] [--needs answer|read] [--to <name>]...
// [--task <t> [--done]] <message>`: the report, its addressees named on it
// (busmon-reply-target), then one notice per addressee, since a report wakes
// nobody. It returns the entry id. --task (board-drift §4) names the tracked
// request: checked before anything is published (a refusal publishes
// nothing), then noted on it (reported_at, report_id). --done also completes
// it, as request done, with the report's first line and "(report <id>)";
// checked first too, so a refused done publishes nothing, but a done refused
// after the report (a change in between) leaves the report standing and
// returns its id with the error.
func sendReport(ctx context.Context, b *bus.Bus, rest []string, stderr io.Writer) (string, error) {
	rest, auto := extractBool(rest, "--auto")
	rest, done := extractBool(rest, "--done")
	rest, task := extractFlag(rest, "--task")
	if slices.Contains(rest, "--task") {
		return "", errors.New("report: --task takes one value, the request's task, once")
	}
	if done && task == "" {
		return "", errors.New("report: --done needs --task <task>")
	}
	if task != "" && !bus.ValidName(task) {
		return "", fmt.Errorf("report: --task %q: invalid task name", task)
	}
	rest, needs := extractFlag(rest, "--needs")
	if !bus.ValidNeeds(needs) {
		return "", fmt.Errorf("report: --needs %q: answer or read", needs)
	}
	if slices.Contains(rest, "--needs") { // no value after it, or given twice: never a report needing nothing (reviewer-codex)
		return "", errors.New("report: --needs takes one value, answer or read, once")
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
		return "", errors.New("usage: report <agent> [--auto] [--needs answer|read] [--to <agent>]... [--task <task> [--done]] <message>")
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
	var steps bus.StepsJudged
	if task != "" {
		if err := b.CheckReportTask(ctx, task, rest[0], done); err != nil {
			return "", fmt.Errorf("report: --task %s: %w", task, err)
		}
	}
	if done { // its open steps judged before anything is published (sibling-enforce §3.1)
		var pending, from string
		var err error
		if steps, pending, from, err = judgeDone(ctx, b, rest[0], task); err != nil {
			return "", fmt.Errorf("report: --done %s: %w", task, err)
		}
		if pending != "" {
			return "", fmt.Errorf("report: --done %s: step %s of %s is not merged yet: done once it merges, or ask %s to abandon it", task, pending, task, from)
		}
	}
	id, err := b.ReportTask(ctx, rest[0], kind, msg, to, needs, task)
	if err != nil {
		return "", err
	}
	if task != "" {
		if err := b.NoteRequest(ctx, task, rest[0], id); err != nil {
			return id, fmt.Errorf("report %s published, but noting it on %s failed: %w", id, task, err)
		}
	}
	for _, t := range to {
		if _, err := sendNotice(ctx, b, rest[0], t, id, reportNotice(id, msg), needs); err != nil {
			why := fmt.Sprintf("report %s published, but telling %s failed: %v", id, t, err)
			if done {
				why += "; request done " + task + " not run"
			}
			return id, errors.New(why) // the report stands: its id is printed (reviewer-codex)
		}
	}
	if done {
		first := strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
		if _, err := b.CompleteRequestSteps(ctx, task, rest[0], first+" (report "+id+")", "", "", steps); err != nil {
			return id, fmt.Errorf("report %s published, but request done %s was refused: %w", id, task, err)
		}
		auditStepsMerged(ctx, b, rest[0], task, steps)
	}
	return id, nil
}
