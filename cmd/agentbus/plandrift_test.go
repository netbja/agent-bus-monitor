package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
)

// plan-drift read from a live project (test Redis), git faked: a merge no
// task links warns and wakes master once; linked, or ignored, it no longer
// warns; a stale fetch reads unknown.
func TestPlanDriftReadsALiveProject(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	if err := w.b.ClearPlan(ctx); err != nil { // the stall world loads one of its own
		t.Fatal(err)
	}
	if out := w.run(w.s.planDrift); out.Status != health.OK || out.Detail != "no plan loaded" {
		t.Fatalf("no plan: %+v", out)
	}
	file := `{"plan":"v","tasks":[{"id":"a","title":"A","owner":"coder","board":"a"},{"id":"b","title":"B","owner":"coder","board":"b"}]}`
	if err := w.b.LoadPlan(ctx, "docs/plans/v.json", "abc1234", []byte(file), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := w.b.PlanLink(ctx, "a", 465); err != nil {
		t.Fatal(err)
	}
	if err := w.b.BoardClaim(ctx, "a", "coder", "feat/a"); err != nil {
		t.Fatal(err)
	}
	var revArgs []string
	w.s.planGit = func(_ context.Context, args ...string) (string, error) {
		revArgs = args
		return "commit 1111\nMerge pull request #465 from netbja/feat/a\ncommit 2222\nMerge pull request #466 from netbja/docs/notes\n", nil
	}
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return 5 * time.Minute, nil }
	out := w.run(w.s.planDrift)
	if out.Status != health.Warn || !strings.Contains(out.Detail, "1 merges not in the plan: #466") {
		t.Fatalf("drift: %+v", out)
	}
	if got := strings.Join(revArgs, " "); !strings.Contains(got, "-c core.fsmonitor=false -c core.pager=cat rev-list --first-parent --format=%s") ||
		!strings.HasSuffix(got, "abc1234..origin/main") {
		t.Fatalf("git %q", got)
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "plan-drift"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.dispatchDirectives("master"); len(got) != 1 || !strings.Contains(got[0], "Plan drift: 1 merges not in the plan: #466") {
		t.Fatalf("master woken %q", got)
	}
	if err := w.b.PlanIgnore(ctx, 466); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.planDrift); out.Status != health.OK {
		t.Fatalf("ignored: %+v", out)
	}
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return 3 * time.Hour, nil }
	if out := w.run(w.s.planDrift); out.Status != health.Unknown || !strings.Contains(out.Detail, "last fetched 3h ago") {
		t.Fatalf("stale: %+v", out)
	}
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return 0, errors.New("no FETCH_HEAD") }
	if out := w.run(w.s.planDrift); out.Status != health.Unknown {
		t.Fatalf("no fetch: %+v", out)
	}
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return time.Minute, nil }
	w.s.planGit = func(context.Context, ...string) (string, error) { return "", errors.New("bad revision") }
	if out := w.run(w.s.planDrift); out.Status != health.Unknown || !strings.Contains(out.Detail, "git rev-list") {
		t.Fatalf("git failed: %+v", out)
	}
}

// A plan whose recorded commit is no commit id never reaches git: never an
// option or a range git would read otherwise.
func TestPlanDriftRefusesABadPlanCommit(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	file := `{"plan":"v","tasks":[{"id":"a","title":"A","owner":"coder"}]}`
	w.b.LoadPlan(ctx, "p.json", "HEAD", []byte(file), []string{"a"})
	ran := false
	w.s.planGit = func(context.Context, ...string) (string, error) { ran = true; return "", nil }
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return time.Minute, nil }
	if out := w.run(w.s.planDrift); out.Status != health.Unknown || !strings.Contains(out.Detail, "not a commit id") || ran {
		t.Fatalf("%+v ran %v", out, ran)
	}
}

// The plan's branch cross-check reads the board's recorded branch.
func TestPlanDriftBranchMismatch(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	file := `{"plan":"v","tasks":[{"id":"a","title":"A","owner":"coder","board":"a"}]}`
	w.b.LoadPlan(ctx, "p.json", "abc1234", []byte(file), []string{"a"})
	w.b.PlanLink(ctx, "a", 465)
	w.b.BoardClaim(ctx, "a", "coder", "feat/other")
	w.s.planGit = func(context.Context, ...string) (string, error) {
		return "commit 1111\nMerge pull request #465 from netbja/feat/a\n", nil
	}
	w.s.fetchAge = func(context.Context) (time.Duration, error) { return time.Minute, nil }
	if out := w.run(w.s.planDrift); out.Status != health.Warn || !strings.Contains(out.Detail, "task a linked to #465, merged from feat/a, delivered on feat/other") {
		t.Fatalf("%+v", out)
	}
}

