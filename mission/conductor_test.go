package mission

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

// harness: a repository with change "chg" (t1 for kimi reviewed by codex, t2
// depending on t1), loaded straight into the store, and a conductor holding
// the lease.
type harness struct {
	t    *testing.T
	ctx  context.Context
	c    *Conductor
	b    *bus.Bus
	r    *redis.Client
	repo string
}

func missionsFile(t1Test string) string {
	return `{"change": "chg", "reviewer": "codex", "test": "true", "missions": [
 {"id": "t1", "title": "First", "agent": "kimi", "allowed_paths": ["src/t1/**"], "test": "` + t1Test + `", "brief": "plan.md#t1"},
 {"id": "t2", "title": "Second", "agent": "coder", "depends_on": ["t1"], "allowed_paths": ["src/t2/**"], "test": "true", "brief": "plan.md#t2"}]}`
}

func newHarness(t *testing.T, t1Test string) *harness {
	t.Helper()
	g, dir := newRepo(t)
	commitFile(t, dir, "docs/changes/chg/plan.md", "# plan\n", "plan")
	data := missionsFile(t1Test)
	commit := commitFile(t, dir, "docs/changes/chg/missions.json", data, "missions")
	b, r := dialBus(t)
	ctx := context.Background()
	if ok, _ := b.AcquireConductor(ctx, "tok", time.Minute); !ok {
		t.Fatal("lease")
	}
	s := Store{B: b, Token: "tok"}
	f, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureBranch(ctx, f.Branch, f.Base); err != nil {
		t.Fatal(err)
	}
	s.PutChange(ctx, ChangeRecord{Change: "chg", Path: "docs/changes/chg/missions.json", Commit: commit, FileHash: FileHash([]byte(data)),
		Base: f.Base, Branch: f.Branch, Rev: 1, State: "active"})
	for _, m := range f.Missions {
		s.PutMission(ctx, "chg", MissionRecord{ID: m.ID, Hash: m.Hash(), State: Pending, Reviewer: f.ReviewerOf(m)})
	}
	c := &Conductor{Bus: b, Store: s, Git: g, ProjectRoot: t.TempDir(), EscalateTo: "master", Self: "conductor", TestLimit: time.Minute}
	return &harness{t: t, ctx: ctx, c: c, b: b, r: r, repo: dir}
}

func (h *harness) pass() {
	h.t.Helper()
	if err := h.c.Pass(h.ctx); err != nil {
		h.t.Fatalf("Pass: %v", err)
	}
}

func (h *harness) mission(id string) MissionRecord {
	h.t.Helper()
	all, err := h.c.Store.Missions(h.ctx, "chg")
	if err != nil {
		h.t.Fatal(err)
	}
	return all[id]
}

// commitAs commits files on a fresh branch from base and returns the sha.
func (h *harness) commitAs(base, branch string, files map[string]string) string {
	h.t.Helper()
	wt := filepath.Join(h.t.TempDir(), "agent")
	run(h.t, h.repo, "git", "worktree", "add", "-q", "-b", branch, wt, base)
	var sha string
	for p, body := range files {
		sha = commitFile(h.t, wt, p, body, "work on "+p)
	}
	return sha
}

// deliver plays the agent: accept the current task, commit, done --commit.
func (h *harness) deliver(id string, files map[string]string) string {
	h.t.Helper()
	m := h.mission(id)
	if err := h.b.AcceptRequest(h.ctx, m.Current.Task, m.Current.Agent); err != nil {
		h.t.Fatalf("accept %s: %v", m.Current.Task, err)
	}
	sha := h.commitAs(m.Current.Base, "mission/"+m.Current.Task, files)
	if _, err := h.b.CompleteRequestAt(h.ctx, m.Current.Task, m.Current.Agent, "done", sha, "mission/"+m.Current.Task); err != nil {
		h.t.Fatal(err)
	}
	return sha
}

func (h *harness) cmds() []bus.Event {
	evs, _, err := h.b.Recent(h.ctx, []string{"cmd"}, 500)
	if err != nil {
		h.t.Fatal(err)
	}
	return evs
}

func countCmds(evs []bus.Event, keep func(bus.Event) bool) int {
	n := 0
	for _, e := range evs {
		if keep(e) {
			n++
		}
	}
	return n
}

