package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The repository check of an automatic restart (reboot-resume §3.1,
// condition 3): read-only, no repository-configured program run (the
// dispatcher never runs repository code): no index.lock in the git
// directory or any worktree's, and git fsck exits 0, within repoCheckLimit.

// repoCheckLimit bounds the whole check.
const repoCheckLimit = 5 * time.Minute

// gitRun runs git with args in the repository: its output, exit code (-1
// when it did not exit) and error.
type gitRun func(ctx context.Context, args ...string) ([]byte, int, error)

// gitRunner is the real one: git with core.fsmonitor off and hooks pointed
// at /dev/null, so nothing the repository configures runs.
func gitRunner(dir string) gitRun {
	return func(ctx context.Context, args ...string) ([]byte, int, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return out, code, err
	}
}

// repoIntegrity is why the repository at dir is not intact ("" intact). A
// directory with no .git is no repository: nothing to damage.
func repoIntegrity(ctx context.Context, dir string, git gitRun) string {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, repoCheckLimit)
	defer cancel()
	failed := func(code int, err error) string {
		if ctx.Err() != nil {
			return "git fsck timed out"
		}
		return fmt.Sprintf("git fsck failed (exit %d)", code)
	}
	out, code, err := git(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return failed(code, err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	locks := []string{filepath.Join(common, "index.lock")}
	if more, err := filepath.Glob(filepath.Join(common, "worktrees", "*", "index.lock")); err == nil {
		locks = append(locks, more...)
	}
	for _, l := range locks {
		if _, err := os.Lstat(l); err == nil {
			return "index.lock present: " + l
		}
	}
	if _, code, err := git(ctx, "fsck", "--no-dangling", "--no-progress"); err != nil || code != 0 {
		return failed(code, err)
	}
	return ""
}
