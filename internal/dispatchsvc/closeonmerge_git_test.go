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

// board-drift §9's real-git test, in local mode: a merge, a squash and a
// rebase each close their review request; an accepted request on a merged
// branch, and a review request whose branch is not merged, stay open.
func TestCloseOnMergeRealGit(t *testing.T) {
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
	reviewBase := g("rev-parse", "main")
	for _, br := range []string{"merge", "squash", "rebase", "accepted", "wip"} {
		g("switch", "-q", "-c", "feat/"+br, "main")
		commitFile(br)
	}
	g("switch", "-q", "main")
	g("merge", "-q", "--no-ff", "-m", "merge", "feat/merge")
	g("merge", "-q", "--squash", "feat/squash")
	g("commit", "-q", "-m", "squashed")
	g("cherry-pick", "feat/rebase") // a rebase merge: the same change, another commit
	g("merge", "-q", "--no-ff", "-m", "merge accepted", "feat/accepted")

	c, b := mqChore(t, "")
	ctx := context.Background()
	for _, br := range []string{"merge", "squash", "rebase", "wip"} {
		reviewed(t, b, br, bus.ReviewOpts{Branch: "feat/" + br, ReviewBase: reviewBase})
	}
	reviewed(t, b, "accepted", bus.ReviewOpts{Branch: "feat/accepted", ReviewBase: reviewBase})
	if err := b.AcceptRequest(ctx, "accepted", "coder"); err != nil { // resumed for its next step
		t.Fatal(err)
	}
	c.dir, c.scratch = dir, t.TempDir()
	c.environ = func() []string { return env }
	c.localTips, c.inBase = mergequeue.LocalTips, mergequeue.InBase
	c.delivered = mergequeue.NewDelivery().Delivered
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	for task, want := range map[string]string{"merge": "done", "squash": "done", "rebase": "done", "accepted": "accepted", "wip": "review"} {
		s, r := stateOf(t, b, task)
		if s != want {
			t.Errorf("%s: %s, want %s", task, s, want)
		}
		if want == "done" && (r.ClosedOnMerge == nil || r.ClosedOnMerge.Head != g("rev-parse", "feat/"+task) || r.ClosedOnMerge.Base != "main") {
			t.Errorf("%s: closed %+v", task, r.ClosedOnMerge)
		}
	}
}

// reviewer-codex: with a remote, a checkout branched from origin/dev may
// have no local dev. A feature branch squash-merged and deleted on origin is
// then judged from the local refs all the same: its local tip when it has
// one (the reviewed commit), else its recorded commit.
func TestCloseOnMergeNoLocalBase(t *testing.T) {
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
	commitFile := func(name string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		g("add", name)
		g("commit", "-q", "-m", name)
		return g("rev-parse", "HEAD")
	}
	gitAt(origin, "init", "-q", "--bare", "-b", "dev")
	g("init", "-q", "-b", "dev")
	g("remote", "add", "origin", origin)
	commitFile("base")
	g("push", "-q", "origin", "dev")
	reviewBase := g("rev-parse", "dev")
	heads := map[string]string{}
	for _, br := range []string{"kept", "gone"} {
		g("switch", "-q", "-c", "feat/"+br, "dev")
		heads[br] = commitFile(br)
		g("switch", "-q", "dev")
		g("merge", "-q", "--squash", "feat/"+br)
		g("commit", "-q", "-m", "squash "+br)
	}
	g("push", "-q", "origin", "dev")
	g("switch", "-q", "-c", "work") // no local dev from here on
	g("branch", "-D", "dev", "feat/gone")

	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	ctx := context.Background()
	reviewed(t, b, "kept", bus.ReviewOpts{Branch: "feat/kept", Commit: heads["kept"], ReviewBase: reviewBase})
	reviewed(t, b, "gone", bus.ReviewOpts{Branch: "feat/gone", Commit: heads["gone"], ReviewBase: reviewBase})
	c.dir, c.scratch = dir, t.TempDir()
	c.environ = func() []string { return env }
	baseKey, _ := config.Lookup("delivery.base")
	c.getenv = func(k string) string {
		if k == baseKey.Env {
			return "dev"
		}
		return ""
	}
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) { return nil, nil }
	c.remoteTips, c.fetchBase, c.inBase = mergequeue.RemoteTips, mergequeue.FetchBase, mergequeue.InBase
	c.localTips, c.localBranches = mergequeue.LocalTips, mergequeue.LocalBranches
	c.delivered = mergequeue.NewDelivery().Delivered
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" {
		t.Fatalf("the pass failed: %+v", rec)
	}
	for _, task := range []string{"kept", "gone"} {
		if s, r := stateOf(t, b, task); s != "done" || r.ClosedOnMerge == nil || r.ClosedOnMerge.Head != heads[task] {
			t.Errorf("%s: %s %+v", task, s, r.ClosedOnMerge)
		}
	}
}
