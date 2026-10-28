package bus

import (
	"context"
	"errors"
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
		if err := b.ProveSession(ctx, "coder", c.pid, c.start, c.session, 100); err == nil {
			t.Errorf("%s: proved", c.name)
		}
		if got := snap(); got != before {
			t.Errorf("%s: snapshot changed\n got %+v\nwant %+v", c.name, got, before)
		}
	}

	if err := b.ProveSession(ctx, "coder", 42, 7, "after-clear-0002", 100); err != nil {
		t.Fatalf("ProveSession: %v", err)
	}
	want := before
	want.IdentSessionAt = 100
	want.IdentSession = "after-clear-0002"
	if got := snap(); got != want {
		t.Fatalf("after the proof\n got %+v\nwant %+v", got, want)
	}

	// No snapshot, nothing to prove against: no entry is created.
	if err := b.ProveSession(ctx, "ghost", 42, 7, "after-clear-0002", 100); err == nil {
		t.Fatal("proved a session for an agent with no snapshot")
	}
	if _, ok := mustAgents(t, b)["ghost"]; ok {
		t.Fatal("ProveSession created a snapshot")
	}
	if err := b.ProveSession(ctx, "Bad Name", 42, 7, "after-clear-0002", 100); err == nil {
		t.Fatal("accepted an invalid agent name")
	}
}

// Both orders end on the proved session: a status naming the old session
// before the proof is replaced by it, and one after it never reverts it.
func TestProveSessionAndAStatusInEitherOrderRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	self := AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7, Session: "before-clear-0001"}
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := b.Status(ctx, "coder", "idle", "status first", self); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := mustAgents(t, b)["coder"].IdentSession; got != "before-clear-0001" {
		t.Fatalf("a same-process status fills an empty session: %q", got)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "after-clear-0002", 100); err != nil {
		t.Fatalf("ProveSession: %v", err)
	}
	if got := mustAgents(t, b)["coder"].IdentSession; got != "after-clear-0002" {
		t.Fatalf("status then proof: %q", got)
	}
	if _, err := b.Status(ctx, "coder", "working", "status after", self); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := mustAgents(t, b)["coder"].IdentSession; got != "after-clear-0002" {
		t.Fatalf("proof then status: reverted to %q", got)
	}
}

// Two /clears in a row (reviewer-codex's review of lifecycle-1): their
// detached provers may land in either order, and the newer session wins,
// by the stamp each hook took in the foreground. A new process starts over.
func TestProveSessionTheNewerHookWinsInEitherOrderRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	ident := func() (string, int64) {
		s := mustAgents(t, b)["coder"]
		return s.IdentSession, s.IdentSessionAt
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "second-clear-0002", 200); err != nil {
		t.Fatalf("newer first: %v", err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "first-clear-00001", 100); !errors.Is(err, ErrStaleProof) {
		t.Fatalf("older after: %v", err)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "replayed-00000002", 200); !errors.Is(err, ErrStaleProof) {
		t.Fatalf("same stamp again: %v", err)
	}
	if s, at := ident(); s != "second-clear-0002" || at != 200 {
		t.Fatalf("after reverse order: %q at %d", s, at)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "third-clear-00003", 300); err != nil {
		t.Fatalf("newer: %v", err)
	}
	if s, _ := ident(); s != "third-clear-00003" {
		t.Fatalf("in order: %q", s)
	}
	if err := b.ProveSession(ctx, "coder", 42, 7, "no-stamp-00000004", 0); err == nil {
		t.Fatal("a proof without a stamp was accepted")
	}

	// A relaunched process proves from scratch: its first proof wins whatever
	// the old process's stamps were.
	if err := b.Bind(ctx, "coder", AgentIdent{Pane: "w1:p3", PID: 51, PIDStart: 9}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if s, at := ident(); s != "" || at != 0 {
		t.Fatalf("a new process kept %q at %d", s, at)
	}
	if err := b.ProveSession(ctx, "coder", 51, 9, "relaunched-000005", 50); err != nil {
		t.Fatalf("new process: %v", err)
	}
}
