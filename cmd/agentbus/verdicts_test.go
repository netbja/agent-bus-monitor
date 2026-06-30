package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestResolveSubject(t *testing.T) {
	if s, err := resolveSubject("25", ""); err != nil || s != "pr:25" {
		t.Fatalf("resolveSubject(pr=25) = (%q,%v), want pr:25", s, err)
	}
	if s, err := resolveSubject("", "commit:abc"); err != nil || s != "commit:abc" {
		t.Fatalf("resolveSubject(subject) = (%q,%v), want commit:abc", s, err)
	}
	if _, err := resolveSubject("25", "commit:abc"); err == nil {
		t.Error("resolveSubject with both --pr and --subject should error")
	}
	if _, err := resolveSubject("", ""); err == nil {
		t.Error("resolveSubject with neither should error")
	}
}

func TestRollUp(t *testing.T) {
	mk := func(reviewer, author, decision string) bus.Verdict {
		return bus.Verdict{Reviewer: reviewer, Author: author, Decision: decision}
	}
	cases := []struct {
		name    string
		vs      []bus.Verdict
		want    string
		wantIdx int
	}{
		{"independent approve",
			[]bus.Verdict{mk("claude2", "claude1", "approve")},
			"APPROVED", 0},
		{"self approve only",
			[]bus.Verdict{mk("claude1", "claude1", "approve")},
			"PENDING", -1},
		{"reject after approve",
			[]bus.Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "reject")},
			"REJECTED", 1},
		{"reject then approve",
			[]bus.Verdict{mk("claude3", "claude1", "reject"), mk("claude2", "claude1", "approve")},
			"APPROVED", 1},
		{"empty", nil, "PENDING", -1},
		// Two independent approves: APPROVED, deciderIdx = index of the LATEST approve.
		{"two independent approves",
			[]bus.Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "approve")},
			"APPROVED", 1},
		// Multiple independent approves, then a reject: REJECTED.
		{"multiple approves then reject",
			[]bus.Verdict{
				mk("claude2", "claude1", "approve"),
				mk("claude3", "claude1", "approve"),
				mk("claude4", "claude1", "reject"),
			},
			"REJECTED", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, gotIdx := rollUp(c.vs)
			if got != c.want {
				t.Errorf("state = %q, want %q", got, c.want)
			}
			if gotIdx != c.wantIdx {
				t.Errorf("deciderIdx = %d, want %d", gotIdx, c.wantIdx)
			}
		})
	}
}

func TestVerdictsOverview(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	ts := func(secAgo int) int64 { return now.Add(-time.Duration(secAgo) * time.Second).UnixMilli() }
	vs := []bus.Verdict{
		{Subject: "pr:10", Reviewer: "claude2", Author: "claude1", Decision: "approve", Message: "looks good", TS: ts(120)},
		{Subject: "pr:11", Reviewer: "claude3", Author: "claude1", Decision: "reject", TS: ts(60)},
	}
	out := verdictsOverview(vs, now)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	// Line with message must contain the quoted message.
	if !strings.Contains(lines[0], `"looks good"`) {
		t.Errorf("with-message line missing quoted message: %q", lines[0])
	}
	// Line without message must NOT contain empty-string literal.
	if strings.Contains(lines[1], `""`) {
		t.Errorf("no-message line contains unwanted %q literal: %q", `""`, lines[1])
	}
}

func TestVerdictsReport(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	ts := func(secAgo int) int64 { return now.Add(-time.Duration(secAgo) * time.Second).UnixMilli() }
	vs := []bus.Verdict{
		{Subject: "pr:25", Reviewer: "claude3", Author: "claude1", Decision: "reject", Message: "typo", TS: ts(120)},
		{Subject: "pr:25", Reviewer: "claude2", Author: "claude1", Decision: "approve", Message: "LGTM", TS: ts(60)},
	}
	out, code := verdictsReport("pr:25", vs, now)
	if code != 0 {
		t.Fatalf("APPROVED should exit 0, got %d", code)
	}
	if !strings.Contains(out, "APPROVED") || !strings.Contains(out, "claude2") {
		t.Fatalf("report missing approved header: %q", out)
	}
	if !strings.Contains(out, "(superseded)") {
		t.Fatalf("older reject should be marked superseded: %q", out)
	}
	selfVs := []bus.Verdict{
		{Subject: "pr:26", Reviewer: "claude1", Author: "claude1", Decision: "approve", TS: ts(10)},
	}
	out2, code2 := verdictsReport("pr:26", selfVs, now)
	if code2 != 2 {
		t.Fatalf("PENDING should exit 2, got %d", code2)
	}
	if !strings.Contains(out2, "ignored") {
		t.Fatalf("self-approval should be annotated ignored: %q", out2)
	}
}
