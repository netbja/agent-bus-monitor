package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// One approval for a small change (spec 2026-11-21-single-approval-design
// §4, build step 2): with the project's delivery.single_approval = small,
// deliver accepts one current approval on H when no reviewer's latest
// verdict is a reject (§4.3, §6 Q8), the change is small (§3.2) and the
// approver is of another provider than the author (§4.1), or of the same
// one and the change is in delivery.same_provider_ok's class (§4.8).

// smallPolicy is the project's single-approval settings (§3.1).
type smallPolicy struct {
	Mode               string // delivery.single_approval: off or small
	MaxLines, MaxFiles int
	Never              []string // delivery.never_small: globs added to the defaults
	SameProvider       string   // delivery.same_provider_ok: off, docs or small
}

// projectSmallPolicy reads them for project from config.toml: its
// [project.<project>.delivery] table, then the machine's (never for
// same_provider_ok), then the defaults (config.ProjectValue). No env
// variable is read. A file that cannot be read or parsed is an error.
func projectSmallPolicy(getenv func(string) string, project string) (smallPolicy, error) {
	f := config.Load(config.Path(getenv), "config.toml")
	for _, w := range f.Warnings {
		if w.Key == "" {
			return smallPolicy{}, errors.New("config.toml: " + w.Msg)
		}
	}
	get := func(name string) any { return config.ProjectValue(f, nil, project, name).Value }
	p := smallPolicy{Mode: get("delivery.single_approval").(string), MaxLines: get("delivery.small_max_lines").(int),
		MaxFiles: get("delivery.small_max_files").(int), SameProvider: get("delivery.same_provider_ok").(string)}
	for _, g := range strings.Split(get("delivery.never_small").(string), ",") {
		if g != "" {
			p.Never = append(p.Never, g)
		}
	}
	return p, nil
}

// oneApproval judges a delivery of subject@h with fewer than two current
// approvers under p, c being the change's class: the line the body and the
// trace carry, or the refusal.
func oneApproval(vs []bus.Verdict, subject, h string, c smallClass, p smallPolicy) (line, refusal string) {
	at := subject + "@" + h
	twoOrYes := " Two approvals, or the person's yes (--single-approval)."
	latest, order := map[string]bus.Verdict{}, []string(nil)
	for _, v := range vs {
		if _, seen := latest[v.Reviewer]; !seen {
			order = append(order, v.Reviewer)
		}
		latest[v.Reviewer] = v
	}
	for _, r := range order {
		if v := latest[r]; v.Reviewer != v.Author && (v.Decision == "reject" || v.Decision == "changes") {
			return "", fmt.Sprintf("%s has 1 approval, but %s's latest verdict on %s is a reject (on %s): one approval is not enough while it stands (single-approval §4.3).%s",
				at, r, subject, v.Head, twoOrYes)
		}
	}
	approvers := currentApprovers(vs, h)
	if len(approvers) == 0 {
		return "", at + " has no current independent approval." + twoOrYes
	}
	if !c.Small {
		return "", fmt.Sprintf("%s has 1 approval; one is enough only for a small change by a reviewer of another provider: %s.%s", at, c.Reason(), twoOrYes)
	}
	a := latest[approvers[0]]
	size := fmt.Sprintf("%d lines, %d files", c.Lines, c.Files)
	if a.ReviewerProvider != "" && a.AuthorProvider != "" && a.ReviewerProvider != a.AuthorProvider {
		return fmt.Sprintf("single approval (small: %s; approved by %s)", size, a.Reviewer), ""
	}
	kind := "small"
	if c.Docs {
		kind = "small docs"
	}
	if p.SameProvider == "small" || p.SameProvider == "docs" && c.Docs {
		return fmt.Sprintf("single approval, same provider (%s: %s; approved by %s)", kind, size, a.Reviewer), ""
	}
	of := "of the author's provider"
	if a.ReviewerProvider == "" || a.AuthorProvider == "" {
		of = "of an unknown provider"
	}
	why := fmt.Sprintf("%s has 1 approval, approved by %s, %s: delivery.same_provider_ok is %s", at, a.Reviewer, of, p.SameProvider)
	if p.SameProvider == "docs" {
		why += ", and this change is not docs-only"
	}
	return "", why + "; one is enough only from a reviewer of another provider (single-approval §4.1, §4.8)." + twoOrYes
}
