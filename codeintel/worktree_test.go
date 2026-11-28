package codeintel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The porcelain -z list: each entry's path (a newline in it included),
// branch (empty when detached) and head; a bare or prunable entry has no
// working tree to route to.
func TestParseWorktrees(t *testing.T) {
	got := ParseWorktrees("worktree /r\x00HEAD aaa\x00branch refs/heads/dev\x00\x00" +
		"worktree /r/.worktrees/x\x00HEAD bbb\x00branch refs/heads/coder/x\x00\x00" +
		"worktree /tmp/rev\x00HEAD ccc\x00detached\x00\x00" +
		"worktree /bare\x00bare\x00\x00" +
		"worktree /n\nl\x00HEAD eee\x00branch refs/heads/nl\x00\x00" +
		"worktree /gone\x00HEAD ddd\x00branch refs/heads/old\x00prunable gitdir file points to non-existent location\x00\x00")
	want := []Worktree{{"/r", "dev", "aaa"}, {"/r/.worktrees/x", "coder/x", "bbb"}, {"/tmp/rev", "", "ccc"}, {"/n\nl", "nl", "eee"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A path belongs to the deepest listed root that contains it, compared by
// element: a nested worktree wins over the main checkout, and /a/b never
// contains /a/bc.
func TestTreesOwner(t *testing.T) {
	tr := Trees{Project: "p", List: []Worktree{{Path: "/a/b"}, {Path: "/a/b/.worktrees/x"}, {Path: "/a"}, {Path: "/c"}}}
	for path, want := range map[string]string{
		"/a/f.go": "/a", "/a/b": "/a/b", "/a/b/g/h.go": "/a/b", "/a/bc/f.go": "/a",
		"/a/b/.worktrees/x/f.go": "/a/b/.worktrees/x", "/a/b/.worktrees/xy/f.go": "/a/b",
		"/c": "/c", "/cd": "", "/": "", "/a/../c/f": "/c", "/a/..x/f.go": "/a",
	} {
		w, ok := tr.owner(path)
		if got := map[bool]string{true: w.Path}[ok]; got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// ForBranch maps a branch to the one worktree that has it checked out, and
// fails closed on none or several; a detached worktree has no branch.
func TestTreesForBranch(t *testing.T) {
	tr := Trees{Project: "p", List: []Worktree{{"/r", "dev", "a"}, {"/w1", "coder/x", "b"}, {"/w2", "coder/y", "c"}, {"/w3", "coder/y", "d"}, {"/d", "", "e"}}}
	if w, err := tr.ForBranch("coder/x"); err != nil || w.Path != "/w1" {
		t.Fatalf("one: %+v %v", w, err)
	}
	if _, err := tr.ForBranch("coder/z"); err == nil || err.Error() != "branch coder/z is checked out in no worktree of p" {
		t.Fatalf("none: %v", err)
	}
	if _, err := tr.ForBranch("coder/y"); err == nil || err.Error() != "branch coder/y is checked out in several worktrees of p: /w2, /w3" {
		t.Fatalf("several: %v", err)
	}
	if _, err := tr.ForBranch(""); err == nil {
		t.Fatal("empty branch matched a detached worktree")
	}
}

// A real repository: a worktree nested inside the main checkout, reached
// through a symlink, routes to itself; a path outside every worktree, a
// removed worktree's path and a missing file are refused; a branch maps
// to its worktree.
func TestListAndResolve(t *testing.T) {
	g := newRepo(t)
	nested := filepath.Join(g.dir, ".worktrees", "x")
	g.git("worktree", "add", "-q", "-b", "coder/x", nested)
	gone := filepath.Join(t.TempDir(), "gone")
	g.git("worktree", "add", "-q", "-b", "coder/gone", gone)
	os.RemoveAll(gone) // listed until a prune, but not there
	os.WriteFile(filepath.Join(nested, "f.go"), []byte("package x\n"), 0o644)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "o.go"), nil, 0o644)

	tr, err := List(context.Background(), "p", g.dir, g.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.List) != 2 {
		t.Fatalf("worktrees: %+v", tr.List)
	}
	realNested, _ := filepath.EvalSymlinks(nested)
	for _, p := range []string{filepath.Join(nested, "f.go"), filepath.Join(link, "f.go"), nested} {
		if w, err := tr.Resolve(p); err != nil || w.Path != realNested || w.Branch != "coder/x" {
			t.Fatalf("%s: %+v %v", p, w, err)
		}
	}
	for _, p := range []string{filepath.Join(outside, "o.go"), filepath.Join(gone, "f.go"), filepath.Join(nested, "missing.go")} {
		if w, err := tr.Resolve(p); err == nil || !strings.HasPrefix(err.Error(), "not a worktree of p: ") {
			t.Fatalf("%s: %+v %v", p, w, err)
		}
	}
	if w, err := tr.ForBranch("coder/x"); err != nil || w.Path != realNested {
		t.Fatalf("branch: %+v %v", w, err)
	}
	// a relative path, even one that resolves from the test's directory
	// inside the worktree, is refused: the relay's directory is not the
	// agent's (reviewer-codex).
	t.Chdir(nested)
	if w, err := tr.Resolve("f.go"); err == nil || !strings.HasPrefix(err.Error(), "not an absolute path: f.go") {
		t.Fatalf("relative: %+v %v", w, err)
	}
	if _, err := tr.ForBranch("coder/gone"); err == nil {
		t.Fatal("a removed worktree's branch was routed")
	}
	if _, err := List(context.Background(), "p", outside, g.env); err == nil {
		t.Fatal("not a repository: no error")
	}
}

// Each listed root is resolved: a root listed through a symlink routes a
// path under its real directory; a root that does not resolve is dropped.
func TestResolvedRoots(t *testing.T) {
	real := t.TempDir()
	os.WriteFile(filepath.Join(real, "f.go"), nil, 0o644)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tr := resolved("p", []Worktree{{Path: link, Branch: "coder/x"}, {Path: filepath.Join(real, "gone"), Branch: "coder/g"}})
	realDir, _ := filepath.EvalSymlinks(real)
	if len(tr.List) != 1 || tr.List[0].Path != realDir {
		t.Fatalf("%+v", tr.List)
	}
	if w, err := tr.Resolve(filepath.Join(realDir, "f.go")); err != nil || w.Branch != "coder/x" {
		t.Fatalf("%+v %v", w, err)
	}
}

// Stamp is the revision an answer read: head, branch (read at the stamp,
// detached empty), and whether the tree has uncommitted changes, an
// untracked file included.
func TestStamp(t *testing.T) {
	g := newRepo(t)
	ctx := context.Background()
	tr, err := List(ctx, "p", g.dir, g.env)
	if err != nil {
		t.Fatal(err)
	}
	w := tr.List[0]
	head := strings.TrimSpace(g.git("rev-parse", "HEAD"))
	if r, err := Stamp(ctx, w, g.env); err != nil || r != (Revision{w.Path, "dev", head, false}) {
		t.Fatalf("clean: %+v %v", r, err)
	}
	os.WriteFile(filepath.Join(g.dir, "new"), nil, 0o644)
	if r, err := Stamp(ctx, w, g.env); err != nil || !r.Dirty || r.Head != head {
		t.Fatalf("untracked: %+v %v", r, err)
	}
	os.Remove(filepath.Join(g.dir, "new"))
	os.WriteFile(filepath.Join(g.dir, "a"), []byte("2\n"), 0o644)
	if r, err := Stamp(ctx, w, g.env); err != nil || !r.Dirty {
		t.Fatalf("modified: %+v %v", r, err)
	}
	g.git("commit", "-qam", "two")
	head2 := strings.TrimSpace(g.git("rev-parse", "HEAD"))
	if r, err := Stamp(ctx, w, g.env); err != nil || r.Dirty || r.Head != head2 || head2 == head {
		t.Fatalf("committed: %+v %v", r, err)
	}
	g.git("switch", "-q", "-c", "coder/s")
	if r, err := Stamp(ctx, w, g.env); err != nil || r.Branch != "coder/s" || r.Head != head2 {
		t.Fatalf("switched after the list: %+v %v", r, err)
	}
	g.git("switch", "-q", "--detach")
	if r, err := Stamp(ctx, w, g.env); err != nil || r.Branch != "" || r.Head != head2 {
		t.Fatalf("detached: %+v %v", r, err)
	}
	if _, err := Stamp(ctx, Worktree{Path: t.TempDir()}, g.env); err == nil {
		t.Fatal("not a repository: no error")
	}
}

// Stamp never rewrites the agent's index (foureyes): on a tree whose stat
// data is stale, a plain git status would refresh it under index.lock. And
// no worktree config runs a command from it: core.fsmonitor is off.
func TestStampTakesNoLock(t *testing.T) {
	g := newRepo(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "fsmon")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	g.git("config", "core.fsmonitor", hook)
	index := filepath.Join(g.dir, ".git", "index")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(index, old, old)
	before, _ := os.ReadFile(index)
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(g.dir, "a"), later, later) // same bytes, new stat data
	tr, err := List(ctx, "p", g.dir, g.env)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Stamp(ctx, tr.List[0], g.env); err != nil || r.Dirty {
		t.Fatalf("%+v %v", r, err)
	}
	st, _ := os.Stat(index)
	after, _ := os.ReadFile(index)
	if !st.ModTime().Equal(old) || string(after) != string(before) {
		t.Fatalf("the index was rewritten: mtime %v", st.ModTime())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("core.fsmonitor ran")
	}
}

type repo struct {
	t   *testing.T
	dir string
	env []string
}

// newRepo is a disposable repository on branch dev with one commit, git
// isolated from the person's configuration.
func newRepo(t *testing.T) repo {
	t.Helper()
	home := t.TempDir()
	g := repo{t: t, dir: filepath.Join(t.TempDir(), "r"), env: []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	}}
	os.MkdirAll(g.dir, 0o755)
	g.git("init", "-q", "-b", "dev")
	os.WriteFile(filepath.Join(g.dir, "a"), []byte("1\n"), 0o644)
	os.WriteFile(filepath.Join(g.dir, ".gitignore"), []byte(".worktrees/\n"), 0o644)
	g.git("add", ".")
	g.git("commit", "-qm", "one")
	return g
}

func (g repo) git(args ...string) string {
	g.t.Helper()
	c := exec.Command("git", args...)
	c.Dir, c.Env = g.dir, g.env
	out, err := c.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