func TestPassRequestsReadyMission(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	board, _ := h.b.Board(h.ctx)
	if e := board["chg-t1-1"]; e.Request == nil || e.Request.Target != "kimi" || e.Request.From != "conductor" {
		t.Fatalf("t1 request: %+v", e)
	}
	if h.mission("t1").State != Requested || h.mission("t2").State != Pending {
		t.Fatalf("states: t1 %s t2 %s", h.mission("t1").State, h.mission("t2").State)
	}
	body := board["chg-t1-1"]
	evs := h.cmds()
	if !strings.Contains(evs[len(evs)-1].Message, "base="+h.mission("t1").Current.Base) || !strings.Contains(evs[len(evs)-1].Message, "--commit") {
		t.Fatalf("request body: %q (%+v)", evs[len(evs)-1].Message, body)
	}
}

func TestPassChecksAndOpensReview(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	sha := h.deliver("t1", map[string]string{"src/t1/a.go": "package t1\n"})
	h.pass()
	m := h.mission("t1")
	if m.State != InReview || m.Subject != "mission:chg/t1@"+sha[:12] || m.ReviewRef != "chg-t1-1" {
		t.Fatalf("mission: %+v", m)
	}
	open, _ := h.b.OpenChallenges(h.ctx, "codex")
	if _, ok := open["chg-t1-1"]; !ok || !strings.Contains(open["chg-t1-1"], "--ref chg-t1-1") {
		t.Fatalf("gate: %v", open)
	}
}

func TestPassRejectsOutOfScope(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"README.md": "changed\n"})
	h.pass()
	m := h.mission("t1")
	if m.State != Requested || m.Attempt != 2 || m.Current.Task != "chg-t1-2" || len(m.History) != 1 || !strings.Contains(m.History[0].Reason, "README.md") {
		t.Fatalf("mission: %+v", m)
	}
	board, _ := h.b.Board(h.ctx)
	if board["chg-t1-1"].State != "superseded" {
		t.Fatalf("old task state %q", board["chg-t1-1"].State)
	}
	thread := board["chg-t1-1"].Request.Thread
	n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Ref == thread && e.Type == bus.CmdReply && e.From == "conductor" })
	if n != 1 {
		t.Fatalf("%d replies on the old thread", n)
	}
	vals := h.r.XRevRangeN(h.ctx, bus.StreamKey(h.b.Project(), "cmd"), "+", "-", 50).Val()
	for _, v := range vals {
		if v.Values["ref"] == thread && v.Values["from"] == "conductor" && v.Values["wake"] != "no" {
			t.Fatal("reply on the old thread would wake the agent")
		}
	}
}

func TestPassRejectsRedTests(t *testing.T) {
	h := newHarness(t, "echo boom; false")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h.pass()
	if m := h.mission("t1"); m.Attempt != 2 || !strings.Contains(m.History[0].Reason, "boom") {
		t.Fatalf("mission: %+v", m)
	}
}

func TestPassUnknownCommit(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	m := h.mission("t1")
	h.b.AcceptRequest(h.ctx, m.Current.Task, "kimi")
	h.b.CompleteRequestAt(h.ctx, m.Current.Task, "kimi", "done", "deadbeef", "")
	h.pass()
	if m := h.mission("t1"); m.Attempt != 2 || !strings.Contains(m.History[0].Reason, "not found") {
		t.Fatalf("mission: %+v", m)
	}
}

func TestPassAttemptsExhausted(t *testing.T) {
	h := newHarness(t, "false")
	h.pass()
	for i := 0; i < 3; i++ {
		h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
		h.pass()
	}
	m := h.mission("t1")
	if m.State != Blocked || m.Attempt != 3 {
		t.Fatalf("mission: %+v", m)
	}
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation && e.Target == "master" }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func (h *harness) verdict(subject, reviewer, decision string) {
	h.t.Helper()
	if _, err := h.b.AppendVerdict(h.ctx, bus.Verdict{Subject: subject, Author: "kimi", Reviewer: reviewer, Decision: decision}); err != nil {
		h.t.Fatal(err)
	}
}

