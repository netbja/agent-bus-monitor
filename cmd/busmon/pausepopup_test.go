package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func pausedShared(now time.Time) *shared {
	st := demoShared(now)
	st.pause = &bus.PauseRecord{Until: now.Add(90 * time.Minute).UnixMilli(), Reason: "quota reset", By: "master", Notify: "master"}
	return st
}

func TestPollMarksAFailedPauseRead(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, pause: &bus.PauseRecord{Until: time.Now().Add(time.Hour).UnixMilli()}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if st.pauseFailed || st.pause == nil {
		t.Fatalf("a good read: failed %v pause %v", st.pauseFailed, st.pause)
	}
	f.pauseErr = errors.New("timeout")
	pollOnce(context.Background(), f, st, 2)
	if !st.pauseFailed || st.pause == nil {
		t.Fatalf("a failed read keeps the last value and marks it: failed %v pause %v", st.pauseFailed, st.pause)
	}
}
