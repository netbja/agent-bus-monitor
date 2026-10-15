package bus

import (
	"context"
	"testing"
)

var bound = AgentSnapshot{State: "idle", Message: "m", TS: 100, Pane: "w2:p1", PID: 42, PIDStart: 7, Terminal: "term_a", Session: "s1"}

func TestMergeKeepsVerifiedIdentity(t *testing.T) {
	// Codex's app-server env carries another session's pane: an unverified
	// status must not replace the identity a bind proved.
	got := mergeSnapshot(&bound, AgentSnapshot{State: "working", Message: "x", TS: 200, Pane: "wH:p1"}, false)
	if got.Pane != "w2:p1" || got.PID != 42 || got.PIDStart != 7 || got.Terminal != "term_a" {
		t.Fatalf("identity = %+v, want the bound one", got)
	}
	if got.State != "working" || got.Message != "x" || got.TS != 200 || got.Session != "s1" {
		t.Fatalf("state = %+v, want the new state and the kept session", got)
	}
}

func TestMergeVerifiedReplaces(t *testing.T) {
	got := mergeSnapshot(&bound, AgentSnapshot{State: "idle", TS: 200, Pane: "w3:p1", PID: 99, PIDStart: 1, Terminal: "term_b"}, false)
	if got.Pane != "w3:p1" || got.PID != 99 || got.PIDStart != 1 || got.Terminal != "term_b" {
		t.Fatalf("identity = %+v, want the newly verified one", got)
	}
}

func TestMergeUnverifiedWithoutPriorKeepsEnvPane(t *testing.T) {
	got := mergeSnapshot(&AgentSnapshot{State: "idle", TS: 1}, AgentSnapshot{State: "idle", TS: 2, Pane: "w1:p1"}, false)
	if got.Pane != "w1:p1" || got.PID != 0 {
		t.Fatalf("got %+v, want the env pane kept as before", got)
	}
}

func TestMergeOlderStatusDoesNotRegress(t *testing.T) {
	got := mergeSnapshot(&bound, AgentSnapshot{State: "working", Message: "old", TS: 50}, false)
	if got.State != "idle" || got.Message != "m" || got.TS != 100 {
		t.Fatalf("got %+v, want the newer state kept", got)
	}
}

func TestMergeBindKeepsState(t *testing.T) {
	old := AgentSnapshot{State: "working", Message: "busy", TS: 100, Session: "s1", Pane: "wH:p1"}
	got := mergeSnapshot(&old, AgentSnapshot{Pane: "w2:p1", PID: 42, PIDStart: 7, Terminal: "term_a"}, true)
	if got.State != "working" || got.Message != "busy" || got.TS != 100 || got.Session != "s1" {
		t.Fatalf("state = %+v, want it untouched by bind", got)
	}
	if got.Pane != "w2:p1" || got.PID != 42 || got.PIDStart != 7 || got.Terminal != "term_a" {
		t.Fatalf("identity = %+v, want the bound one", got)
	}
	if fresh := mergeSnapshot(nil, AgentSnapshot{Pane: "w2:p1", PID: 42, PIDStart: 7}, true); fresh.State != "" || fresh.PID != 42 {
		t.Fatalf("bind with no snapshot = %+v", fresh)
	}
}

func TestBindThenStatusRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.Bind(ctx, "codex", AgentIdent{Pane: "w2:p1", PID: 42, PIDStart: 7, Terminal: "term_a"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := b.Status(ctx, "codex", "idle", "ready", AgentIdent{Pane: "wH:p1"}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	m, err := b.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	s := m["codex"]
	if s.Pane != "w2:p1" || s.PID != 42 || s.PIDStart != 7 || s.State != "idle" || s.Message != "ready" {
		t.Fatalf("snapshot = %+v, want bound identity + new state", s)
	}
	if err := b.Bind(ctx, "Bad Name", AgentIdent{PID: 1}); err == nil {
		t.Fatal("Bind accepted an invalid agent name")
	}
	if err := b.Bind(ctx, "codex", AgentIdent{Pane: "w2:p1"}); err == nil {
		t.Fatal("Bind accepted an identity with no pid")
	}
}

func TestBindSurvivesConcurrentStatusRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() {
			_, err := b.Status(ctx, "codex", "working", "tick", AgentIdent{Pane: "wH:p1"})
			done <- err
		}()
	}
	if err := b.Bind(ctx, "codex", AgentIdent{Pane: "w2:p1", PID: 42, PIDStart: 7}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Status: %v", err)
		}
	}
	s := mustAgents(t, b)["codex"]
	if s.PID != 42 || s.Pane != "w2:p1" {
		t.Fatalf("snapshot = %+v, want the bind to survive the concurrent statuses", s)
	}
}

func mustAgents(t *testing.T, b *Bus) map[string]AgentSnapshot {
	t.Helper()
	m, err := b.Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	return m
}

func TestMergeOldVerifiedStatusDoesNotUndoBind(t *testing.T) {
	// A verified status appended at ts 150, delayed, lands after a bind made at
	// ts 300: the bind is newer and must stay.
	old := AgentSnapshot{State: "idle", TS: 100, Pane: "w2:p1", PID: 42, PIDStart: 7, IdentTS: 300}
	got := mergeSnapshot(&old, AgentSnapshot{State: "working", TS: 150, Pane: "w9:p9", PID: 11, PIDStart: 3}, false)
	if got.PID != 42 || got.Pane != "w2:p1" {
		t.Fatalf("identity = %+v, want the newer bind", got)
	}
	if got.State != "working" || got.TS != 150 {
		t.Fatalf("state = %+v, want the newer state", got)
	}
	newer := mergeSnapshot(&old, AgentSnapshot{State: "idle", TS: 400, Pane: "w9:p9", PID: 11, PIDStart: 3}, false)
	if newer.PID != 11 || newer.IdentTS != 400 {
		t.Fatalf("identity = %+v, want the newer verified status", newer)
	}
}

func TestDelayedVerifiedStatusAfterBindRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	before, err := b.ServerNowMS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(ctx, "codex", AgentIdent{Pane: "w2:p1", PID: 42, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	// The snapshot write of a status whose stream entry predates the bind.
	if err := b.putSnapshot(ctx, "codex", AgentSnapshot{State: "idle", TS: before - 1, Pane: "w9:p9", PID: 11, PIDStart: 3}, false); err != nil {
		t.Fatal(err)
	}
	if s := mustAgents(t, b)["codex"]; s.PID != 42 || s.Pane != "w2:p1" {
		t.Fatalf("snapshot = %+v, want the bind kept", s)
	}
}

func TestMergeKeepsClientAndProvider(t *testing.T) {
	bound := mergeSnapshot(nil, AgentSnapshot{Pane: "p", PID: 7, IdentTS: 10, Client: "codex", Provider: "openai"}, true)
	if bound.Client != "codex" || bound.Provider != "openai" {
		t.Fatalf("bind: %+v", bound)
	}
	st := mergeSnapshot(&bound, AgentSnapshot{State: "working", TS: 20}, false)
	if st.Client != "codex" || st.Provider != "openai" {
		t.Fatalf("a status without them erased them: %+v", st)
	}
}
