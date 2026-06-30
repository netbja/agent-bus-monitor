package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// overviewLimit bounds the no-argument `verdicts` listing (matches busmon's
// default activity window).
const overviewLimit = 25

// resolveSubject maps the --pr / --subject flags to a single subject key.
// Exactly one must be set: --pr N becomes "pr:N"; --subject S is used verbatim.
func resolveSubject(pr, subject string) (string, error) {
	pr = strings.TrimSpace(pr)
	subject = strings.TrimSpace(subject)
	switch {
	case pr != "" && subject != "":
		return "", fmt.Errorf("pass only one of --pr or --subject")
	case pr != "":
		return "pr:" + pr, nil
	case subject != "":
		return subject, nil
	default:
		return "", fmt.Errorf("a subject is required: pass --pr N or --subject S")
	}
}

// rollUp computes the 4-eyes state of a subject from its verdicts, which MUST be
// ordered oldest→newest (as bus.Verdicts returns them). An approve counts only
// when reviewer != author. A reject supersedes earlier verdicts, but a later
// independent approve wins again. Returns the state and the index of the
// deciding entry (-1 for PENDING).
func rollUp(vs []bus.Verdict) (state string, deciderIdx int) {
	indepApproveIdx, lastRejectIdx := -1, -1
	for i := range vs {
		switch vs[i].Decision {
		case "approve":
			if vs[i].Reviewer != vs[i].Author {
				indepApproveIdx = i
			}
		case "reject":
			lastRejectIdx = i
		}
	}
	switch {
	case indepApproveIdx >= 0 && indepApproveIdx > lastRejectIdx:
		return "APPROVED", indepApproveIdx
	case lastRejectIdx >= 0:
		return "REJECTED", lastRejectIdx
	default:
		return "PENDING", -1
	}
}

// exitForState maps a roll-up state to a scriptable exit code (1 is reserved for
// usage/connection errors).
func exitForState(state string) int {
	switch state {
	case "APPROVED":
		return 0
	case "REJECTED":
		return 3
	default: // PENDING
		return 2
	}
}

// verdictsReport renders the roll-up header plus one detail line per verdict
// (oldest→newest), and returns the matching exit code. Entries older than the
// deciding entry are marked "(superseded)"; self-approvals are marked
// "(author=…, ignored)".
func verdictsReport(subject string, vs []bus.Verdict, now time.Time) (string, int) {
	state, deciderIdx := rollUp(vs)
	var sb strings.Builder
	switch {
	case deciderIdx >= 0:
		d := vs[deciderIdx]
		mark := "✓"
		if state == "REJECTED" {
			mark = "✗"
		}
		fmt.Fprintf(&sb, "%s: %s %s (%s, %s)\n", subject, state, mark,
			d.Reviewer, humanAge(now.Sub(time.UnixMilli(d.TS))))
	case hasSelfApprove(vs):
		fmt.Fprintf(&sb, "%s: PENDING (only self-approval)\n", subject)
	default:
		fmt.Fprintf(&sb, "%s: PENDING\n", subject)
	}
	for i := range vs {
		v := vs[i]
		fmt.Fprintf(&sb, "  %-9s %-7s %-12s", humanAge(now.Sub(time.UnixMilli(v.TS))), v.Decision, v.Reviewer)
		if v.Message != "" {
			fmt.Fprintf(&sb, "  %q", v.Message)
		}
		if v.Ref != "" {
			fmt.Fprintf(&sb, "  ref=%s", v.Ref)
		}
		switch {
		case v.Reviewer == v.Author:
			fmt.Fprintf(&sb, "  (author=%s, ignored)", v.Author)
		case deciderIdx >= 0 && i < deciderIdx:
			sb.WriteString("  (superseded)")
		}
		sb.WriteByte('\n')
	}
	return sb.String(), exitForState(state)
}

// hasSelfApprove reports whether any verdict is a self-approval (used only to
// annotate a PENDING header).
func hasSelfApprove(vs []bus.Verdict) bool {
	for _, v := range vs {
		if v.Decision == "approve" && v.Reviewer == v.Author {
			return true
		}
	}
	return false
}

// verdictsOverview lists the most recent overviewLimit verdicts across all
// subjects, one line each, oldest→newest. No per-subject roll-up.
func verdictsOverview(vs []bus.Verdict, now time.Time) string {
	if len(vs) > overviewLimit {
		vs = vs[len(vs)-overviewLimit:]
	}
	var sb strings.Builder
	for _, v := range vs {
		fmt.Fprintf(&sb, "%-9s %-12s %-7s %-12s  %q\n",
			humanAge(now.Sub(time.UnixMilli(v.TS))), v.Subject, v.Decision, v.Reviewer, v.Message)
	}
	return sb.String()
}
