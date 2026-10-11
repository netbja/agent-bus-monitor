package mission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// toReview drives t1 to in_review with one allowed file.
func (h *harness) toReview(files map[string]string) string {
	h.t.Helper()
	h.pass()
	sha := h.deliver("t1", files)
	h.pass()
	if m := h.mission("t1"); m.State != InReview {
		h.t.Fatalf("t1 not in review: %+v", m)
	}
	return sha
}

func (h *harness) integrationDir() string {
	return filepath.Join(h.c.ProjectRoot, "chg", "integration")
}

func (h *harness) branchHead() string {
	h.t.Helper()
	sha, err := h.c.Git.ResolveCommit(h.ctx, "change/chg")
	if err != nil {
		h.t.Fatal(err)
	}
	return sha
}

func TestIntegrateUnlocksDependent(t *testing.T) {
	h := newHarness(t, "true")
	sha := h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	m := h.mission("t1")
	if m.State != Integrated || !m.TestedOK || m.TestedHead != h.branchHead() {
		t.Fatalf("t1: %+v", m)
	}
	if ok, _ := h.c.Git.BranchHasTrailer(h.ctx, "change/chg", "Mission-Commit", sha); !ok {
		t.Fatal("no Mission-Commit trailer on the change branch")
	}
	if ok, _ := h.c.Git.BranchHasTrailer(h.ctx, "change/chg", "Mission-Hash", m.Hash); !ok {
		t.Fatal("no Mission-Hash trailer")
	}
	h.pass()
	if t2 := h.mission("t2"); t2.State != Requested || t2.Current.Base != h.branchHead() {
		t.Fatalf("t2: %+v (head %s)", t2, h.branchHead())
	}
	if main, _ := h.c.Git.ResolveCommit(h.ctx, "main"); main == h.branchHead() {
		t.Fatal("main moved")
	}
}

// moveBranch commits on change/chg through a temporary worktree.
func (h *harness) moveBranch(rel, body string) {
	h.t.Helper()
	wt := filepath.Join(h.t.TempDir(), "mover")
	run(h.t, h.repo, "git", "worktree", "add", "-q", wt, "change/chg")
	commitFile(h.t, wt, rel, body, "concurrent change")
	run(h.t, h.repo, "git", "worktree", "remove", "--force", wt)
}

func TestIntegrateConflictBlocks(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "mission side\n"})
	h.moveBranch("src/t1/a.go", "branch side\n")
	pre := h.branchHead()
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	m := h.mission("t1")
	if m.State != Blocked || !strings.Contains(m.Reason, "conflict") || m.PreHead != pre {
		t.Fatalf("t1: %+v", m)
	}
	if h.branchHead() != pre {
		t.Fatal("branch head moved after a conflict")
	}
	if dirty, _ := h.c.Git.Dirty(h.ctx, h.integrationDir()); dirty {
		t.Fatal("integration worktree left dirty")
	}
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func TestIntegrateRedSuiteResets(t *testing.T) {
	h := newHarnessWith(t, "true", "false")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	pre := h.branchHead()
	h.verdict(h.mission("t1").Subject, "codex", "approve")
	h.pass()
	if m := h.mission("t1"); m.State != Blocked || m.TestedOK {
		t.Fatalf("t1: %+v", m)
	}
	if h.branchHead() != pre {
		t.Fatal("a red change suite left the merge in the branch")
	}
}

// approvedByHand sets t1 to state s without integrating it.
func (h *harness) setState(id string, s State, edit func(*MissionRecord)) {
	h.t.Helper()
	m := h.mission(id)
	m.State = s
	if edit != nil {
		edit(&m)
	}
	if err := h.c.Store.PutMission(h.ctx, "chg", m); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) ensureIntegrationWorktree() {
	h.t.Helper()
	if _, err := os.Stat(h.integrationDir()); err == nil {
		return
	}
	if err := h.c.Git.AddWorktree(h.ctx, h.integrationDir(), "change/chg", false); err != nil {
		h.t.Fatal(err)
	}
}

