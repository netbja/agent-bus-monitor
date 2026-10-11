package mission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Git runs every git command the conductor needs against the shared
// repository. Each command runs in its own process group, killed whole when
// ctx ends, so a conductor that loses its lease stops its Git work too.
type Git struct{ Repo string }

// ErrConflict: a merge stopped on conflicts and is left in progress.
var ErrConflict = errors.New("merge conflict")

// ErrUnknownCommit: the repository answered, and the commit is not in it.
var ErrUnknownCommit = errors.New("unknown commit")

// group makes cmd a process-group leader whose whole group dies on cancel.
func group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
}

func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	if dir == "" {
		dir = g.Repo
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	group(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (g Git) Show(ctx context.Context, commit, path string) ([]byte, error) {
	out, err := g.run(ctx, "", "cat-file", "blob", commit+":"+path)
	return []byte(out), err
}

func (g Git) Tree(ctx context.Context, commit string) ([]string, error) {
	out, err := g.run(ctx, "", "ls-tree", "-r", "--name-only", commit)
	return lines(out), err
}

// ResolveCommit returns the full sha of rev, or an error if it names no commit.
func (g Git) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := g.run(ctx, "", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ee.ExitCode() == 1: // git answered: no such commit
		return "", fmt.Errorf("commit %s not found in %s: %w", rev, g.Repo, ErrUnknownCommit)
	default: // git itself failed (repository missing, lock, …)
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g Git) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := g.run(ctx, "", "merge-base", "--is-ancestor", a, b)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	}
	return false, err
}

func (g Git) DiffNames(ctx context.Context, base, commit string) ([]string, error) {
	out, err := g.run(ctx, "", "diff", "--no-renames", "--name-only", base, commit) // a rename lists both sides
	return lines(out), err
}

// EnsureBranch creates branch at from when it does not exist; an existing
// branch is never moved.
func (g Git) EnsureBranch(ctx context.Context, branch, from string) error {
	if _, err := g.run(ctx, "", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nil
	}
	_, err := g.run(ctx, "", "branch", branch, from)
	return err
}

func (g Git) AddWorktree(ctx context.Context, dir, rev string, detach bool) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if detach {
		args = append(args, "--detach")
	}
	_, err := g.run(ctx, "", append(args, dir, rev)...)
	return err
}

// RemoveWorktree removes one of the conductor's own worktrees.
func (g Git) RemoveWorktree(ctx context.Context, dir string) error {
	_, err := g.run(ctx, "", "worktree", "remove", "--force", dir)
	_, _ = g.run(ctx, "", "worktree", "prune")
	return err
}

func (g Git) Head(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// MergeMessage is the integration commit message; git merge has no
// --trailer, so the trailers are in the body (spec §5.2).
func MergeMessage(change, id string, rev int, title, commit, hash string) string {
	return fmt.Sprintf("mission %s/%s (rev %d): %s\n\nMission: %s/%s\nMission-Commit: %s\nMission-Rev: %d\nMission-Hash: %s\n",
		change, id, rev, title, change, id, commit, rev, hash)
}

func (g Git) MergeNoFF(ctx context.Context, dir, commit, message string) error {
	_, err := g.run(ctx, dir, "merge", "--no-ff", "--no-edit", "-m", message, commit)
	if err != nil {
		if in, _ := g.MergeInProgress(dir); in {
			return ErrConflict
		}
	}
	return err
}

func (g Git) MergeInProgress(dir string) (bool, error) {
	out, err := g.run(context.Background(), dir, "rev-parse", "--path-format=absolute", "--git-path", "MERGE_HEAD")
	if err != nil {
		return false, err
	}
	_, err = os.Stat(strings.TrimSpace(out))
	return err == nil, nil
}

func (g Git) MergeAbort(ctx context.Context, dir string) error {
	_, err := g.run(ctx, dir, "merge", "--abort")
	return err
}

func (g Git) ResetHard(ctx context.Context, dir, rev string) error {
	_, err := g.run(ctx, dir, "reset", "--hard", "--quiet", rev)
	return err
}

// Dirty reports any change in dir, untracked files included.
func (g Git) Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	return strings.TrimSpace(out) != "", err
}

