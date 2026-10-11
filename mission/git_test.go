package mission

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// newRepo makes a repository on branch main with one commit (README.md).
func newRepo(t *testing.T) (Git, string) {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q", "-b", "main")
	commitFile(t, dir, "README.md", "hello\n", "init")
	return Git{Repo: dir}, dir
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFile writes rel and commits it in dir; returns the new commit.
func commitFile(t *testing.T, dir, rel, body, msg string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", rel)
	run(t, dir, "git", "commit", "-q", "-m", msg)
	return run(t, dir, "git", "rev-parse", "HEAD")
}

func TestDiffNamesAndAncestor(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	base := run(t, dir, "git", "rev-parse", "HEAD")
	c := commitFile(t, dir, "cmd/a.go", "package a\n", "a")
	names, err := g.DiffNames(ctx, base, c)
	if err != nil || strings.Join(names, ",") != "cmd/a.go" {
		t.Fatalf("DiffNames = %v %v", names, err)
	}
	if ok, err := g.IsAncestor(ctx, base, c); err != nil || !ok {
		t.Fatalf("IsAncestor(base, c) = %v %v", ok, err)
	}
	if ok, _ := g.IsAncestor(ctx, c, base); ok {
		t.Fatal("IsAncestor(c, base) true")
	}
	if tree, _ := g.Tree(ctx, c); strings.Join(tree, ",") != "README.md,cmd/a.go" {
		t.Fatalf("Tree = %v", tree)
	}
	if b, err := g.Show(ctx, c, "cmd/a.go"); err != nil || string(b) != "package a\n" {
		t.Fatalf("Show = %q %v", b, err)
	}
}

func TestEnsureBranchNeverMoves(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	first := run(t, dir, "git", "rev-parse", "HEAD")
	if err := g.EnsureBranch(ctx, "change/x", "main"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, dir, "b.txt", "b", "b") // main moves on
	if err := g.EnsureBranch(ctx, "change/x", "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.ResolveCommit(ctx, "change/x"); got != first {
		t.Fatalf("existing branch moved: %s != %s", got, first)
	}
}

func TestMergeNoFFTrailers(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	g.EnsureBranch(ctx, "change/x", "main")
	run(t, dir, "git", "checkout", "-q", "-b", "mission/t1")
	c := commitFile(t, dir, "a.txt", "a", "a")
	run(t, dir, "git", "checkout", "-q", "main")
	wt := filepath.Join(t.TempDir(), "integration")
	if err := g.AddWorktree(ctx, wt, "change/x", false); err != nil {
		t.Fatal(err)
	}
	msg := MergeMessage("x", "t1", 1, "Title", c, "h123")
	if err := g.MergeNoFF(ctx, wt, c, msg); err != nil {
		t.Fatal(err)
	}
	if v := run(t, wt, "git", "log", "-1", "--format=%(trailers:key=Mission-Hash,valueonly)"); v != "h123" {
		t.Fatalf("trailer = %q", v)
	}
	if v := run(t, wt, "git", "log", "-1", "--format=%(trailers:key=Mission,valueonly)"); v != "x/t1" {
		t.Fatalf("Mission trailer = %q", v)
	}
	if ok, _ := g.BranchHasTrailer(ctx, "change/x", "Mission-Commit", c); !ok {
		t.Fatal("BranchHasTrailer missed the merge")
	}
}

func TestMergeConflictLeavesMergeInProgress(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	g.EnsureBranch(ctx, "change/x", "main")
	run(t, dir, "git", "checkout", "-q", "-b", "mission/t1")
	c := commitFile(t, dir, "README.md", "mission side\n", "m")
	run(t, dir, "git", "checkout", "-q", "main")
	wt := filepath.Join(t.TempDir(), "integration")
	g.AddWorktree(ctx, wt, "change/x", false)
	commitFile(t, wt, "README.md", "change side\n", "c")
	head, _ := g.Head(ctx, wt)
	if err := g.MergeNoFF(ctx, wt, c, "m"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if in, _ := g.MergeInProgress(wt); !in {
		t.Fatal("merge not in progress")
	}
	if err := g.MergeAbort(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if dirty, _ := g.Dirty(ctx, wt); dirty {
		t.Fatal("dirty after abort")
	}
	if h, _ := g.Head(ctx, wt); h != head {
		t.Fatal("head moved")
	}
}

func TestRunTestPassFail(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	if ok, _, err := RunTest(context.Background(), dir, "true", log, time.Minute); err != nil || !ok {
		t.Fatalf("true: %v %v", ok, err)
	}
	if ok, tail, err := RunTest(context.Background(), dir, "echo boom; false", log, time.Minute); err != nil || ok || !strings.Contains(tail, "boom") {
		t.Fatalf("false: %v %q %v", ok, tail, err)
	}
}

func TestRunTestTimeoutKillsGroup(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	ok, tail, _ := RunTest(context.Background(), dir, "sleep 30 & echo $! > child.pid; echo $$ > parent.pid; sleep 30", filepath.Join(dir, "log"), time.Second)
	if ok || time.Since(start) > 7*time.Second || !strings.HasSuffix(strings.TrimSpace(tail), "timeout after 1s") {
		t.Fatalf("ok %v after %v, tail %q", ok, time.Since(start), tail)
	}
	for _, f := range []string{"child.pid", "parent.pid"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("%s %d still alive (%v)", f, pid, err)
		}
	}
}

func TestRunTestTailBounded(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	_, tail, err := RunTest(context.Background(), dir, "yes | head -c 2000000", log, time.Minute)
	if err != nil || len(tail) > 4096 {
		t.Fatalf("tail %d bytes, err %v", len(tail), err)
	}
	if st, _ := os.Stat(log); st.Size() < 2000000 {
		t.Fatalf("log holds %d bytes", st.Size())
	}
}

func TestResolveCommitUnknown(t *testing.T) {
	g, _ := newRepo(t)
	if _, err := g.ResolveCommit(context.Background(), "deadbeef"); err == nil {
		t.Fatal("unknown commit resolved")
	}
}