func TestStartupMergeInProgress(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.setState("t1", Approved, nil)
	// leave a conflicting merge in progress in the integration worktree
	h.ensureIntegrationWorktree()
	run(t, h.repo, "git", "checkout", "-q", "-b", "other", "main")
	other := commitFile(t, h.repo, "README.md", "other side\n", "other")
	run(t, h.repo, "git", "checkout", "-q", "main")
	commitFile(t, h.integrationDir(), "README.md", "branch side\n", "branch")
	if err := h.c.Git.MergeNoFF(h.ctx, h.integrationDir(), other, "m"); err != ErrConflict {
		t.Fatalf("setup merge: %v", err)
	}
	if err := h.c.Startup(h.ctx); err != nil {
		t.Fatal(err)
	}
	if in, _ := h.c.Git.MergeInProgress(h.integrationDir()); in {
		t.Fatal("merge still in progress after Startup")
	}
	h.pass()
	if m := h.mission("t1"); m.State != Integrated {
		t.Fatalf("t1 after startup: %+v", m)
	}
}

func TestStartupDirtyBlocks(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.ensureIntegrationWorktree()
	h.setState("t1", Integrating, func(m *MissionRecord) { m.PreHead = h.branchHead() })
	stray := filepath.Join(h.integrationDir(), "stray.txt")
	os.WriteFile(stray, []byte("mine"), 0o644)
	if err := h.c.Startup(h.ctx); err != nil {
		t.Fatal(err)
	}
	if m := h.mission("t1"); m.State != Blocked || !strings.Contains(m.Reason, "unrecorded changes") {
		t.Fatalf("t1: %+v", m)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("the stray file was deleted")
	}
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func TestStartupResumeAfterMergeBeforeSuite(t *testing.T) {
	h := newHarness(t, "true")
	sha := h.toReview(map[string]string{"src/t1/a.go": "x"})
	h.ensureIntegrationWorktree()
	pre := h.branchHead()
	m := h.mission("t1")
	if err := h.c.Git.MergeNoFF(h.ctx, h.integrationDir(), sha, MergeMessage("chg", "t1", 1, "First", sha, m.Hash)); err != nil {
		t.Fatal(err)
	}
	h.setState("t1", Integrating, func(r *MissionRecord) { r.PreHead, r.TestedOK, r.TestedHead = pre, false, "" })
	if err := h.c.Startup(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.mission("t1"); got.State != Integrated || !got.TestedOK || got.TestedHead != h.branchHead() {
		t.Fatalf("t1: %+v", got)
	}
}

func TestStartupCheckingGoesBackToDelivered(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h.pass() // in review now; rewind to a crashed check
	h.setState("t1", Checking, nil)
	if err := h.c.Startup(h.ctx); err != nil {
		t.Fatal(err)
	}
	if m := h.mission("t1"); m.State != Delivered {
		t.Fatalf("t1: %s", m.State)
	}
	h.pass()
	if m := h.mission("t1"); m.State != InReview {
		t.Fatalf("t1 after re-check: %s", m.State)
	}
}

// loadHarness: a repository with missions.json committed but nothing loaded.
func loadHarness(t *testing.T, data string) (*Conductor, string, context.Context) {
	t.Helper()
	g, dir := newRepo(t)
	commitFile(t, dir, "docs/changes/chg/plan.md", "# plan\n", "plan")
	commit := commitFile(t, dir, "docs/changes/chg/missions.json", data, "missions")
	b, _ := dialBus(t)
	ctx := context.Background()
	b.AcquireConductor(ctx, "tok", time.Minute)
	c := &Conductor{Bus: b, Store: Store{B: b, Token: "tok"}, Git: g, ProjectRoot: t.TempDir(), EscalateTo: "master", Self: "conductor", TestLimit: time.Minute}
	return c, commit, ctx
}

func TestOpLoadValidates(t *testing.T) {
	bad := strings.Replace(missionsFile("true"), `"agent": "coder"`, `"agent": "kimi", "reviewer": "kimi"`, 1)
	c, commit, ctx := loadHarness(t, bad)
	ok, msg := c.HandleOp(ctx, bus.MissionOp{Op: "load", Args: []string{"docs/changes/chg/missions.json@" + commit}})
	if ok || !strings.Contains(msg, "reviewer") {
		t.Fatalf("invalid file: %v %q", ok, msg)
	}
	if names, _ := c.Store.Changes(ctx); len(names) != 0 {
		t.Fatalf("records written for an invalid file: %v", names)
	}

	c, commit, ctx = loadHarness(t, missionsFile("true"))
	load := bus.MissionOp{Op: "load", Args: []string{"docs/changes/chg/missions.json@" + commit}}
	if ok, msg := c.HandleOp(ctx, load); !ok || !strings.Contains(msg, "loaded chg rev 1: 2 missions") {
		t.Fatalf("load: %v %q", ok, msg)
	}
	ms, _ := c.Store.Missions(ctx, "chg")
	if len(ms) != 2 || ms["t1"].State != Pending || ms["t2"].Reviewer != "codex" {
		t.Fatalf("records: %+v", ms)
	}
	if _, err := c.Git.ResolveCommit(ctx, "change/chg"); err != nil {
		t.Fatal("change branch not created")
	}
	if ok, msg := c.HandleOp(ctx, load); !ok || !strings.Contains(msg, "already loaded rev 1") {
		t.Fatalf("second load: %v %q", ok, msg)
	}
	other := commitFile(t, c.Git.Repo, "docs/changes/chg/missions.json", strings.Replace(missionsFile("true"), "First", "Changed", 1), "edit")
	if ok, msg := c.HandleOp(ctx, bus.MissionOp{Op: "load", Args: []string{"docs/changes/chg/missions.json@" + other}}); ok || !strings.Contains(msg, "slice 1") {
		t.Fatalf("reload: %v %q", ok, msg)
	}
	if ok, _ := c.HandleOp(ctx, bus.MissionOp{Op: "load", Args: []string{"no-commit-here"}}); ok {
		t.Fatal("malformed load accepted")
	}
}

func TestOpRetryOnlyBlocked(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	if ok, msg := h.c.HandleOp(h.ctx, bus.MissionOp{Op: "retry", Args: []string{"chg/t1"}}); ok || !strings.Contains(msg, "not blocked") {
		t.Fatalf("retry in review: %v %q", ok, msg)
	}
	h.setState("t1", Blocked, nil)
	if ok, msg := h.c.HandleOp(h.ctx, bus.MissionOp{Op: "retry", Args: []string{"chg/t1"}}); !ok {
		t.Fatalf("retry blocked: %q", msg)
	}
	if m := h.mission("t1"); m.State != Requested || m.Attempt != 2 {
		t.Fatalf("t1: %+v", m)
	}
}

func TestOpAbort(t *testing.T) {
	h := newHarness(t, "true")
	h.toReview(map[string]string{"src/t1/a.go": "x"})
	if ok, msg := h.c.HandleOp(h.ctx, bus.MissionOp{Op: "abort", Args: []string{"chg", "wrong plan"}}); !ok {
		t.Fatalf("abort: %q", msg)
	}
	if open, _ := h.b.OpenChallenges(h.ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate left open: %v", open)
	}
	if m := h.mission("t1"); m.State != Aborted {
		t.Fatalf("t1: %s", m.State)
	}
	if m := h.mission("t2"); m.State != Aborted {
		t.Fatalf("t2: %s", m.State)
	}
	board, _ := h.b.Board(h.ctx)
	if board["chg-t1-1"].State != "superseded" {
		t.Fatalf("attempt not superseded: %q", board["chg-t1-1"].State)
	}
	if ch, _, _ := h.c.Store.Change(h.ctx, "chg"); ch.State != "aborted" {
		t.Fatalf("change state %q", ch.State)
	}
	before := len(h.cmds())
	h.pass()
	if len(h.cmds()) != before {
		t.Fatal("Pass acted on an aborted change")
	}
	if ok, _ := h.c.HandleOp(h.ctx, bus.MissionOp{Op: "bogus"}); ok {
		t.Fatal("unknown op accepted")
	}
}
