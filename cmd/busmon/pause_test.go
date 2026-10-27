package main

import (
	"context"
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
