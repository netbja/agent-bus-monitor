package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestHeaderShowsThePauseAndWhenItEnds(t *testing.T) {
	now := time.Now()
	p := &bus.PauseRecord{Until: now.Add(112*time.Minute + 20*time.Second).UnixMilli(), Reason: "quota", Notify: "master"}
	top := headerLines(headerData{project: "p", health: health{ok: true}, pause: p, now: now}, 160, TierWide)[0].text()
	if !strings.Contains(top, "paused · resumes in 1h52 (quota)") {
		t.Fatalf("header %q", top)
	}
	p.Until = now.Add(-time.Minute).UnixMilli()
	if top = headerLines(headerData{project: "p", health: health{ok: true}, pause: p, now: now}, 160, TierWide)[0].text(); !strings.Contains(top, "paused · resume due") {
		t.Fatalf("overdue header %q", top)
	}
}

func TestPollReadsThePause(t *testing.T) {
	p := &bus.PauseRecord{Until: time.Now().Add(time.Hour).UnixMilli(), Notify: "master"}
	f := &fakeReader{calls: map[string]int{}, pause: p}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if st.pause != p {
		t.Fatalf("pause = %+v", st.pause)
	}
}

func TestAllSkipsWhatOnlyReports(t *testing.T) {
	st := demoShared(time.Now())
	st.agents["conductor"] = &agentState{lastSeen: time.Now()} // reports, not in {p}:agents
	h := newHarness(t, st)
	if names := strings.Join(h.u.agentNames(), ","); strings.Contains(names, "conductor") || !strings.Contains(names, "coder") {
		t.Fatalf("@all targets %q", names)
	}
}

func TestAllFanOutReadsAsOneActivityLine(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	base := time.Now().UnixMilli()
	for i, a := range []string{"coder", "foureyes", "master"} {
		h.u.appendEvent(bus.Event{ID: strconv.FormatInt(base+int64(i), 10) + "-0", Kind: "cmd", Type: bus.CmdDirective,
			From: "bernard", Target: a, Message: "end of session", Human: true})
	}
	h.u.appendEvent(bus.Event{ID: strconv.FormatInt(base+5, 10) + "-0", Kind: "cmd", Type: bus.CmdDirective,
		From: "bernard", Target: "coder", Message: "another thing", Human: true})
	var events []feedLine
	for _, fl := range h.u.feed {
		if !fl.sep {
			events = append(events, fl)
		}
	}
	if len(events) != 2 || len(events[0].to) != 3 {
		t.Fatalf("feed %d lines, first to %v", len(events), events[0].to)
	}
	if row := activityRow(events[0], TierWide, 140).text(); !strings.Contains(row, "@all (3)") || strings.Count(row, "end of session") != 1 {
		t.Fatalf("row %q", row)
	}
	if !parseFilter("@master").match(events[0]) {
		t.Fatal("@master filter misses the fan-out line")
	}
}
