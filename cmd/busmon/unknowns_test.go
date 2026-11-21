package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// project-switch-ux item 2: what is unknown because the dispatcher does not
// run says so (it computes health, the quota readings and the merge queue),
// instead of a bare "?" or "not computed yet".
func TestUnknownWhy(t *testing.T) {
	for state, want := range map[string]string{
		"stopped": "project stopped", "down": "dispatch down", "failed": "dispatch failed", "gave-up": "dispatch gave-up",
		"up": "", "ready": "", "": "", "standby": "",
	} {
		if got := unknownWhy(state); got != want {
			t.Errorf("%q: %q, want %q", state, got, want)
		}
	}
}

func TestMergeUnknownSaysWhy(t *testing.T) {
	s := snapshot{dispatch: "stopped", mergeQRead: true}
	items := mergeItems(s)
	if len(items) != 1 || items[0].Text != "merge queue unknown: project stopped" {
		t.Fatalf("stopped: %+v", items)
	}
	s.dispatch = "up"
	if items := mergeItems(s); len(items) != 1 || !strings.Contains(items[0].Text, "not computed yet") {
		t.Fatalf("running: %+v", items)
	}
}

func TestHealthUnknownSaysWhy(t *testing.T) {
	s := guardianSummary{Total: 5, Unmeasured: 5}
	if got := guardianSegWhy(s, 140, "dispatch down").text(); got != "health ?5 dispatch down" {
		t.Fatalf("wide: %q", got)
	}
	if got := guardianSegWhy(s, 60, "dispatch down").text(); got != "?5" {
		t.Fatalf("narrow keeps the short form: %q", got)
	}
	if got := guardianSegWhy(guardianSummary{Total: 5, ToDo: 1}, 140, "dispatch down").text(); strings.Contains(got, "dispatch down") {
		t.Fatalf("a measured problem is not explained away: %q", got)
	}
}

func TestQuotaUnknownSaysWhyOnce(t *testing.T) {
	now := time.Now()
	d := headerData{project: "demo", now: now, dispatch: "down",
		budgets: map[string]bus.BudgetSnapshot{"claude": {Unknown: true}, "grok": {Unknown: true}}}
	text := strings.Join(lineTexts(headerRows(d, 160, TierWide, 2)), "\n")
	if strings.Count(text, "(dispatch down)") != 1 {
		t.Fatalf("want one reason:\n%s", text)
	}
	d.dispatch = "up"
	if text := strings.Join(lineTexts(headerRows(d, 160, TierWide, 2)), "\n"); strings.Contains(text, "dispatch down)") {
		t.Fatalf("running, yet:\n%s", text)
	}
}

func lineTexts(ls []Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.text()
	}
	return out
}

// A provider with a reading is not unknown: no reason is added for it, even
// while the dispatcher is down.
func TestQuotaReasonOnlyForUnknown(t *testing.T) {
	now := time.Now()
	read := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 40, ObservedAt: now.Unix(), ResetsAt: now.Add(time.Hour).Unix()}}}
	d := headerData{project: "demo", now: now, dispatch: "down", budgets: map[string]bus.BudgetSnapshot{"claude": read}}
	if text := strings.Join(lineTexts(headerRows(d, 160, TierWide, 2)), "\n"); strings.Contains(text, "(dispatch down)") {
		t.Fatalf("a read provider explained away:\n%s", text)
	}
	for name, c := range map[string]struct {
		b    bus.BudgetSnapshot
		want bool
	}{
		"read":       {read, false},
		"unknown":    {bus.BudgetSnapshot{Unknown: true}, true},
		"no windows": {bus.BudgetSnapshot{}, true},
		"unobserved": {bus.BudgetSnapshot{Windows: []bus.BudgetWindow{{Name: "5h", ResetsAt: now.Add(time.Hour).Unix()}}}, true},
		"unlimited":  {bus.BudgetSnapshot{Unlimited: true}, false},
	} {
		if got := quotaAllUnknown(c.b, now); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
