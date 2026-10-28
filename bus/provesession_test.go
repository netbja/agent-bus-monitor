package bus

import (
	"context"
	"testing"
)

// After /clear the process stays and the session changes (lifecycle spec,
// part D): ProveSession moves IdentSession for the verified process only.
func TestProveSessionMovesIdentSessionForTheVerifiedProcessOnlyRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7, Terminal: "term_a"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := b.Status(ctx, "coder", "idle", "handed off", AgentIdent{Pane: "w1:p2", Session: "before-clear-0001"}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	snap := func() AgentSnapshot {
		m, err := b.Agents(ctx)
		if err != nil {
			t.Fatalf("Agents: %v", err)
		}
		return m["coder"]
	}
	before := snap()

	for _, c := range []struct {
		name    string
		pid     int
		start   uint64
		session string
	}{
		{"another pid", 43, 7, "after-clear-0002"},
		{"a recycled pid", 42, 8, "after-clear-0002"},
		{"no pid", 0, 0, "after-clear-0002"},
		{"a session id that is no id", 42, 7, "../x"},
		{"an empty session", 42, 7, ""},
	} {
		if err := b.ProveSession(ctx, "coder", c.pid, c.start, c.session); err == nil {
			t.Errorf("%s: proved", c.name)
		}
		if got := snap(); got != before {
			t.Errorf("%s: snapshot changed\n got %+v\nwant %+v", c.name, got, before)
		}
	}

	if err := b.ProveSession(ctx, "coder", 42, 7, "after-clear-0002"); err != nil {
		t.Fatalf("ProveSession: %v", err)
	}
	want := before
	want.IdentSession = "after-clear-0002"
	if got := snap(); got != want {
		t.Fatalf("after the proof\n got %+v\nwant %+v", got, want)
	}

	// No snapshot, nothing to prove against: no entry is created.
	if err := b.ProveSession(ctx, "ghost", 42, 7, "after-clear-0002"); err == nil {
		t.Fatal("proved a session for an agent with no snapshot")
	}
	if _, ok := mustAgents(t, b)["ghost"]; ok {
		t.Fatal("ProveSession created a snapshot")
	}
	if err := b.ProveSession(ctx, "Bad Name", 42, 7, "after-clear-0002"); err == nil {
		t.Fatal("accepted an invalid agent name")
	}
}
