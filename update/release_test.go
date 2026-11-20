package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sh runs git in dir, failing the test on error; it returns the output.
func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "f")
	sh(t, dir, "commit", "-qm", msg)
	return sh(t, dir, "rev-parse", "HEAD")
}

// world is an upstream with v0.1.0 and v0.2.0 on main and a stray v0.9.0
// on another branch, and the person's clone of it (origin = upstream),
// checked out on a branch of their own.
type world struct{ up, clone, v1, v2 string }

func newWorld(t *testing.T) world {
	t.Helper()
	up := t.TempDir()
	sh(t, up, "init", "-q", "-b", "main")
	v1 := commit(t, up, "one")
	sh(t, up, "tag", "v0.1.0")
	v2 := commit(t, up, "two")
	sh(t, up, "tag", "v0.2.0")
	sh(t, up, "checkout", "-q", "-b", "side")
	commit(t, up, "stray")
	sh(t, up, "tag", "v0.9.0")
	sh(t, up, "checkout", "-q", "main")
	clone := filepath.Join(t.TempDir(), "clone")
	sh(t, filepath.Dir(clone), "clone", "-q", up, clone)
	sh(t, clone, "checkout", "-q", "-b", "mine", v1)
	return world{up: up, clone: clone, v1: v1, v2: v2}
}

// TestFindRelease: the newest vX.Y.Z on origin/main, never the stray tag on
// another branch; forward only (update spec §3.1).
func TestFindRelease(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for _, c := range []struct {
		version, tag, from string
		err                error
	}{
		{"v0.1.0", "v0.2.0", "v0.1.0", nil},
		{"v0.2.0", "", "", ErrUpToDate},
		{"v0.2.0-3-gabcdef0", "", "", ErrUpToDate},         // ahead of the newest release
		{"v0.0.9-3-g" + w.v1[:7], "v0.2.0", w.v1[:7], nil}, // an ancestor of the tag: allowed
		{"dev", "", "", ErrNotRelease},
		{"v0.1.0-dirty", "", "", ErrNotRelease},
		{"v0.1.0-2-g" + strings.Repeat("a", 7), "", "", ErrNotRelease}, // not an ancestor: a commit would be lost
	} {
		r, err := Find(ctx, w.clone, c.version)
		if !errors.Is(err, c.err) || (err == nil && (r.Tag != c.tag || r.FromRev != c.from)) {
			t.Errorf("%s: got %+v, %v; want %s from %s, %v", c.version, r, err, c.tag, c.from, c.err)
		}
	}
	// the clone is untouched: its branch and HEAD
	if b := sh(t, w.clone, "rev-parse", "--abbrev-ref", "HEAD"); b != "mine" || sh(t, w.clone, "rev-parse", "HEAD") != w.v1 {
		t.Fatalf("clone moved: %s", b)
	}
}

// TestFindNewTagFetched: a tag pushed upstream after the clone is fetched.
func TestFindNewTagFetched(t *testing.T) {
	w := newWorld(t)
	sh(t, w.up, "checkout", "-q", "main")
	commit(t, w.up, "three")
	sh(t, w.up, "tag", "v0.3.0")
	r, err := Find(context.Background(), w.clone, "v0.2.0")
	if err != nil || r.Tag != "v0.3.0" {
		t.Fatalf("%+v %v", r, err)
	}
}

// TestFindRepoRefused: no repository stamped (a go install build) or one
// that is gone refuses, naming ./setup.sh update; a release tree whose clone
// was moved or deleted says the tree is a worktree of that clone (the
// person's answer 1).
func TestFindRepoRefused(t *testing.T) {
	ctx := context.Background()
	for _, repo := range []string{"", filepath.Join(t.TempDir(), "gone")} {
		if _, err := Find(ctx, repo, "v0.1.0"); !errors.Is(err, ErrNoRepo) || !strings.Contains(err.Error(), "./setup.sh update") {
			t.Errorf("%q: %v", repo, err)
		}
	}
	w := newWorld(t)
	tree := filepath.Join(t.TempDir(), "v0.1.0")
	sh(t, w.clone, "worktree", "add", "-q", "--detach", tree, "v0.1.0")
	moved := w.clone + ".moved"
	if err := os.Rename(w.clone, moved); err != nil {
		t.Fatal(err)
	}
	_, err := Find(ctx, tree, "v0.1.0")
	if !errors.Is(err, ErrNoRepo) || !strings.Contains(err.Error(), "worktree of your clone") || !strings.Contains(err.Error(), w.clone) {
		t.Fatalf("moved clone: %v", err)
	}
	if err := os.Rename(moved, w.clone); err != nil {
		t.Fatal(err)
	}
	if r, err := Find(ctx, tree, "v0.1.0"); err != nil || r.Tag != "v0.2.0" || r.GitDir != filepath.Join(w.clone, ".git") {
		t.Fatalf("from a tree: %+v %v", r, err)
	}
}
