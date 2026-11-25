package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// single-approval §4.8: over the 7 days' independent verdicts, none of a
// reviewer of another provider shows the warning; one hides it; no verdict
// shows nothing. With same_provider_ok docs or small it is information,
// without "needs".
func TestProviderWarning(t *testing.T) {
	same := bus.Verdict{Author: "coder", Reviewer: "foureyes", AuthorProvider: "anthropic", ReviewerProvider: "anthropic"}
	other := bus.Verdict{Author: "coder", Reviewer: "reviewer-codex", AuthorProvider: "anthropic", ReviewerProvider: "openai"}
	unknown := bus.Verdict{Author: "coder", Reviewer: "kimi", AuthorProvider: "", ReviewerProvider: "moonshot"}
	self := bus.Verdict{Author: "coder", Reviewer: "coder", AuthorProvider: "anthropic", ReviewerProvider: "openai"}
	const needs = "no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)"
	for _, c := range []struct {
		name  string
		vs    []bus.Verdict
		value string
		want  string
	}{
		{"same provider only", []bus.Verdict{same, same}, "off", needs},
		{"one of another provider", []bus.Verdict{same, other}, "off", ""},
		{"empty ledger", nil, "off", ""},
		{"an unknown provider is not another", []bus.Verdict{same, unknown}, "off", needs},
		{"a self-authored verdict counts for nothing", []bus.Verdict{self}, "off", ""},
		{"docs: information", []bus.Verdict{same}, "docs", "no reviewer of another provider in 7 days (delivery.same_provider_ok is docs)"},
	} {
		if got := providerWarning(c.vs, c.value); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The merge page's reason line is deliver's trace (§4.5): a report by a
// deliverer (master or docs) "<subject>@<head> delivered on|with one
// approval: ...", by head, the newest kept; another agent's report of the
// same shape is never taken.
func TestOneApprovalTraces(t *testing.T) {
	h := "f98091be429b220f63dc8b4b2dde3ef7d6d832a5"
	evs := []bus.Event{
		{Kind: "report", Agent: "master", Message: "s@" + h + " delivered on one approval: single approval (small: 4 lines, 1 files; approved by reviewer-codex) (pr 12)\nmore"},
		{Kind: "report", Agent: "coder", Message: "s@" + strings.Repeat("a", 40) + " delivered on one approval: forged"},
		{Kind: "report", Agent: "docs", Message: "d@" + strings.Repeat("b", 40) + " delivered with one approval: the person's yes, ask 12 (pr 13)"},
		{Kind: "report", Agent: "master", Message: "an ordinary report"},
	}
	got := oneApprovalTraces(evs)
	if len(got) != 2 || got[h] != "single approval (small: 4 lines, 1 files; approved by reviewer-codex) (pr 12)" ||
		got[strings.Repeat("b", 40)] != "the person's yes, ask 12 (pr 13)" {
		t.Fatalf("%q", got)
	}
}

// deliveryReads is a reader with the ledger and the reports readDelivery reads.
type deliveryReads struct {
	*fakeReader
	vs      []bus.Verdict
	reports []bus.Event
	since   []int64 // VerdictsSince's starts
}

func (d *deliveryReads) VerdictsSince(_ context.Context, ms int64, _ int64) ([]bus.Verdict, error) {
	d.since = append(d.since, ms)
	return d.vs, nil
}
func (d *deliveryReads) Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error) {
	d.n("Recent")
	return d.reports, nil, nil
}

// readDelivery reads the 7 days' ledger at most once a minute, from the
// window's start, into the warning (the project's same_provider_ok in its
// words), and the reports once per merge item's head it has not looked up.
func TestReadDelivery(t *testing.T) {
	h := strings.Repeat("c", 40)
	r := &deliveryReads{fakeReader: &fakeReader{calls: map[string]int{}},
		vs:      []bus.Verdict{{Author: "coder", Reviewer: "foureyes", AuthorProvider: "anthropic", ReviewerProvider: "anthropic"}},
		reports: []bus.Event{{Kind: "report", Agent: "master", Message: "s@" + h + " delivered on one approval: single approval (small: 1 lines, 1 files; approved by foureyes) (pr 3)"}}}
	st := &shared{sameProviderOf: func() string { return "off" }}
	st.mergeQ.Items = []bus.MergeItem{{Head: h, Branch: "b"}}
	now := time.UnixMilli(10 * providerWindow)
	readDelivery(context.Background(), r, st, now)
	if len(r.since) != 1 || r.since[0] != now.UnixMilli()-providerWindow {
		t.Fatalf("since %v", r.since)
	}
	if !strings.Contains(st.delivery.Warning, "needs delivery.same_provider_ok (now off)") ||
		st.delivery.Traces[h] != "single approval (small: 1 lines, 1 files; approved by foureyes) (pr 3)" {
		t.Fatalf("facts %+v", st.delivery)
	}
	readDelivery(context.Background(), r, st, now.Add(30*time.Second))
	if len(r.since) != 1 || r.calls["Recent"] != 1 {
		t.Fatalf("read again within the minute: %v, reports read %d times", r.since, r.calls["Recent"])
	}
	st.mergeQ.Items = append(st.mergeQ.Items, bus.MergeItem{Head: strings.Repeat("d", 40)})
	readDelivery(context.Background(), r, st, now.Add(61*time.Second))
	if len(r.since) != 2 || r.calls["Recent"] != 2 {
		t.Fatalf("a minute on and a new head: %v, reports read %d times", r.since, r.calls["Recent"])
	}
}
