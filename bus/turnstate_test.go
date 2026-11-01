package bus

import (
	"context"
	"errors"
	"testing"
)

func verifiedAgent(t *testing.T, b *Bus, state, msg string) AgentIdent {
	t.Helper()
	who := AgentIdent{Pane: "p1", PID: 4242, PIDStart: 99}
	if _, err := b.Status(context.Background(), "coder", state, msg, who); err != nil {
		t.Fatal(err)
	}
	return who
}

func snapOf(t *testing.T, b *Bus) AgentSnapshot {
	t.Helper()
	all, err := b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return all["coder"]
}

func statusCount(t *testing.T, b *Bus) int64 {
	t.Helper()
	n, err := b.r.XLen(context.Background(), StreamKey(b.project, "status")).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A turn flips idle and working in the snapshot only: the message kept, auto
// set, no status entry; a declared waiting, blocked or done is never touched.
func TestTurnStateFlipsTheSnapshotOnly(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	who := verifiedAgent(t, b, "idle", "reviews pending")
	entries := statusCount(t, b)
	if err := b.TurnState(ctx, "coder", "working", who); err != nil {
		t.Fatal(err)
	}
	s := snapOf(t, b)
	if s.State != "working" || s.Message != "reviews pending" || s.Auto != AutoTurn {
		t.Fatalf("flip: %+v", s)
	}
	if statusCount(t, b) != entries {
		t.Fatal("a flip wrote a status entry")
	}
	if err := b.TurnState(ctx, "coder", "working", who); !errors.Is(err, ErrNoFlip) {
		t.Fatalf("an equal state: %v", err)
	}
	if err := b.TurnState(ctx, "coder", "idle", who); err != nil || snapOf(t, b).State != "idle" {
		t.Fatalf("back to idle: %v %+v", err, snapOf(t, b))
	}
	for _, declared := range []string{"waiting", "blocked", "done"} {
		verifiedAgent(t, b, declared, "mine")
		if err := b.TurnState(ctx, "coder", "working", who); !errors.Is(err, ErrNoFlip) || snapOf(t, b).State != declared {
			t.Fatalf("%s overwritten: %v %+v", declared, err, snapOf(t, b))
		}
	}
}

// Only the agent's verified process flips it.
func TestTurnStateNeedsTheVerifiedProcess(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	who := verifiedAgent(t, b, "idle", "x")
	for _, other := range []AgentIdent{{}, {PID: 4242, PIDStart: 1}, {PID: 7, PIDStart: 99}} {
		if err := b.TurnState(ctx, "coder", "working", other); !errors.Is(err, ErrNotProved) {
			t.Fatalf("%+v: %v", other, err)
		}
	}
	if snapOf(t, b).State != "idle" {
		t.Fatal("an unproved caller flipped")
	}
	if err := b.TurnState(ctx, "nobody", "working", who); !errors.Is(err, ErrNotProved) {
		t.Fatalf("no snapshot: %v", err)
	}
}

// A declaration clears auto and wins over a flip whatever their ts (foureyes:
// Status XADDs, then writes the snapshot with the entry's time).
func TestADeclarationBeatsAFlip(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	who := verifiedAgent(t, b, "idle", "x")
	if err := b.TurnState(ctx, "coder", "working", who); err != nil {
		t.Fatal(err)
	}
	flipped := snapOf(t, b)
	// A declaration whose entry is older than the flip's TIME.
	next := AgentSnapshot{State: "waiting", Message: "for the person", TS: flipped.TS - 5, PID: who.PID, PIDStart: who.PIDStart, Pane: who.Pane}
	if err := b.putSnapshot(ctx, "coder", next, false); err != nil {
		t.Fatal(err)
	}
	if s := snapOf(t, b); s.State != "waiting" || s.Auto != "" || s.Message != "for the person" {
		t.Fatalf("the declaration lost: %+v", s)
	}
}
