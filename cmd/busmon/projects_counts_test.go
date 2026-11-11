package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
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

// reviewer-codex: a read that fails while the broker answers is no 0 either.
// The Agents read failing would count no agent at work, the Board's no
// request: either is ?, as any failed read of the project's poll.
func TestReadProjectCountsPartialReadIsUnknown(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	snaps := map[string]bus.AgentSnapshot{"coder": {State: "working", TS: now.UnixMilli()}}
	for name, f := range map[string]*fakeReader{
		"agents":  {calls: map[string]int{}, snaps: snaps, agentsErr: errors.New("timeout")},
		"board":   {calls: map[string]int{}, snaps: snaps, boardErr: errors.New("timeout")},
		"asks":    {calls: map[string]int{}, snaps: snaps, asksErr: errors.New("timeout")},
		"gates":   {calls: map[string]int{}, snaps: snaps, gatesErr: errors.New("timeout")},
		"history": {calls: map[string]int{}, snaps: snaps, recentErr: errors.New("timeout")}, // the threads owed to the person
	} {
		if c, err := readProjectCounts(context.Background(), f, "charlie", now); err == nil {
			t.Errorf("%s read failed: counts %+v, want an error (?)", name, c)
		}
	}
}

// The gates are read per agent, of the agents the poll knows: the count's
// poll reads them for the agents it has just read, so a review gate held
// by the person is in YOU.
func TestReadProjectCountsReadsTheGates(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{"coder": {State: "working", TS: now.UnixMilli()}},
		gates: map[string]map[string]string{"coder": {"1799999990000-0": "charlie|look at b1"}}}
	c, err := readProjectCounts(context.Background(), f, "charlie", now)
	if err != nil || c.you != 1 {
		t.Fatalf("counts %+v err %v, want the person's gate in YOU", c, err)
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

// On a real broker, a healthy project's reads all succeed (no consumer
// group, no plan, no merge queue are no failures): its counts are numbers.
func TestReadProjectCountsRealBroker(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := "pc" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		c.Del(ctx, keys...)
	})
	b, err := bus.Open(c, p)
	if err != nil {
		t.Fatal(err)
	}
	for agent, state := range map[string]string{"coder": "working", "kimi": "idle"} {
		if _, err := b.Status(ctx, agent, state, "", bus.AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	opt := []bus.AskOption{{Label: "A"}, {Label: "B"}}
	if _, _, err := b.Ask(ctx, bus.AskDef{From: "master", Title: "pick", Questions: []bus.AskQuestion{{ID: "q", Header: "Q", Question: "which?", Options: opt}}}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := readProjectCounts(ctx, b, "charlie", time.Now())
	if err != nil || got != (workCounts{you: 1, team: 1}) {
		t.Fatalf("counts %+v err %v, want 1 for you (the ask), 1 at work (coder)", got, err)
	}
}

// The recording reader of a *bus.Bus keeps the plan, merge queue and health
// readers: For you's merge and check rows and the plan's rows come from
// them, and the poll finds them by type.
func TestRecordingKeepsTheCountsReaders(t *testing.T) {
	r, _ := recording(&bus.Bus{})
	if _, ok := r.(planReader); !ok {
		t.Error("no plan reader")
	}
	if _, ok := r.(mergeQueueReader); !ok {
		t.Error("no merge queue reader")
	}
	if _, ok := r.(guardianReader); !ok {
		t.Error("no health reader")
	}
}