// BranchHasTrailer reports whether a commit reachable from branch carries the
// trailer key: value.
func (g Git) BranchHasTrailer(ctx context.Context, branch, key, value string) (bool, error) {
	out, err := g.run(ctx, "", "log", "--format=%(trailers:key="+key+",valueonly)", branch)
	if err != nil {
		return false, err
	}
	for _, l := range lines(out) {
		if strings.TrimSpace(l) == value {
			return true, nil
		}
	}
	return false, nil
}

const (
	tailLines = 40
	tailBytes = 4096
	keepBytes = 64 << 10
)

// tailBuf keeps the last keepBytes written to it.
type tailBuf struct{ b []byte }

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > keepBytes {
		t.b = t.b[len(t.b)-keepBytes:]
	}
	return len(p), nil
}

// tail is the last tailLines lines, at most tailBytes bytes.
func (t *tailBuf) tail() string {
	s := strings.TrimRight(string(t.b), "\n")
	ls := strings.Split(s, "\n")
	if len(ls) > tailLines {
		ls = ls[len(ls)-tailLines:]
	}
	s = strings.Join(ls, "\n")
	if len(s) > tailBytes {
		s = s[len(s)-tailBytes:]
	}
	return s
}

// RunTest runs command through sh -c in dir, appending its output to logPath,
// for at most limit. ok is the exit status; tail is the end of the output (a
// timeout adds "timeout after <limit>" as its last line). err is only for a
// failure to run at all, or ctx itself ending.
func RunTest(ctx context.Context, dir, command, logPath string, limit time.Duration) (ok bool, tail string, err error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return false, "", err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, "", err
	}
	defer logf.Close()
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(tctx, "sh", "-c", command)
	cmd.Dir = dir
	group(cmd)
	tb := &tailBuf{}
	w := io.MultiWriter(logf, tb)
	cmd.Stdout, cmd.Stderr = w, w
	runErr := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return false, tb.tail(), ctx.Err()
	case tctx.Err() == context.DeadlineExceeded:
		msg := fmt.Sprintf("timeout after %v", limit)
		fmt.Fprintln(logf, msg)
		return false, strings.TrimSpace(tb.tail() + "\n" + msg), nil
	case runErr != nil:
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return false, tb.tail(), nil
		}
		return false, tb.tail(), runErr
	}
	return true, tb.tail(), nil
}

// MissionMerge is one conductor merge read back from its trailers.
type MissionMerge struct {
	ID, Commit, Hash string
	Rev              int
}

var (
	hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`) // a commit sha
	hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`) // a mission hash (sha-256)
)

// MissionMerges returns the change's conductor merges on branch, oldest first.
// A merge without the full trailer set, naming another change, or whose
// Mission-Commit is not a full sha is not a conductor merge and is skipped.
func (g Git) MissionMerges(ctx context.Context, branch, change string) ([]MissionMerge, error) {
	format := "--format=%H%x1f%(trailers:key=Mission,valueonly)%x1f%(trailers:key=Mission-Commit,valueonly)%x1f%(trailers:key=Mission-Hash,valueonly)%x1f%(trailers:key=Mission-Rev,valueonly)%x1e"
	out, err := g.run(ctx, "", "log", "--merges", format, branch)
	if err != nil {
		return nil, err
	}
	var merges []MissionMerge
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 5 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		c, id, ok := strings.Cut(f[1], "/")
		rev, err := strconv.Atoi(f[4])
		if !ok || c != change || id == "" || !hex40.MatchString(f[2]) || !hex64.MatchString(f[3]) || err != nil || rev < 1 {
			continue // not a well-formed conductor merge: never proof of integration
		}
		merges = append(merges, MissionMerge{ID: id, Commit: f[2], Hash: f[3], Rev: rev})
	}
	slices.Reverse(merges) // git log is newest first
	return merges, nil
}
