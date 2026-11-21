package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
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
