package mergequeue

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv isolates git from the person's configuration.
func gitEnv(t *testing.T) []string {
	home := t.TempDir()
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "LC_ALL=C"}
}

func git(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// world: a bare origin, the person's clone (work) and a pusher clone, main
// with one commit, a branch feat/a with one commit pushed.
type gitWorld struct {
	env                  []string
	origin, work, pusher string
	featA                string
}

func newGitWorld(t *testing.T) gitWorld {
	t.Helper()
	env := gitEnv(t)
	root := t.TempDir()
	w := gitWorld{env: env, origin: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work"), pusher: filepath.Join(root, "pusher")}
	git(t, env, root, "init", "-q", "--bare", "-b", "main", w.origin)
	git(t, env, root, "clone", "-q", w.origin, w.pusher)
	os.WriteFile(filepath.Join(w.pusher, "f"), []byte("1\n"), 0o644)
	git(t, env, w.pusher, "add", "f")
	git(t, env, w.pusher, "commit", "-q", "-m", "one")
	git(t, env, w.pusher, "push", "-q", "origin", "main")
	git(t, env, w.pusher, "switch", "-q", "-c", "feat/a")
	os.WriteFile(filepath.Join(w.pusher, "a"), []byte("a\n"), 0o644)
	git(t, env, w.pusher, "add", "a")
	git(t, env, w.pusher, "commit", "-q", "-m", "a")
	git(t, env, w.pusher, "push", "-q", "origin", "feat/a")
	w.featA = git(t, env, w.pusher, "rev-parse", "HEAD")
	git(t, env, root, "clone", "-q", w.origin, w.work)
	return w
}

func TestRemoteTips(t *testing.T) {
	w := newGitWorld(t)
	tips, base, err := RemoteTips(context.Background(), w.work, w.env, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(tips) != 1 || tips[0].Branch != "feat/a" || tips[0].Head != w.featA || base == "" {
		t.Fatalf("tips %+v base %q", tips, base)
	}
}

// The objects-only fetch writes no ref, refs/remotes and origin/HEAD
// included, and still brings the base's objects (foureyes).
func TestFetchBaseWritesNoRef(t *testing.T) {
	w := newGitWorld(t)
	git(t, w.env, w.pusher, "switch", "-q", "main")
	os.WriteFile(filepath.Join(w.pusher, "f"), []byte("2\n"), 0o644)
	git(t, w.env, w.pusher, "commit", "-q", "-am", "two")
	git(t, w.env, w.pusher, "push", "-q", "origin", "main")
	newMain := git(t, w.env, w.pusher, "rev-parse", "HEAD")
	before := git(t, w.env, w.work, "for-each-ref")
	if err := FetchBase(context.Background(), w.work, w.env, "main"); err != nil {
		t.Fatal(err)
	}
	if after := git(t, w.env, w.work, "for-each-ref"); after != before {
		t.Fatalf("refs moved:\n%s\n---\n%s", before, after)
	}
	git(t, w.env, w.work, "cat-file", "-e", newMain+"^{commit}")
}

// In the base: an ancestor (merged by a merge commit), or a head whose
// contents the base already has (a squash), not a branch still ahead.
func TestInBase(t *testing.T) {
	w := newGitWorld(t)
	ctx := context.Background()
	_, base, err := RemoteTips(ctx, w.work, w.env, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := FetchBase(ctx, w.work, w.env, "main"); err != nil {
		t.Fatal(err)
	}
	git(t, w.env, w.work, "fetch", "-q", "origin", "feat/a") // the approved head is local, as a reviewed one is
	in, err := InBase(ctx, w.work, w.env, base, []string{w.featA})
	if err != nil || in[w.featA] {
		t.Fatalf("ahead: %v %v", in, err)
	}
	// A squash of feat/a onto main: its contents are in the base, not its commit.
	git(t, w.env, w.pusher, "switch", "-q", "main")
	git(t, w.env, w.pusher, "merge", "-q", "--squash", "feat/a")
	git(t, w.env, w.pusher, "commit", "-q", "-m", "squash a")
	git(t, w.env, w.pusher, "push", "-q", "origin", "main")
	_, base, _ = RemoteTips(ctx, w.work, w.env, "main")
	if err := FetchBase(ctx, w.work, w.env, "main"); err != nil {
		t.Fatal(err)
	}
	in, err = InBase(ctx, w.work, w.env, base, []string{w.featA})
	if err != nil || !in[w.featA] {
		t.Fatalf("squashed: %v %v", in, err)
	}
	// A merge commit: an ancestor.
	git(t, w.env, w.pusher, "switch", "-q", "-c", "feat/b", "main")
	os.WriteFile(filepath.Join(w.pusher, "b"), []byte("b\n"), 0o644)
	git(t, w.env, w.pusher, "add", "b")
	git(t, w.env, w.pusher, "commit", "-q", "-m", "b")
	featB := git(t, w.env, w.pusher, "rev-parse", "HEAD")
	git(t, w.env, w.pusher, "push", "-q", "origin", "feat/b")
	git(t, w.env, w.pusher, "switch", "-q", "main")
	git(t, w.env, w.pusher, "merge", "-q", "--no-ff", "-m", "merge b", "feat/b")
	git(t, w.env, w.pusher, "push", "-q", "origin", "main")
	git(t, w.env, w.work, "fetch", "-q", "origin", "feat/b")
	_, base, _ = RemoteTips(ctx, w.work, w.env, "main")
	FetchBase(ctx, w.work, w.env, "main")
	if in, err := InBase(ctx, w.work, w.env, base, []string{featB}); err != nil || !in[featB] {
		t.Fatalf("merged: %v %v", in, err)
	}
	// A head the clone does not have cannot be judged: an error, never "not merged".
	if _, err := InBase(ctx, w.work, w.env, base, []string{"dddddddddddddddddddddddddddddddddddddddd"}); err == nil {
		t.Fatal("a missing head judged")
	}
}

// The local forge: local branches and their tips, the base apart.
func TestLocalTips(t *testing.T) {
	w := newGitWorld(t)
	git(t, w.env, w.work, "branch", "feat/a", "origin/feat/a")
	tips, base, err := LocalTips(context.Background(), w.work, w.env, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(tips) != 1 || tips[0].Branch != "feat/a" || tips[0].Head != w.featA || base == "" {
		t.Fatalf("tips %+v base %q", tips, base)
	}
}
