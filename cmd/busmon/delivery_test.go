package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
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

// A merge item's page ends with deliver's one-approval trace for its head
// and the provider warning; with neither, nothing is added.
func TestMergePageDeliveryLines(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	snap := m.snap
	snap.mergeQ, snap.mergeQRead = mqRecord(shellNow), true
	snap.delivery = deliveryFacts{Warning: "no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)",
		Traces: map[string]string{mqH: "single approval (small: 4 lines, 1 files; approved by foureyes) (pr 309)"}}
	m.snap = snap
	m.refresh()
	m.push(page{kind: ovMerge, key: mqH})
	text := plainLines(m.rd.top().body)
	for _, want := range []string{"Delivered on one approval: single approval (small: 4 lines, 1 files; approved by foureyes) (pr 309)",
		"no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("page lacks %q:\n%s", want, text)
		}
	}
	snap.delivery = deliveryFacts{}
	m.snap = snap
	m.closeReader()
	m.push(page{kind: ovMerge, key: mqH})
	if text := plainLines(m.rd.top().body); strings.Contains(text, "one approval") {
		t.Fatalf("lines without facts:\n%s", text)
	}
}

// Shift+C's delivery rows are the project's values (§3.1: [project.<p>]
// first, then the machine's, same_provider_ok the project's only), all
// read-only; same_provider_ok says "no effect" while single_approval is off;
// another project's table never shows.
func TestProjectDeliveryRows(t *testing.T) {
	cfg := "[delivery]\nsmall_max_lines = 200\nsame_provider_ok = \"small\"\n\n[project.demo.delivery]\nsingle_approval = \"small\"\nsame_provider_ok = \"docs\"\n\n[project.other.delivery]\nsmall_max_files = 9\n"
	getenv := settingsEnv(t, cfg, "", nil)
	rows := func(project string) map[string]settingRow {
		out := map[string]settingRow{}
		for _, g := range projectDeliveryRows(settingsGroups(getenv, settingsFlags{}, []config.Group{config.GroupServices}), getenv, project) {
			for _, r := range g.Rows {
				if strings.HasPrefix(r.Key, "delivery.") {
					out[r.Key] = r
				}
			}
		}
		return out
	}
	demo := rows("demo")
	for key, want := range map[string]struct{ value, source string }{
		"delivery.single_approval":  {"small", "config.toml [project.demo]"},
		"delivery.small_max_lines":  {"200", "config.toml"},
		"delivery.small_max_files":  {"5", "default"},
		"delivery.never_small":      {"none", "default"},
		"delivery.same_provider_ok": {"docs", "config.toml [project.demo]"},
	} {
		r, ok := demo[key]
		if !ok || r.Value != want.value || r.Source != want.source || r.Editable || !strings.Contains(settingHelp(r), "[project.demo.delivery]") {
			t.Errorf("%s: %+v (help %q)", key, r, settingHelp(r))
		}
	}
	if h := settingHelp(demo["delivery.same_provider_ok"]); strings.Contains(h, "no effect") {
		t.Errorf("single_approval is small, yet %q", h)
	}
	quiet := rows("quiet")
	if r := quiet["delivery.same_provider_ok"]; r.Value != "off" || !strings.Contains(settingHelp(r), "no effect: delivery.single_approval is off") {
		t.Errorf("quiet same_provider_ok: %+v %q", r, settingHelp(r))
	}
	if r := quiet["delivery.small_max_files"]; r.Value != "5" {
		t.Errorf("another project's value leaked: %+v", r)
	}
}

// The provider warning is a line under a selected delivery row of Shift+C
// (§4.8), and under no other row.
func TestSettingsDeliveryWarning(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	set := &settingsPage{groups: projectDeliveryRows(settingsGroups(getenv, settingsFlags{}, settingsPageGroups), getenv, "demo"), loaded: true, keys: true,
		deliveryWarning: "no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)"}
	for gi, g := range set.groups {
		if g.Group != config.GroupServices {
			continue
		}
		set.group = gi
		for ri, r := range g.Rows {
			text := plainLines(settingsBody(set, ri, 160))
			if got := strings.Contains(text, "no reviewer of another provider in 7 days"); got != strings.HasPrefix(r.Key, "delivery.") {
				t.Errorf("%s: warning shown %v:\n%s", r.Key, got, text)
			}
		}
		return
	}
	t.Fatal("no Services group")
}
