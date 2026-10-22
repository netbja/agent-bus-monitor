package main

import (
	"context"
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

// rollUp is bus.RollUp (kept as a local name for the CLI code).
func rollUp(vs []bus.Verdict) (string, int) { return bus.RollUp(vs) }

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
		if v.Message != "" || v.Full != "" || v.TextComplete != "yes" {
			fmt.Fprintf(&sb, "  %q", verdictText(v))
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
		if v.Reviewer != v.Author && v.AuthorProvider != "" && v.AuthorProvider == v.ReviewerProvider {
			sb.WriteString("  (same provider)") // a soft note: it never changes the state
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
		fmt.Fprintf(&sb, "%-9s %-12s %-7s %-12s",
			humanAge(now.Sub(time.UnixMilli(v.TS))), v.Subject, v.Decision, v.Reviewer)
		if v.Message != "" || v.Full != "" || v.TextComplete != "yes" {
			fmt.Fprintf(&sb, "  %q", verdictText(v))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func verdictText(v bus.Verdict) string {
	body := v.Message
	if v.Full != "" {
		body = v.Full
	}
	if v.TextComplete == "" {
		return "[completeness unknown: legacy entry] " + body
	}
	if v.TextComplete == "no" {
		return "[truncated at publication] " + body
	}
	return body
}

// verdictProviders reads the author's and the reviewer's providers from
// their snapshots; a failed read or an unknown agent gives "" (the ledger
// then records nothing: the fields are additive and never block a verdict).
func verdictProviders(ctx context.Context, b *bus.Bus, author, reviewer string) (string, string) {
	m, err := b.Agents(ctx)
	if err != nil {
		return "", ""
	}
	prov := func(name string) string {
		s := m[name]
		return bus.ProviderFor(s.Client, s.Provider)
	}
	return prov(author), prov(reviewer)
}
