package main

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// project-switch-ux item 3: the header counts what waits in the other
// projects of this broker, read every othersEvery from the projects whose
// dispatch runs (master: a stopped project is read on M and R only).

func TestWorkCountsMerge(t *testing.T) {
	s := snapshot{mergeQRead: true, mergeQ: bus.MergeQueueRecord{ItemsAt: 1, Items: []bus.MergeItem{{Branch: "a", Head: "h1"}, {Branch: "b", Head: "h2"}}}}
	if c := workCountsOf(s, "charlie", time.Now()); c.merge != 2 || c.you < 2 {
		t.Fatalf("%+v", c)
	}
	s = snapshot{mergeQRead: true} // never computed: an unknown row, not a merge
	if c := workCountsOf(s, "charlie", time.Now()); c.merge != 0 {
		t.Fatalf("unknown queue: %+v", c)
	}
}

func TestOthersSummary(t *testing.T) {
	now := time.Now()
	fresh := func(you, merge int) otherRead {
		return otherRead{counts: workCounts{you: you, merge: merge}, at: now.Add(-time.Minute)}
	}
	for _, c := range []struct {
		reads map[string]otherRead
		want  string
	}{
		{map[string]otherRead{"a": fresh(3, 1), "b": fresh(0, 0)}, "other: 2 for you · 1 merge"},
		{map[string]otherRead{"a": fresh(1, 1)}, "other: 1 merge"},
		{map[string]otherRead{"a": fresh(2, 0)}, "other: 2 for you"},
		{map[string]otherRead{"a": fresh(0, 0)}, ""},
		{map[string]otherRead{"a": {counts: workCounts{you: 5}, at: now.Add(-othersFresh - time.Second)}}, ""}, // stale: out of the sum
		{map[string]otherRead{"demo": fresh(4, 0)}, ""},                                                        // the open project is not other
	} {
		if got := othersSummary(c.reads, "demo", now); got != c.want {
			t.Errorf("%v: %q, want %q", c.reads, got, c.want)
		}
	}
}

// Only a project whose dispatch runs is read; one whose liveness or counts
// cannot be read drops out, never 0; the open project is skipped.
func TestReadOtherCounts(t *testing.T) {
	var mu sync.Mutex
	var counted []string
	alive := func(_ context.Context, p string) (bool, error) {
		switch p {
		case "run", "fails":
			return true, nil
		case "dead":
			return false, nil
		}
		return false, errors.New("unreadable")
	}
	counts := func(_ context.Context, p string) (workCounts, error) {
		mu.Lock()
		counted = append(counted, p)
		mu.Unlock()
		if p == "fails" {
			return workCounts{}, errors.New("read failed")
		}
		return workCounts{you: 2}, nil
	}
	now := time.Now()
	got := readOtherCounts(context.Background(), []string{"demo", "run", "dead", "fails", "odd"}, "demo", alive, counts, now)
	slices.Sort(counted)
	if len(got) != 1 || got["run"].counts.you != 2 || !got["run"].at.Equal(now) || !slices.Equal(counted, []string{"fails", "run"}) {
		t.Fatalf("got %+v, counted %v", got, counted)
	}
}

// The header shows the sum; O opens the other project with the most for you,
// by the checked switch.
func TestOthersHeaderAndKey(t *testing.T) {
	m := switchShell(t)
	now := time.Now()
	m.others = map[string]otherRead{"trading": {counts: workCounts{you: 3}, at: now}, "busdev": {counts: workCounts{you: 1}, at: now}}
	m.refresh()
	if !strings.Contains(m.grid().plain(), "other: 4 for you") {
		t.Fatalf("header:\n%s", strings.Join(strings.Split(m.grid().plain(), "\n")[:2], "\n"))
	}
	m, cmd := pressCmd(t, m, 'O')
	m, quit := runQuits(t, m, cmd)
	if !quit || m.switchTo != "trading" {
		t.Fatalf("O: quit %v switchTo %q flash %q", quit, m.switchTo, m.ui.flash)
	}
	m2 := switchShell(t)
	m2, cmd = pressCmd(t, m2, 'O')
	if _, quit := runQuits(t, m2, cmd); quit || !strings.Contains(m2.ui.flash, "nothing waits in the other projects") {
		t.Fatalf("O with nothing: %q", m2.ui.flash)
	}
}