func TestPassIgnoresWrongSubjectAndSelfApproval(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h.pass()
	m := h.mission("t1")
	h.verdict("mission:chg/t1@000000000000", "codex", "approve")
	h.verdict(m.Subject, "kimi", "approve")
	h.pass()
	if h.mission("t1").State != InReview {
		t.Fatal("moved on a wrong subject or a self-approval")
	}
	h.verdict(m.Subject, "codex", "approve")
	h.pass()
	if h.mission("t1").State != Approved {
		t.Fatalf("state %s", h.mission("t1").State)
	}
	if open, _ := h.b.OpenChallenges(h.ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate left open: %v", open)
	}
}

func TestPassIgnoresThirdPartyApprove(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h.pass()
	h.verdict(h.mission("t1").Subject, "grok", "approve")
	h.pass()
	if h.mission("t1").State != InReview {
		t.Fatal("a reviewer other than the assigned one approved")
	}
}

func TestPassReviewRejectedRetries(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h.pass()
	h.verdict(h.mission("t1").Subject, "codex", "reject")
	h.pass()
	m := h.mission("t1")
	if m.State != Requested || m.Attempt != 2 || m.History[0].Outcome != "rejected" {
		t.Fatalf("mission: %+v", m)
	}
	if open, _ := h.b.OpenChallenges(h.ctx, "codex"); len(open) != 0 {
		t.Fatalf("gate left open after a reject: %v", open)
	}
}

func TestPassPublishIdempotentAfterCrash(t *testing.T) {
	h := newHarness(t, "true")
	m := h.mission("t1")
	m.State, m.Attempt = Ready, 1
	m.Current = Attempt{N: 1, Task: "chg-t1-1", Agent: "kimi", Base: "x"}
	h.c.Store.PutMission(h.ctx, "chg", m)
	if _, err := h.b.FencedRequest(h.ctx, "tok", "chg-t1-1", "conductor", "kimi", "", "published before the crash"); err != nil {
		t.Fatal(err)
	}
	before := len(h.cmds())
	h.pass()
	if got := h.mission("t1"); got.State != Requested || got.Attempt != 1 {
		t.Fatalf("mission: %+v", got)
	}
	if after := len(h.cmds()); after != before {
		t.Fatalf("republished: %d → %d cmd entries", before, after)
	}

	// The challenge: the gate already holds the ref, so no second challenge.
	h2 := newHarness(t, "true")
	h2.pass()
	sha := h2.deliver("t1", map[string]string{"src/t1/a.go": "x"})
	h2.b.FencedChallenge(h2.ctx, "tok", "codex", "chg-t1-1", "conductor", "opened before the crash")
	h2.pass()
	if got := h2.mission("t1"); got.State != InReview || got.Subject != "mission:chg/t1@"+sha[:12] {
		t.Fatalf("mission: %+v", got)
	}
	if n := countCmds(h2.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdChallenge }); n != 1 {
		t.Fatalf("%d challenge cmds", n)
	}
}

func TestPassAgentBlocked(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	if err := h.b.BlockRequest(h.ctx, "chg-t1-1", "kimi", "need the API key"); err != nil {
		t.Fatal(err)
	}
	h.pass()
	if m := h.mission("t1"); m.State != Blocked || !strings.Contains(m.Reason, "need the API key") {
		t.Fatalf("mission: %+v", m)
	}
	if n := countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation }); n != 1 {
		t.Fatalf("%d escalations", n)
	}
}

func TestPassSupersededLateDone(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi")
	h.b.BlockRequest(h.ctx, "chg-t1-1", "kimi", "stuck")
	h.pass() // blocked
	if err := h.c.retry(h.ctx, "chg", "t1"); err != nil {
		t.Fatal(err)
	}
	h.pass()
	board, _ := h.b.Board(h.ctx)
	thread := board["chg-t1-1"].Request.Thread
	replies := func() int {
		return countCmds(h.cmds(), func(e bus.Event) bool { return e.Ref == thread && e.Type == bus.CmdReply && e.From == "conductor" })
	}
	if replies() != 1 {
		t.Fatalf("%d supersede replies", replies())
	}
	h.b.AcceptRequest(h.ctx, "chg-t1-1", "kimi") // the old agent resumes anyway
	h.b.CompleteRequestAt(h.ctx, "chg-t1-1", "kimi", "late", "abc1234", "")
	h.pass()
	h.pass()
	if m := h.mission("t1"); m.Current.Task != "chg-t1-2" || m.State != Requested {
		t.Fatalf("the late done moved the mission: %+v", m)
	}
	if replies() != 2 {
		t.Fatalf("%d replies, want exactly one more", replies())
	}
}
