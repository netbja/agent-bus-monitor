package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A10 P2: agents wait <agent> --since <ms> --timeout <d> returns 0 once the
// agent has declared a verified status (pid set) at or after since, else 1
// after the timeout; an older status (a relaunched role) does not count.
func TestAgentsWait(t *testing.T) {
	var snaps map[string]bus.AgentSnapshot
	calls := 0
	read := func(context.Context) (map[string]bus.AgentSnapshot, error) {
		calls++
		if calls == 3 {
			snaps = map[string]bus.AgentSnapshot{"reviewer-codex": {State: "idle", PID: 7, TS: 2000}}
		}
		return snaps, nil
	}
	snaps = map[string]bus.AgentSnapshot{"reviewer-codex": {State: "idle", PID: 5, TS: 500}} // an older run
	var out, errw bytes.Buffer
	sleep := func(time.Duration) {}
	if code := runAgentsWait(context.Background(), read, sleep, []string{"reviewer-codex", "--since", "1000", "--timeout", "10s"}, &out, &errw); code != 0 {
		t.Fatalf("code %d: %s", code, errw.String())
	}
	if calls != 3 {
		t.Fatalf("read %d times, want 3", calls)
	}
	calls = 0
	snaps = map[string]bus.AgentSnapshot{"reviewer-codex": {PID: 7, Pane: "w2:pE"}} // bound, never declared
	read2 := func(context.Context) (map[string]bus.AgentSnapshot, error) { calls++; return snaps, nil }
	out.Reset()
	errw.Reset()
	if code := runAgentsWait(context.Background(), read2, sleep, []string{"reviewer-codex", "--since", "1000", "--timeout", "3s"}, &out, &errw); code != 1 {
		t.Fatalf("no status: code %d", code)
	}
	if calls != 3 || !strings.Contains(errw.String(), "no status from reviewer-codex") {
		t.Fatalf("timeout: %d reads, %q", calls, errw.String())
	}
	if code := runAgentsWait(context.Background(), read2, sleep, []string{}, &out, &errw); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}