// othersShell is a shell whose other: sources are fakes: two projects on
// the broker, one running.
func othersShell(t *testing.T) Model {
	t.Helper()
	src := adminFixture(shellNow)
	src.alive = func(_ context.Context, p string) (bool, error) { return p == "trading", nil }
	src.counts = func(context.Context, string) (workCounts, error) { return workCounts{you: 2}, nil }
	return shellWith(t, 140, 40, shellConfig{admin: &src,
		listProjects: func(context.Context) ([]string, error) {
			return []string{"agent-bus-monitor", "trading", "busdev"}, nil
		}}, false)
}

// The read starts with busmon (foureyes: dropping it from Init passed every
// test), and the tick reads again.
func TestOthersReadAtStartAndOnTick(t *testing.T) {
	m := othersShell(t)
	var got *othersMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		var msg tea.Msg
		func() {
			defer func() { _ = recover() }() // the poll, which this fixture cannot run: not what is checked here
			msg = c()
		}()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, x := range msg {
				walk(x)
			}
		case othersMsg:
			got = &msg
		}
	}
	walk(m.othersCmd()) // the command Init batches
	if got == nil || len(got.reads) != 1 || got.reads["trading"].counts.you != 2 {
		t.Fatalf("othersCmd: %+v", got)
	}
	got = nil
	walk(m.Init())
	if got == nil {
		t.Fatal("Init does not read the other projects")
	}
	got = nil
	walk(m.update(othersTickMsg{}))
	if got == nil {
		t.Fatal("the tick does not read again")
	}
}

// Each read arms the next tick (foureyes: gotOthers returning nil passed).
func TestOthersRearm(t *testing.T) {
	m := othersShell(t)
	if cmd := m.gotOthers(othersMsg{}); cmd == nil {
		t.Fatal("no next read armed")
	}
}

// The production source reads only running projects (foureyes): alive is
// the dispatch lease, on this broker.
func TestOthersAliveIsTheLease(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	up, down := "oa"+strconv.FormatInt(time.Now().UnixNano(), 36), "ob"+strconv.FormatInt(time.Now().UnixNano(), 36)
	c.Set(ctx, bus.DispatchKey(up), "tok", time.Minute)
	t.Cleanup(func() { c.Del(context.Background(), bus.DispatchKey(up)) })
	src := adminSourceFor(c, func(string) string { return "" }, "charlie")
	if ok, err := src.alive(ctx, up); err != nil || !ok {
		t.Fatalf("a held lease: %v %v", ok, err)
	}
	if ok, err := src.alive(ctx, down); err != nil || ok {
		t.Fatalf("no lease: %v %v", ok, err)
	}
}

// O ranks as the header counts (reviewer-codex): non-merge rows first, then
// merges; a project with only merges waiting is still opened.
func TestOthersKeyRanksAsTheHeader(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		reads map[string]otherRead
		want  string
	}{
		{map[string]otherRead{"agent-bus-monitor": {}, "trading": {counts: workCounts{you: 5, merge: 4}, at: now}, "busdev": {counts: workCounts{you: 3}, at: now}}, "busdev"},
		{map[string]otherRead{"trading": {counts: workCounts{you: 2, merge: 2}, at: now}}, "trading"},
		{map[string]otherRead{"trading": {counts: workCounts{you: 3, merge: 2}, at: now}, "busdev": {counts: workCounts{you: 1}, at: now}}, "trading"},
	} {
		src := adminFixture(shellNow)
		m := shellWith(t, 140, 40, shellConfig{admin: &src, projectHasKeys: func(context.Context, string) (bool, error) { return true, nil }}, false)
		m.others = c.reads
		m, cmd := pressCmd(t, m, 'O')
		m, quit := runQuits(t, m, cmd)
		if !quit || m.switchTo != c.want {
			t.Errorf("%v: switchTo %q (flash %q), want %q", c.reads, m.switchTo, m.ui.flash, c.want)
		}
	}
}