// The real git read, on a throwaway repository: the merges after the plan's
// commit on origin/main's first parent, and FETCH_HEAD's age.
func TestPlanDriftRealGit(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitRead(context.Background(), dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "start")
	base := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "Merge pull request #7 from o/feat/x")
	git("commit", "-q", "--allow-empty", "-m", "a squash (#8)")
	git("commit", "-q", "--allow-empty", "-m", "Merge pull request #9 from o/feat/y")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("remote", "add", "origin", "git@github.com:o/r.git")
	tip := git("rev-parse", "origin/main")
	fetched := []byte(tip + "\t\tbranch 'main' of github.com:o/r\n") // a fetch that saw origin's main at origin/main
	out, err := gitRead(context.Background(), dir, append(append([]string{}, gitSafe...), "rev-list", "--first-parent", "--format=%s", "--max-count=500", base+"..origin/main")...)
	if err != nil {
		t.Fatal(err)
	}
	m := health.ParseMerges(out)
	if len(m) != 2 || m[0].PR != 9 || m[0].Branch != "feat/y" || m[1].PR != 7 {
		t.Fatalf("merges %+v from %q", m, out)
	}
	if _, err := fetchAgeIn(context.Background(), dir); err == nil {
		t.Fatal("an age without FETCH_HEAD")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "FETCH_HEAD"), fetched, 0o644); err != nil {
		t.Fatal(err)
	}
	if age, err := fetchAgeIn(context.Background(), dir); err != nil || age > time.Minute {
		t.Fatalf("age %v %v", age, err)
	}
	// FETCH_HEAD is per worktree (foureyes): a fetch in a linked worktree,
	// where the agents work, makes origin/main fresh for the main tree too.
	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "--detach", wt)
	if err := os.Remove(filepath.Join(dir, ".git", "FETCH_HEAD")); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchAgeIn(context.Background(), dir); err == nil {
		t.Fatal("an age with no FETCH_HEAD anywhere")
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.WriteFile(filepath.Join(dir, ".git", "FETCH_HEAD"), fetched, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(filepath.Join(dir, ".git", "FETCH_HEAD"), old, old)
	wtGit, err := gitRead(context.Background(), wt, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(wtGit), "FETCH_HEAD"), fetched, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{dir, wt} {
		if age, err := fetchAgeIn(context.Background(), root); err != nil || age > time.Minute {
			t.Fatalf("from %s: age %v %v, want the worktree's fresh fetch", root, age, err)
		}
	}
	// reviewer-codex: a fresh FETCH_HEAD is no proof of origin/main by
	// itself. A fetch of another branch, of another remote, or one that saw
	// main at another commit than origin/main holds is no evidence: unknown.
	for name, body := range map[string]string{
		"another branch": tip + "\t\tbranch 'feature/other' of github.com:o/r\n",
		"another remote": tip + "\t\tbranch 'main' of github.com:someone/fork\n",
		"main elsewhere": strings.Repeat("a", 40) + "\t\tbranch 'main' of github.com:o/r\n",
		"no main line":   "garbage\n",
	} {
		for _, f := range []string{filepath.Join(dir, ".git", "FETCH_HEAD"), filepath.Join(strings.TrimSpace(wtGit), "FETCH_HEAD")} {
			if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if age, err := fetchAgeIn(context.Background(), dir); err == nil {
			t.Fatalf("%s: a fresh FETCH_HEAD read as origin/main fetched %v ago", name, age)
		}
	}
	// The evidence is the newest FETCH_HEAD that shows origin's main: a
	// newer fetch of another branch does not hide an older fetch of main.
	if err := os.WriteFile(filepath.Join(dir, ".git", "FETCH_HEAD"), fetched, 0o644); err != nil {
		t.Fatal(err)
	}
	ten := time.Now().Add(-10 * time.Minute)
	os.Chtimes(filepath.Join(dir, ".git", "FETCH_HEAD"), ten, ten)
	if age, err := fetchAgeIn(context.Background(), dir); err != nil || age < 9*time.Minute || age > 11*time.Minute {
		t.Fatalf("the main fetch 10 min ago behind a newer branch fetch: %v %v", age, err)
	}
	// An https remote reads the same as the scp form.
	for _, url := range []string{"https://github.com/o/r.git", "ssh://git@github.com/o/r", "https://github.com/o/r/"} {
		git("remote", "set-url", "origin", url)
		if err := os.WriteFile(filepath.Join(dir, ".git", "FETCH_HEAD"), []byte(tip+"\t\tbranch 'main' of https://github.com/o/r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := fetchAgeIn(context.Background(), dir); err != nil {
			t.Fatalf("origin %s: %v", url, err)
		}
	}
	git("remote", "set-url", "origin", "git@github.com:o/r.git")
	if validCommit("--all") || validCommit("HEAD") || validCommit("abc") || !validCommit(base) {
		t.Fatal("validCommit")
	}
	// The output is capped: more than the cap is an error, never a partial read.
	big := strings.Repeat("x", planDriftOutMax+10)
	if err := os.WriteFile(filepath.Join(dir, "big"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "big")
	git("commit", "-q", "-m", "big")
	if _, err := gitRead(context.Background(), dir, "show", "HEAD:big"); err == nil || !strings.Contains(err.Error(), "past") {
		t.Fatalf("an output past the cap: %v", err)
	}
	// reviewer-codex: the cap stops git while it writes, never after
	// buffering everything: an output that never ends returns at the cap,
	// long before the context's end.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err = gitRead(ctx, dir, "-c", "alias.flood=!yes", "flood")
	if err == nil || !strings.Contains(err.Error(), "past") || time.Since(start) > 5*time.Second {
		t.Fatalf("an endless output: %v after %v", err, time.Since(start))
	}
}
