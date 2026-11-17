package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repoGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=charlie", "GIT_AUTHOR_EMAIL=charlie@example.invalid", "GIT_COMMITTER_NAME=charlie", "GIT_COMMITTER_EMAIL=charlie@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// testRepo is a repository with one commit and one worktree, whose
// fsmonitor and hooks would each write a file if they ran.
func testRepo(t *testing.T) (repo, wt, ran string) {
	t.Helper()
	root := t.TempDir()
	repo, wt, ran = filepath.Join(root, "repo"), filepath.Join(root, "wt"), filepath.Join(root, "ran")
	os.MkdirAll(repo, 0o755)
	repoGit(t, repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	repoGit(t, repo, "add", "a.txt")
	repoGit(t, repo, "commit", "-q", "-m", "a")
	repoGit(t, repo, "worktree", "add", "-q", wt)
	script := filepath.Join(root, "evil.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ran >> "+ran+"\n"), 0o755)
	repoGit(t, repo, "config", "core.fsmonitor", script)
	hooks := filepath.Join(root, "hooks")
	os.MkdirAll(hooks, 0o755)
	for _, h := range []string{"post-checkout", "reference-transaction", "post-index-change"} {
		os.WriteFile(filepath.Join(hooks, h), []byte("#!/bin/sh\necho ran >> "+ran+"\n"), 0o755)
	}
	repoGit(t, repo, "config", "core.hooksPath", hooks)
	os.Remove(ran)
	return repo, wt, ran
}

// reboot-resume §3.1 condition 3: an intact repository reads intact, and
// no repository-configured program runs (core.fsmonitor, hooks).
func TestRepoCheckIntact(t *testing.T) {
	repo, _, ran := testRepo(t)
	if why := repoIntegrity(context.Background(), repo, gitRunner(repo)); why != "" {
		t.Fatalf("intact read as %q", why)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		b, _ := os.ReadFile(ran)
		t.Fatalf("a repository-configured program ran: %q", b)
	}
}

// An index.lock in the main git directory, then in a worktree's.
func TestRepoCheckIndexLock(t *testing.T) {
	repo, wt, _ := testRepo(t)
	main := filepath.Join(repo, ".git", "index.lock")
	os.WriteFile(main, nil, 0o644)
	if why := repoIntegrity(context.Background(), repo, gitRunner(repo)); why != "index.lock present: "+main {
		t.Fatalf("main: %q", why)
	}
	os.Remove(main)
	wtLock := filepath.Join(repo, ".git", "worktrees", filepath.Base(wt), "index.lock")
	os.WriteFile(wtLock, nil, 0o644)
	if why := repoIntegrity(context.Background(), repo, gitRunner(repo)); why != "index.lock present: "+wtLock {
		t.Fatalf("worktree: %q", why)
	}
}

// git fsck failing (a damaged object) names its exit code.
func TestRepoCheckFsckFails(t *testing.T) {
	repo, _, _ := testRepo(t)
	objs := filepath.Join(repo, ".git", "objects")
	filepath.WalkDir(objs, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && len(filepath.Base(filepath.Dir(p))) == 2 {
			os.Chmod(p, 0o644)
			os.WriteFile(p, []byte("garbage"), 0o644)
		}
		return nil
	})
	if why := repoIntegrity(context.Background(), repo, gitRunner(repo)); !strings.HasPrefix(why, "git fsck failed (exit ") {
		t.Fatalf("damaged: %q", why)
	}
}

// git fsck past its limit: timed out.
func TestRepoCheckFsckTimesOut(t *testing.T) {
	repo, _, _ := testRepo(t)
	slow := func(ctx context.Context, args ...string) ([]byte, int, error) {
		if args[0] == "fsck" {
			<-ctx.Done()
			return nil, -1, ctx.Err()
		}
		return gitRunner(repo)(ctx, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if why := repoIntegrity(ctx, repo, slow); why != "git fsck timed out" {
		t.Fatalf("slow: %q", why)
	}
}

// A project directory that is no repository has nothing to damage.
func TestRepoCheckNoRepository(t *testing.T) {
	dir := t.TempDir()
	if why := repoIntegrity(context.Background(), dir, gitRunner(dir)); why != "" {
		t.Fatalf("no repository: %q", why)
	}
}
