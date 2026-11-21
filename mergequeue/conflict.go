package mergequeue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The conflict check (spec 2026-11-21-conflict-owner-design r5, §2): an
// approved head is merged with the base's tip by git merge-tree in an
// isolated repository, so no merge driver, hook or attribute of the
// repository runs (the dispatcher never runs repository code), and no ref
// is written.

// Candidate is an approved head to check: Key names it in the result (the
// PR's number, or the branch in the git fallback).
type Candidate struct {
	Key, Branch, Head string
}

// Judged is a candidate's outcome: Why says it was not judged this pass
// (absent, git failed, out of time); otherwise Conflict, with the
// conflicting files (the first conflictFiles).
type Judged struct {
	Conflict bool
	Files    []string
	Why      string
}

// ConflictCheck is where the check runs: Dir, the services' repository
// (its object store is the isolated one's alternate), Scratch, a directory
// the isolated repository is made in and removed from, Env the chore's
// hardened environment (only its PATH reaches the isolated git), Fetch
// whether absent heads are fetched from origin (every mode that pushes; not
// local), and Call each git call's bound (0: none beyond ctx).
type ConflictCheck struct {
	Dir, Scratch string
	Env          []string
	Fetch        bool
	Call         time.Duration
}

const (
	conflictFiles = 10
	// gitTooOld is the reason when merge-tree lacks --write-tree (2.38) or
	// git lacks --attr-source (2.42): nothing is judged, nothing sent.
	gitTooOld = "git too old for the conflict check (merge-tree --write-tree and --attr-source need git 2.42)"
	// notReached marks a candidate the pass's budget did not reach.
	notReached = "not checked this pass (time): next pass"
)

// CheckConflicts judges each candidate against baseTip, oldest first as
// given, until ctx ends (the rest: notReached). why is a reason the whole
// check could not run (no scratch, git too old), with no candidate judged.
func CheckConflicts(ctx context.Context, c ConflictCheck, baseTip string, cands []Candidate) (map[string]Judged, string) {
	out := map[string]Judged{}
	if len(cands) == 0 {
		return out, ""
	}
	if !bus.ValidHead(baseTip) {
		return out, "no base tip to judge against"
	}
	if ctx.Err() != nil { // the pass's budget already spent: all for the next pass
		for _, cd := range cands {
			out[cd.Key] = Judged{Why: notReached}
		}
		return out, ""
	}
	if err := os.MkdirAll(c.Scratch, 0o700); err != nil {
		return out, "no scratch directory for the conflict check"
	}
	iso, err := os.MkdirTemp(c.Scratch, "conflict-check-")
	if err != nil {
		return out, "no scratch directory for the conflict check"
	}
	defer os.RemoveAll(iso)
	isoEnv := isolatedEnv(c.Env, iso)
	gitDir := filepath.Join(iso, "repo.git")
	g := func(env []string, dir string, args ...string) ([]byte, int, error) {
		cctx, cancel := ctx, context.CancelFunc(func() {})
		if c.Call > 0 {
			cctx, cancel = context.WithTimeout(ctx, c.Call)
		}
		defer cancel()
		return gitCode(cctx, dir, env, args...)
	}
	if _, code, err := g(isoEnv, iso, "init", "-q", "--bare", "--template=", gitDir); err != nil || code != 0 {
		return out, "the conflict check's scratch repository failed"
	}
	empty, code, err := g(isoEnv, iso, "--git-dir="+gitDir, "hash-object", "-w", "-t", "tree", "/dev/null")
	tree := strings.TrimSpace(string(empty))
	if err != nil || code != 0 || !bus.ValidHead(tree) {
		return out, "the conflict check's scratch repository failed"
	}
	usage, _, err := g(isoEnv, iso, "--git-dir="+gitDir, "--attr-source="+tree, "merge-tree", "-h")
	if err != nil || !mergeTreeUsable(string(usage)) {
		return out, gitTooOld
	}
	objects, code, err := g(c.Env, c.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || code != 0 {
		return out, "the services' repository cannot be read"
	}
	alt := filepath.Join(strings.TrimSpace(string(objects)), "objects") + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "objects", "info", "alternates"), []byte(alt), 0o600); err != nil {
		return out, "the conflict check's scratch repository failed"
	}
	has := func(head string) bool {
		_, code, err := g(isoEnv, iso, "--git-dir="+gitDir, "cat-file", "-e", head+"^{commit}")
		return err == nil && code == 0
	}
	if !has(baseTip) {
		return out, "the base tip is absent here"
	}
	// Absent heads, fetched once for all from origin (no ref written), in
	// every mode that pushes.
	var missing []string
	for _, cd := range cands {
		if bus.ValidHead(cd.Head) && validBranchName(cd.Branch) && !has(cd.Head) {
			missing = append(missing, cd.Branch)
		}
	}
	if c.Fetch && len(missing) > 0 {
		g(c.Env, c.Dir, append([]string{"fetch", "--quiet", "--no-write-fetch-head", "--no-tags", "--refmap=", "origin"}, missing...)...)
	}
	for i, cd := range cands {
		if ctx.Err() != nil {
			for _, rest := range cands[i:] {
				out[rest.Key] = Judged{Why: notReached}
			}
			break
		}
		if !bus.ValidHead(cd.Head) {
			out[cd.Key] = Judged{Why: "an invalid head"}
			continue
		}
		if !has(cd.Head) {
			out[cd.Key] = Judged{Why: "the head is absent here, even after a fetch"}
			continue
		}
		cctx, cancel := ctx, context.CancelFunc(func() {})
		if c.Call > 0 {
			cctx, cancel = context.WithTimeout(ctx, c.Call)
		}
		res, _, code, err := gitStreams(cctx, iso, isoEnv, "--git-dir="+gitDir, "--attr-source="+tree, "merge-tree", "--write-tree", "--name-only", "--no-messages", baseTip, cd.Head)
		cancel()
		switch {
		case err != nil:
			out[cd.Key] = Judged{Why: "git merge-tree did not finish: " + err.Error()}
		case code == 0:
			out[cd.Key] = Judged{}
		case code == 1:
			out[cd.Key] = Judged{Conflict: true, Files: conflictNames(string(res))}
		default:
			out[cd.Key] = Judged{Why: fmt.Sprintf("git merge-tree exited %d", code)}
		}
	}
	return out, ""
}

