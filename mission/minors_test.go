package mission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// §10: abort transitions the missions first and writes the change last, so a
// failure part-way leaves the change active (and `mission abort` can be rerun).
func TestAbortCrashLeavesChangeActive(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	if _, err := h.b.FencedRequest(h.ctx, "tok", "chg-t2-1", "conductor", "coder", "", "published"); err != nil {
		t.Fatal(err)
	}
	h.setState("t2", Requested, func(m *MissionRecord) {
		m.Current = Attempt{N: 1, Task: "chg-t2-1", Agent: "Bad Name"} // its close reply cannot be sent
	})
	if ok, _ := h.op("abort", "chg", "stop"); ok {
		t.Fatal("abort reported success despite a failed mission")
	}
	if ch, _, _ := h.c.Store.Change(h.ctx, "chg"); ch.State != "active" {
		t.Fatalf("change is %q after a partial abort", ch.State)
	}
}

// §10: an unknown commit is the delivery's fault; a broken repository is ours.
func TestResolveCommitInfraError(t *testing.T) {
	g, _ := newRepo(t)
	if _, err := g.ResolveCommit(context.Background(), "deadbeef"); !errors.Is(err, ErrUnknownCommit) {
		t.Fatalf("unknown commit: %v", err)
	}
	gone := Git{Repo: filepath.Join(t.TempDir(), "no-such-repo")}
	if _, err := gone.ResolveCommit(context.Background(), "HEAD"); err == nil || errors.Is(err, ErrUnknownCommit) {
		t.Fatalf("broken repo reported as an unknown commit: %v", err)
	}
}

// §10: the verdict subject carries the full sha.
func TestSubjectUsesFullSha(t *testing.T) {
	h := newHarness(t, "true")
	sha := h.toReview(map[string]string{"src/t1/a.go": "x"})
	if s := h.mission("t1").Subject; s != "mission:chg/t1@"+sha {
		t.Fatalf("subject %q", s)
	}
}

// §10: a late accept on an attempt of an aborted change still gets its reply.
func TestAbortedChangeAnswersLateDone(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	if ok, msg := h.op("abort", "chg", "stop"); !ok {
		t.Fatalf("abort: %q", msg)
	}
	board, _ := h.b.Board(h.ctx)
	thread := board["chg-t1-1"].Request.Thread
	replies := func() int {
		return countCmds(h.cmds(), func(e bus.Event) bool { return e.Ref == thread && e.From == "conductor" && e.Type == bus.CmdReply })
	}
	n := replies()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.pass()
	h.pass()
	if replies() != n+1 {
		t.Fatalf("late accept on an aborted change: %d → %d replies", n, replies())
	}
}

// §10: effects that write to a stream are at least once — a crash between
// the XADD and the flag repeats the effect once, and only once.
func TestEffectsAtLeastOnce(t *testing.T) {
	h := newHarness(t, "true")
	h.setState("t1", Blocked, func(m *MissionRecord) { m.Escalated, m.Reason = false, "stuck" })
	// the escalation of a conductor that crashed before recording it
	h.b.FencedCmdRef(h.ctx, "tok", "conductor", "master", bus.CmdEscalation, "", "chg/t1 blocked: stuck", false)
	h.pass()
	h.pass()
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 2 {
		t.Fatalf("%d escalations, want the crashed one plus exactly one replay", n)
	}
	if m := h.mission("t1"); m.State != Blocked || !m.Escalated {
		t.Fatalf("t1: %+v", m)
	}
}

// §10: an integration never runs in a dirty worktree.
func TestIntegrateRefusesDirtyWorktree(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.ensureIntegrationWorktree()
	stray := filepath.Join(h.integrationDir(), "notes.txt")
	os.WriteFile(stray, []byte("mine"), 0o644)
	pre := h.branchHead()
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	if m := h.mission("t1"); m.State != Blocked || !strings.Contains(m.Reason, "unrecorded changes") {
		t.Fatalf("t1: %+v", m)
	}
	if h.branchHead() != pre {
		t.Fatal("merged into a dirty worktree")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("the stray file was deleted")
	}
}

// §10: a dirty integration worktree with nothing integrating is escalated once
// at startup; cleaning it clears the flag.
func TestStartupDirtyWithoutIntegrationEscalatesOnce(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.ensureIntegrationWorktree()
	stray := filepath.Join(h.integrationDir(), "notes.txt")
	os.WriteFile(stray, []byte("mine"), 0o644)
	h.c.Startup(h.ctx)
	h.c.Startup(h.ctx)
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
	if ch, _, _ := h.c.Store.Change(h.ctx, "chg"); !ch.DirtyEscalated {
		t.Fatal("flag not set")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("the stray file was deleted")
	}
	os.Remove(stray)
	h.c.Startup(h.ctx)
	if ch, _, _ := h.c.Store.Change(h.ctx, "chg"); ch.DirtyEscalated {
		t.Fatal("flag not cleared once clean")
	}
}
