package mission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const e2eMissions = `{"change": "chg", "reviewer": "codex", "test": "true", "missions": [
 {"id": "a", "title": "A", "agent": "kimi", "allowed_paths": ["src/a/**"], "test": "true", "brief": "plan.md#a"},
 {"id": "b", "title": "B", "agent": "coder", "allowed_paths": ["src/b/**"], "test": "BTEST", "brief": "plan.md#b"},
 {"id": "c", "title": "C", "agent": "kimi", "depends_on": ["a", "b"], "allowed_paths": ["src/c/**"], "test": "true", "brief": "plan.md#c"}]}`

// team plays every agent and the reviewer: it accepts each request addressed
// by the conductor, commits from the given base, delivers, and approves every
// review it is asked for. Git or bus hiccups are retried on the next round.
type team struct {
	t     *testing.T
	b     *bus.Bus
	repo  string
	mu    sync.Mutex
	done  map[string]bool // tasks delivered
	voted map[string]bool // subjects approved
}

func (tm *team) round(ctx context.Context) {
	board, err := tm.b.Board(ctx)
	if err != nil {
		return
	}
	ms, err := Store{B: tm.b}.Missions(ctx, "chg")
	if err != nil {
		return
	}
	for _, m := range ms {
		task := m.Current.Task
		e, ok := board[task]
		if !ok || tm.done[task] || (e.State != "requested" && e.State != "accepted") {
			continue
		}
		if e.State == "requested" && tm.b.AcceptRequest(ctx, task, m.Current.Agent) != nil {
			continue
		}
		wt := filepath.Join(tm.t.TempDir(), task)
		if _, err := gitRun(tm.repo, "worktree", "add", "-q", "-b", "mission/"+task, wt, m.Current.Base); err != nil {
			continue
		}
		p := filepath.Join(wt, "src", m.ID, "work.go")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("package "+m.ID+"\n"), 0o644)
		gitRun(wt, "add", ".")
		gitRun(wt, "commit", "-q", "-m", "mission "+task)
		sha, err := gitRun(wt, "rev-parse", "HEAD")
		if err != nil {
			continue
		}
		if _, err := tm.b.CompleteRequestAt(ctx, task, m.Current.Agent, "done", sha, "mission/"+task); err == nil {
			tm.done[task] = true
		}
	}
	for _, m := range ms {
		if m.State != InReview || m.Subject == "" || tm.voted[m.Subject] {
			continue
		}
		if _, err := tm.b.AppendVerdict(ctx, bus.Verdict{Subject: m.Subject, Author: m.Current.Agent, Reviewer: m.Reviewer, Decision: "approve", Ref: m.ReviewRef}); err == nil {
			tm.voted[m.Subject] = true
		}
	}
}

func (tm *team) play(ctx context.Context) {
	for ctx.Err() == nil {
		tm.round(ctx)
		time.Sleep(50 * time.Millisecond)
	}
}

func gitRun(dir string, args ...string) (string, error) {
	out, err := Git{Repo: dir}.run(context.Background(), dir, args...)
	return strings.TrimSpace(out), err
}

func e2eSetup(t *testing.T, bTest string) (*bus.Bus, RunConfig, string, *team) {
	t.Helper()
	b, _ := dialBus(t)
	_, repo := newRepo(t)
	commitFile(t, repo, "docs/changes/chg/plan.md", "# plan\n", "plan")
	commit := commitFile(t, repo, "docs/changes/chg/missions.json", strings.Replace(e2eMissions, "BTEST", bTest, 1), "missions")
	return b, runConfig(t, repo), commit, &team{t: t, b: b, repo: repo, done: map[string]bool{}, voted: map[string]bool{}}
}

func allIntegrated(b *bus.Bus) bool {
	ms, _ := Store{B: b}.Missions(context.Background(), "chg")
	if len(ms) != 3 {
		return false
	}
	for _, m := range ms {
		if m.State != Integrated {
			return false
		}
	}
	return true
}

func TestEndToEndThreeMissions(t *testing.T) {
	b, cfg, commit, tm := e2eSetup(t, "true")
	mainBefore, _ := Git{Repo: cfg.Repo}.ResolveCommit(context.Background(), "main")
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(t, ctx, b, cfg)
	defer func() { cancel(); <-done }()
	loadViaOp(t, b, commit)
	go tm.play(ctx)
	waitFor(t, 60*time.Second, "all missions integrated", func() bool { return allIntegrated(b) })

	ms, _ := Store{B: b}.Missions(context.Background(), "chg")
	if ms["a"].Current.Base != ms["b"].Current.Base {
		t.Errorf("a and b were not requested from the same head: %s vs %s", ms["a"].Current.Base, ms["b"].Current.Base)
	}
	g := Git{Repo: cfg.Repo}
	ctx2 := context.Background()
	for _, dep := range []string{"a", "b"} {
		if ok, _ := g.IsAncestor(ctx2, ms[dep].Current.Commit, ms["c"].Current.Base); !ok {
			t.Errorf("c's base %s does not contain %s's commit", short(ms["c"].Current.Base), dep)
		}
	}
	out, _ := gitRun(cfg.Repo, "log", "--merges", "--format=%(trailers:key=Mission-Hash,valueonly)", "change/chg")
	if n := len(lines(out)); n != 3 {
		t.Errorf("%d merge commits with Mission-Hash, want 3:\n%s", n, out)
	}
	if mainAfter, _ := g.ResolveCommit(ctx2, "main"); mainAfter != mainBefore {
		t.Error("main moved")
	}
	if open, _ := b.OpenChallenges(ctx2, "codex"); len(open) != 0 {
		t.Errorf("gates left open: %v", open)
	}
}

func TestEndToEndRestartMidCheck(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "b-check-started")
	t.Setenv("MARK", mark)
	b, cfg, commit, tm := e2eSetup(t, `touch $MARK; sleep 2; true`)
	teamCtx, stopTeam := context.WithCancel(context.Background())
	defer stopTeam()

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := startRun(t, ctx1, b, cfg)
	loadViaOp(t, b, commit)
	go tm.play(teamCtx)
	waitFor(t, 30*time.Second, "b's check started", func() bool { _, err := os.Stat(mark); return err == nil })
	cancel1()
	if err := <-done1; err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if ms, _ := (Store{B: b}).Missions(context.Background(), "chg"); ms["b"].State != Checking {
		t.Fatalf("the cancel did not land mid-check: b is %s", ms["b"].State)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := startRun(t, ctx2, b, cfg)
	defer func() { cancel2(); <-done2 }()
	waitFor(t, 60*time.Second, "all missions integrated after the restart", func() bool { return allIntegrated(b) })
	out, _ := gitRun(cfg.Repo, "log", "--merges", "--format=%(trailers:key=Mission,valueonly)", "change/chg")
	if n := strings.Count(out, "chg/b"); n != 1 {
		t.Fatalf("b integrated %d times:\n%s", n, out)
	}
}
