package dispatchsvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// board-drift §5.1 on real git, local mode: an accepted request on a
// squash-merged branch and a review request with no review_base on a merged
// branch are open with a merged branch; a review request with no branch tip
// and no commit cannot be judged; an accepted request on a fresh branch (no
// commit of its own), one on unmerged work, and a request in another state
// are not findings.
func TestBoardDriftMergedRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "LC_ALL=C"}
	g := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commitFile := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		g("add", name)
		g("commit", "-q", "-m", name)
	}
	g("init", "-q", "-b", "main")
	commitFile("base")
	for _, br := range []string{"squashed", "nobase", "wip", "blocked"} {
		g("switch", "-q", "-c", "feat/"+br, "main")
		commitFile(br)
	}
	g("switch", "-q", "main")
	g("merge", "-q", "--squash", "feat/squashed")
	g("commit", "-q", "-m", "squash of feat/squashed")
	g("merge", "-q", "--no-ff", "-m", "merge nobase", "feat/nobase")
	g("merge", "-q", "--squash", "feat/blocked")
	g("commit", "-q", "-m", "squashed blocked")
	g("branch", "feat/fresh", "main") // branched, nothing committed yet

	c, b := mqChore(t, "")
	ctx := context.Background()
	reviewed(t, b, "squashed", bus.ReviewOpts{Branch: "feat/squashed"})
	if err := b.AcceptRequest(ctx, "squashed", "coder"); err != nil { // resumed: accepted, its branch kept
		t.Fatal(err)
	}
	reviewed(t, b, "nobase", bus.ReviewOpts{Branch: "feat/nobase"})
	reviewed(t, b, "wip", bus.ReviewOpts{Branch: "feat/wip", ReviewBase: g("rev-parse", "main")})
	reviewed(t, b, "fresh", bus.ReviewOpts{Branch: "feat/fresh"})
	if err := b.AcceptRequest(ctx, "fresh", "coder"); err != nil {
		t.Fatal(err)
	}
	reviewed(t, b, "unjudged", bus.ReviewOpts{Branch: "feat/deleted"})
	reviewed(t, b, "blocked", bus.ReviewOpts{Branch: "feat/blocked"})
	if err := b.BlockRequest(ctx, "blocked", "coder", "waits"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "nobranch", "master", "coder", "", "x", 0); err != nil { // accepted, no branch yet: nothing to judge
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "nobranch", "coder"); err != nil {
		t.Fatal(err)
	}
	c.dir = dir
	c.environ = func() []string { return env }
	c.localTips, c.localBranches, c.inBase = mergequeue.LocalTips, mergequeue.LocalBranches, mergequeue.InBase
	c.delivered = mergequeue.NewDelivery().Delivered
	got, why := c.driftMerged(ctx)
	if why != "" {
		t.Fatalf("not judged: %s", why)
	}
	want := []DriftMerged{
		{Task: "nobase", Owner: "coder", State: "review", Branch: "feat/nobase", Head: g("rev-parse", "feat/nobase"), Base: "main"},
		{Task: "squashed", Owner: "coder", State: "accepted", Branch: "feat/squashed", Head: g("rev-parse", "feat/squashed"), Base: "main"},
		{Task: "unjudged", Owner: "coder", State: "review", Branch: "feat/deleted", Unjudged: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// With a remote: origin's base is fetched and its tips read; a branch
// origin does not hold (never pushed, or deleted after its merge) is judged
// from the local refs, with no local base needed.
func TestBoardDriftMergedRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "LC_ALL=C"}
	origin, dir := t.TempDir(), t.TempDir()
	gitAt := func(at string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = at, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g := func(args ...string) string { t.Helper(); return gitAt(dir, args...) }
	commitFile := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		g("add", name)
		g("commit", "-q", "-m", name)
	}
	gitAt(origin, "init", "-q", "--bare", "-b", "dev")
	g("init", "-q", "-b", "dev")
	g("remote", "add", "origin", origin)
	commitFile("base")
	g("push", "-q", "origin", "dev")
	for _, br := range []string{"local", "unmerged"} {
		g("switch", "-q", "-c", "feat/"+br, "dev")
		commitFile(br)
	}
	g("switch", "-q", "dev")
	g("merge", "-q", "--squash", "feat/local")
	g("commit", "-q", "-m", "squash of feat/local")
	g("push", "-q", "origin", "dev")
	g("switch", "-q", "-c", "work")
	g("branch", "-D", "dev") // no local base

	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	ctx := context.Background()
	for _, task := range []string{"local", "unmerged"} {
		if _, err := b.Request(ctx, task, "master", "coder", "", "x", 0); err != nil {
			t.Fatal(err)
		}
		if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
			t.Fatal(err)
		}
		if err := b.ReviewRequestOpts(ctx, task, "coder", bus.ReviewOpts{Branch: "feat/" + task}); err != nil {
			t.Fatal(err)
		}
		if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
			t.Fatal(err)
		}
	}
	c.dir = dir
	c.environ = func() []string { return env }
	baseKey, _ := config.Lookup("delivery.base")
	c.getenv = func(k string) string {
		if k == baseKey.Env {
			return "dev"
		}
		return ""
	}
	c.remoteTips, c.fetchBase, c.inBase = mergequeue.RemoteTips, mergequeue.FetchBase, mergequeue.InBase
	c.localBranches = mergequeue.LocalBranches
	c.delivered = mergequeue.NewDelivery().Delivered
	got, why := c.driftMerged(ctx)
	if why != "" {
		t.Fatalf("not judged: %s", why)
	}
	want := DriftMerged{Task: "local", Owner: "coder", State: "accepted", Branch: "feat/local", Head: g("rev-parse", "feat/local"), Base: "dev"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