// mergeTreeUsable judges the probe's usage text: an unknown option is a git
// older than --attr-source; otherwise merge-tree must offer --write-tree.
func mergeTreeUsable(usage string) bool {
	return !strings.Contains(usage, "unknown option") && strings.Contains(usage, "--write-tree")
}

// conflictNames are the files after the tree id, the first conflictFiles.
func conflictNames(out string) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var files []string
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, cleanText(l))
		}
		if len(files) == conflictFiles {
			break
		}
	}
	return files
}

// isolatedEnv is the isolated git's whole environment: env's PATH and
// nothing of the person's or the system's configuration or attributes.
func isolatedEnv(env []string, home string) []string {
	out := []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return out
}

// gitCode runs git (on env's PATH) in dir with exactly env: its output
// (stdout, then stderr), its exit code, and an error only when it did not
// run or did not finish.
func gitCode(ctx context.Context, dir string, env []string, args ...string) ([]byte, int, error) {
	out, errOut, code, err := gitStreams(ctx, dir, env, args...)
	return append(out, errOut...), code, err
}

// gitStreams is gitCode with the two outputs apart: what merge-tree names
// is on its standard output only, never a warning on its error output
// (foureyes).
func gitStreams(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr []byte, code int, err error) {
	path, err := lookIn(env, "git")
	if err != nil {
		return nil, nil, -1, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, env, time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, nil, -1, errors.New("git timed out")
	case errors.As(err, &ee):
		return out.Bytes(), errb.Bytes(), ee.ExitCode(), nil
	case err != nil:
		return nil, nil, -1, err
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}

// ConflictOwner is who a conflicting branch goes back to (§3): the one owner
// every board entry naming it (claim or request branch) agrees on, when a
// known agent; with no entry, the branch's <role>/ prefix when a known
// agent; master otherwise.
func ConflictOwner(board map[string]bus.BoardEntry, agents map[string]bool, branch, master string) string {
	known := func(n string) bool { return bus.ValidName(n) && agents[n] }
	owners := map[string]bool{}
	for _, e := range board {
		if e.Branch == branch || (e.Request != nil && e.Request.Branch == branch) {
			owners[e.Owner] = true
		}
	}
	if len(owners) > 0 {
		if len(owners) == 1 {
			for o := range owners {
				if known(o) {
					return o
				}
			}
		}
		return master
	}
	if role, _, ok := strings.Cut(branch, "/"); ok && known(role) {
		return role
	}
	return master
}
