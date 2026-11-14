package mergequeue

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

const (
	hA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hC = "cccccccccccccccccccccccccccccccccccccccc"
	hD = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func v(id, subject, reviewer, decision, head string) bus.Verdict {
	return bus.Verdict{ID: id, Subject: subject, Author: "coder", Reviewer: reviewer, Decision: decision, Head: head, Message: reviewer + " checked " + head[:7], TS: 1000}
}

func heads(items []bus.MergeItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Head)
	}
	return out
}

func TestDeriveForge(t *testing.T) {
	ledger := []bus.Verdict{
		v("1-0", "feat-a", "foureyes", "approve", hA),
		v("2-0", "feat-a", "reviewer-codex", "approve", hA),
		v("3-0", "feat-b", "foureyes", "approve", hB), // B then pushed past: the PR's head is C
		v("4-0", "feat-r", "foureyes", "approve", hC),
		v("5-0", "feat-r", "reviewer-codex", "reject", hC), // rejected after the approve
		v("6-0", "feat-d", "foureyes", "approve", hD),      // the draft's own approved head
	}
	prs := []PR{
		{Number: 1, Title: "A", URL: "https://x/1", Branch: "feat/a", Head: hA},
		{Number: 2, Title: "B", URL: "https://x/2", Branch: "feat/b", Head: hC}, // newer push, and C is rejected
		{Number: 3, Title: "draft", Branch: "feat/d", Head: hD, Draft: true},    // approved, still a draft: neither queued nor counted
		{Number: 4, Title: "unreviewed", Branch: "feat/u", Head: "dddddddddddddddddddddddddddddddddddddddd"},
	}
	got := Derive(Input{Verdicts: ledger, PRs: prs})
	if len(got.Items) != 1 {
		t.Fatalf("items %+v", got.Items)
	}
	it := got.Items[0]
	if it.Number != 1 || it.Head != hA || it.Branch != "feat/a" || it.Title != "A" || it.URL != "https://x/1" {
		t.Fatalf("item %+v", it)
	}
	if len(it.Subjects) != 1 || it.Subjects[0] != "feat-a" || len(it.Approvals) != 2 ||
		it.Approvals[0].Reviewer != "foureyes" || it.Approvals[1].Reviewer != "reviewer-codex" {
		t.Fatalf("subjects %v approvals %+v", it.Subjects, it.Approvals)
	}
	if got.Unapproved != 2 { // #2 (newer push onto a rejected head) and #4 (no review); the draft is not counted
		t.Fatalf("unapproved %d", got.Unapproved)
	}
}

func TestDeriveTwoSubjectsOneHead(t *testing.T) {
	got := Derive(Input{
		Verdicts: []bus.Verdict{v("1-0", "spec", "foureyes", "approve", hA), v("2-0", "impl", "reviewer-codex", "approve", hA)},
		PRs:      []PR{{Number: 7, Branch: "feat/x", Head: hA}},
	})
	if len(got.Items) != 1 || len(got.Items[0].Subjects) != 2 || got.Items[0].Subjects[0] != "impl" || got.Items[0].Subjects[1] != "spec" {
		t.Fatalf("%+v", got.Items)
	}
}

func TestDeriveNoHeadMatchesNothing(t *testing.T) {
	got := Derive(Input{
		Verdicts: []bus.Verdict{{ID: "1-0", Subject: "s", Author: "coder", Reviewer: "foureyes", Decision: "approve"}},
		PRs:      []PR{{Number: 7, Branch: "feat/x", Head: ""}, {Number: 8, Branch: "feat/y", Head: hA}},
	})
	if len(got.Items) != 0 {
		t.Fatalf("%+v", got.Items)
	}
}

// A late approve on an older head becomes the decider: the delivered PR leaves
// the queue, and is counted (never silent).
func TestDeriveLateApproveOnOlderHead(t *testing.T) {
	got := Derive(Input{
		Verdicts: []bus.Verdict{v("1-0", "s", "foureyes", "approve", hB), v("2-0", "s", "reviewer-codex", "approve", hA)},
		PRs:      []PR{{Number: 9, Branch: "feat/s", Head: hB}},
	})
	if len(got.Items) != 0 || got.Unapproved != 1 {
		t.Fatalf("%+v", got)
	}
}

// git fallback: a branch tip at an approved head, not merged; a branch moved
// past it, a merged one and an approved head with no branch are no items.
func TestDeriveTips(t *testing.T) {
	merged := map[string]bool{hC: true}
	got := Derive(Input{
		Verdicts: []bus.Verdict{v("1-0", "a", "foureyes", "approve", hA), v("2-0", "b", "foureyes", "approve", hB),
			v("3-0", "c", "foureyes", "approve", hC)},
		Tips:   []Tip{{Branch: "feat/a", Head: hA}, {Branch: "feat/b", Head: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}, {Branch: "feat/c", Head: hC}},
		Merged: func(h string) bool { return merged[h] },
	})
	if hs := heads(got.Items); len(hs) != 1 || hs[0] != hA || got.Items[0].Branch != "feat/a" || got.Items[0].Number != 0 {
		t.Fatalf("%+v", got.Items)
	}
	if got.Unapproved != 0 {
		t.Fatalf("tips count no unapproved: %d", got.Unapproved)
	}
}

// Items come oldest approved first.
func TestDeriveOrder(t *testing.T) {
	a, b := v("5-0", "a", "foureyes", "approve", hA), v("3-0", "b", "foureyes", "approve", hB)
	a.TS, b.TS = 5000, 3000
	got := Derive(Input{Verdicts: []bus.Verdict{b, a}, PRs: []PR{{Number: 1, Head: hA}, {Number: 2, Head: hB}}})
	if len(got.Items) != 2 || got.Items[0].Number != 2 || got.Items[0].ApprovedAt != 3000 {
		t.Fatalf("%+v", got.Items)
	}
}

// The person: a PR that conflicts with its base cannot be merged, so it is
// no "merge PR" row; it is listed apart, for master's rebase. A closed or
// merged PR is not in the forge's open list (ReadGitHub asks --state open),
// so it is never an item. GitHub's UNKNOWN (not computed yet) stays an item.
func TestDeriveConflictingPR(t *testing.T) {
	ledger := []bus.Verdict{
		v("1-0", "feat-a", "foureyes", "approve", hA),
		v("2-0", "feat-b", "foureyes", "approve", hB),
		v("3-0", "feat-c", "foureyes", "approve", hC),
	}
	prs := []PR{
		{Number: 500, Branch: "feat/a", Head: hA, Base: "release", Conflicting: true}, // its own base, not delivery.base (reviewer-codex)
		{Number: 501, Branch: "feat/b", Head: hB},
		{Number: 502, Branch: "feat/c", Head: hC},                    // mergeable not known yet
		{Number: 503, Branch: "feat/u", Head: hD, Conflicting: true}, // unapproved: counted there, not here
	}
	got := Derive(Input{Verdicts: ledger, PRs: prs})
	if len(got.Items) != 2 || got.Items[0].Number != 501 || got.Items[1].Number != 502 {
		t.Fatalf("items %+v", got.Items)
	}
	if len(got.Conflicting) != 1 || got.Conflicting[0] != (bus.MergeConflict{Number: 500, Base: "release"}) || got.Unapproved != 1 {
		t.Fatalf("conflicting %v unapproved %d", got.Conflicting, got.Unapproved)
	}
}
