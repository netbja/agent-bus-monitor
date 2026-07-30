package bus

import (
	"context"
	"strings"
	"testing"
)

func TestBoardKeyNaming(t *testing.T) {
	if got := BoardKey("busmon"); got != "busmon:board" {
		t.Fatalf("BoardKey = %q, want busmon:board", got)
	}
}

func TestBoardClaimAndRead(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.BoardClaim(ctx, "task-20", "coder", "coder/task-20"); err != nil {
		t.Fatalf("BoardClaim: %v", err)
	}
	m, err := b.Board(ctx)
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	e, ok := m["task-20"]
	if !ok {
		t.Fatalf("task-20 missing from board: %+v", m)
	}
	if e.Owner != "coder" || e.State != "working" || e.Branch != "coder/task-20" {
		t.Errorf("entry = %+v, want coder/working/coder/task-20", e)
	}
	if e.Updated == 0 {
		t.Error("Updated not stamped")
	}
}

func TestBoardClaimConflict(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.BoardClaim(ctx, "task-20", "foureyes", ""); err != nil {
		t.Fatalf("BoardClaim: %v", err)
	}
	// A different owner must be refused — this refusal is the board's whole job.
	err := b.BoardClaim(ctx, "task-20", "coder", "")
	if err == nil || !strings.Contains(err.Error(), "owned by foureyes") {
		t.Fatalf("conflicting claim = %v, want 'owned by foureyes' error", err)
	}
	// Re-claim by the same owner refreshes, it does not conflict.
	if err := b.BoardClaim(ctx, "task-20", "foureyes", "foureyes/task-20"); err != nil {
		t.Fatalf("re-claim by owner: %v", err)
	}
	// Once done, anyone can pick the task up again.
	if err := b.BoardState(ctx, "task-20", "done"); err != nil {
		t.Fatalf("BoardState done: %v", err)
	}
	if err := b.BoardClaim(ctx, "task-20", "coder", ""); err != nil {
		t.Fatalf("claim after done: %v", err)
	}
	m, _ := b.Board(ctx)
	if got := m["task-20"].Owner; got != "coder" {
		t.Errorf("owner after re-claim = %q, want coder", got)
	}
}

func TestBoardStateKeepsOwnerAndBranch(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.BoardClaim(ctx, "task-7", "coder", "coder/task-7"); err != nil {
		t.Fatalf("BoardClaim: %v", err)
	}
	if err := b.BoardState(ctx, "task-7", "review"); err != nil {
		t.Fatalf("BoardState: %v", err)
	}
	m, _ := b.Board(ctx)
	e := m["task-7"]
	if e.State != "review" || e.Owner != "coder" || e.Branch != "coder/task-7" {
		t.Errorf("entry after state = %+v, want review keeping coder/coder/task-7", e)
	}
}

func TestBoardStateUnknownTaskFails(t *testing.T) {
	b := dialTest(t)
	if err := b.BoardState(context.Background(), "ghost-task", "review"); err == nil {
		t.Fatal("BoardState on an unclaimed task should fail loud, not invent an entry")
	}
}

func TestBoardDrop(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.BoardClaim(ctx, "task-3", "coder", ""); err != nil {
		t.Fatalf("BoardClaim: %v", err)
	}
	if err := b.BoardDrop(ctx, "task-3"); err != nil {
		t.Fatalf("BoardDrop: %v", err)
	}
	m, _ := b.Board(ctx)
	if _, ok := m["task-3"]; ok {
		t.Errorf("task-3 still on the board after drop: %+v", m)
	}
	// Dropping an unknown task is a no-op success.
	if err := b.BoardDrop(ctx, "task-3"); err != nil {
		t.Errorf("drop of unknown task: %v", err)
	}
}

func TestBoardValidation(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.BoardClaim(ctx, "Bad Task", "coder", ""); err == nil {
		t.Error("claim accepted an invalid task name")
	}
	if err := b.BoardClaim(ctx, "task-1", "Bad Agent", ""); err == nil {
		t.Error("claim accepted an invalid owner name")
	}
	if err := b.BoardState(ctx, "task-1", "in review"); err == nil {
		t.Error("state accepted a multi-word state")
	}
}
