package dispatchsvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
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
