// Package update is agentbus update, step 1 (spec
// docs/superpowers/specs/2026-11-20-agentbus-update-design.md): a manual
// update to the latest release tag, built in its own tree, a snapshot before
// any change, the person's yes, and a rollback by the snapshot's own binary.
package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/netbja/agent-bus-monitor/buildinfo"
)

var (
	// ErrNoRepo: no repository to update from (§3.1).
	ErrNoRepo = errors.New("no repository to update from")
	// ErrNotRelease: the running build is not a release (the person's answer 3).
	ErrNotRelease = errors.New("not a release build")
	// ErrUpToDate: no newer release on origin/main.
	ErrUpToDate = errors.New("up to date")
)

// Release is what Find chose: the tag to install, the commit the running
// build is at (for the skills diff), and the clone's git directory.
type Release struct {
	Tag     string
	FromRev string
	GitDir  string
}

var (
	plainTag  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	describes = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+)-[0-9]+-g([0-9a-f]{4,40})$`)
)

// git runs git, never prompting; the error carries its stderr.
func git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

const viaSetup = "update with ./setup.sh update from your clone"

// GitDir is the clone's git directory behind repo, the tree a binary was
// built from (buildinfo.Repo): the clone itself, or a release tree that is a
// worktree of it, whose history stays in the clone's .git (the person's
// answer 1). A tree whose clone was moved or deleted is refused, saying so.
func GitDir(ctx context.Context, repo string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("%w: this agentbus names no repository (not built by setup): %s", ErrNoRepo, viaSetup)
	}
	if _, err := os.Stat(repo); err != nil {
		return "", fmt.Errorf("%w: the repository this agentbus was built from, %s, is gone: %s", ErrNoRepo, repo, viaSetup)
	}
	if b, err := os.ReadFile(filepath.Join(repo, ".git")); err == nil { // a worktree: "gitdir: <clone>/.git/worktrees/<name>"
		wt := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
		if _, err := os.Stat(wt); err != nil {
			clone := wt
			if i := strings.Index(wt, string(filepath.Separator)+".git"+string(filepath.Separator)+"worktrees"+string(filepath.Separator)); i >= 0 {
				clone = wt[:i]
			}
			return "", fmt.Errorf("%w: %s is a worktree of your clone %s, and later updates need that clone's .git; the clone was moved or deleted: put it back at %s, or %s",
				ErrNoRepo, repo, clone, clone, viaSetup)
		}
	}
	dir, err := git(ctx, "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%w: %s is not a git checkout (%v): %s", ErrNoRepo, repo, err, viaSetup)
	}
	return dir, nil
}

// Find fetches the clone's tags (remote refs and tags only, never a branch
// or a working tree) and chooses the newest vX.Y.Z tag that is an ancestor of
// origin/main, forward from version, the running build's (§3.1).
func Find(ctx context.Context, repo, version string) (Release, error) {
	dir, err := GitDir(ctx, repo)
	if err != nil {
		return Release{}, err
	}
	from := version
	var sha string
	switch m := describes.FindStringSubmatch(version); {
	case plainTag.MatchString(version):
	case m != nil:
		sha = m[2]
		from = sha
	default:
		return Release{}, fmt.Errorf("%w: this agentbus is %s, not a release (a branch or local changes): %s", ErrNotRelease, version, viaSetup)
	}
	if _, err := git(ctx, "--git-dir", dir, "fetch", "--quiet", "--tags", "origin"); err != nil {
		return Release{}, fmt.Errorf("fetch: %w", err)
	}
	tags, err := git(ctx, "--git-dir", dir, "tag", "--list", "v*", "--merged", "refs/remotes/origin/main", "--sort=-v:refname")
	if err != nil {
		return Release{}, err
	}
	tag := ""
	for _, t := range strings.Fields(tags) {
		if plainTag.MatchString(t) {
			tag = t
			break
		}
	}
	if tag == "" || !buildinfo.Newer(tag, version) {
		return Release{}, fmt.Errorf("%w: %s, no newer release on origin/main", ErrUpToDate, version)
	}
	if sha != "" {
		if _, err := git(ctx, "--git-dir", dir, "merge-base", "--is-ancestor", sha, tag); err != nil {
			return Release{}, fmt.Errorf("%w: this agentbus is %s, whose commit %s is not in %s (it would be lost): %s", ErrNotRelease, version, sha, tag, viaSetup)
		}
	}
	return Release{Tag: tag, FromRev: from, GitDir: dir}, nil
}
