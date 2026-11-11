package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// One counter (correction 3): the Projects page's two numbers are the Work
// model's, by the same rule as the header: YOU's rows and TEAM's agents.
func TestWorkCountsOfIsTheWorkModels(t *testing.T) {
	pinLocal(t)
	m := workShell(t, 140, 40)
	got := workCountsOf(m.snap, "charlie", shellNow)
	if want := (workCounts{you: m.work.youCount(), team: len(m.work.Team)}); got != want || want.you == 0 || want.team == 0 {
		t.Fatalf("counts %+v, the Work model's %+v", got, want)
	}
}

// Another project's counts: one read of its bus, the poll's, then the same
// rule; a broker down is an error, never 0.
func TestReadProjectCounts(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	ask := demoAsk()
	ask.CreatedAt = now.Add(-time.Minute).UnixMilli()
	f := &fakeReader{calls: map[string]int{}, asks: []bus.Ask{ask},
		snaps: map[string]bus.AgentSnapshot{"coder": {State: "working", TS: now.UnixMilli()}, "kimi": {State: "idle", TS: now.UnixMilli()}}}
	c, err := readProjectCounts(context.Background(), f, "charlie", now)
	if err != nil || c != (workCounts{you: 1, team: 1}) {
		t.Fatalf("counts %+v err %v, want 1 for you (the ask), 1 at work (coder)", c, err)
	}
	if _, err := readProjectCounts(context.Background(), &fakeReader{calls: map[string]int{}, fail: true}, "charlie", now); err == nil {
		t.Fatal("a broker down read as counts")
	}
}

// gatherAdmin reads the counts of every broker project after the rest; one
// that fails is unknown (?), alone.
func TestGatherAdminCounts(t *testing.T) {
	src := adminFixture(shellNow)
	src.counts = func(_ context.Context, p string) (workCounts, error) {
		if p == "old" {
			return workCounts{}, errors.New("timeout")
		}
		return workCounts{you: len(p), team: 1}, nil
	}
	v := gatherAdmin(context.Background(), src, "agent-bus-monitor", shellNow)
	if r := rowNamed(t, v, "demo"); r.counts == nil || *r.counts != (workCounts{you: 4, team: 1}) {
		t.Errorf("demo: %+v", r.counts)
	}
	if r := rowNamed(t, v, "old"); r.counts != nil {
		t.Errorf("old: a failed read gave %+v", r.counts)
	}
	if r := rowNamed(t, v, "newone"); r.counts != nil {
		t.Errorf("a template only was read: %+v", r.counts)
	}
}

// The cells: ? when unknown or no longer fresh (the person's answer to Q3),
// — for a template only, the open project's live counts.
func TestProjectsCountCells(t *testing.T) {
	pinLocal(t)
	src := adminFixture(shellNow)
	src.counts = func(_ context.Context, p string) (workCounts, error) {
		if p == "old" {
			return workCounts{}, errors.New("timeout")
		}
		return workCounts{you: 2, team: 3}, nil
	}
	v := gatherAdmin(context.Background(), src, "agent-bus-monitor", shellNow)
	live := &workCounts{you: 5, team: 4}
	cell := func(v adminView, now time.Time, name string) string {
		r := rowNamed(t, withLiveCounts(v, live), name)
		return adminForYou(r, now) + "/" + adminAtWork(r, now)
	}
	for name, want := range map[string]string{"agent-bus-monitor": "5/4", "demo": "2/3", "old": "?/?", "newone": GlyphDash + "/" + GlyphDash} {
		if got := cell(v, shellNow, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	later := shellNow.Add(countsFresh + time.Second)
	if got := cell(v, later, "demo"); got != "?/?" {
		t.Errorf("demo read %s ago: %q, want ?/?", countsFresh+time.Second, got)
	}
	if got := cell(v, later, "agent-bus-monitor"); got != "5/4" {
		t.Errorf("the open project is live: %q", got)
	}
	if got := rowNamed(t, withLiveCounts(v, nil), "agent-bus-monitor"); got.counts != nil {
		t.Errorf("busmon's link down: the open row %+v, want ?", got.counts)
	}
	wide, _ := projectsBody(withLiveCounts(v, live), 0, TierWide, 130, shellNow, "")
	medium, _ := projectsBody(withLiveCounts(v, live), 0, TierMedium, 100, shellNow, "")
	narrow, _ := projectsBody(withLiveCounts(v, live), 0, TierNarrow, 60, shellNow, "")
	for name, body := range map[string][]Line{"wide": wide, "medium": medium} {
		text := tokLinesText(body)
		if !strings.Contains(text, "FOR YOU") || !strings.Contains(text, "AT WORK") {
			t.Errorf("%s: no count columns:\n%s", name, text)
		}
	}
	if text := tokLinesText(narrow); !strings.Contains(text, "5 for you "+GlyphNone+" 4 at work") {
		t.Errorf("narrow: no counts:\n%s", text)
	}
	stale, _ := projectsBody(withLiveCounts(v, live), 0, TierWide, 130, later, "")
	if text := tokLinesText(stale); !strings.Contains(text, "R reads them again") {
		t.Errorf("stale counts without a note:\n%s", text)
	}
}
