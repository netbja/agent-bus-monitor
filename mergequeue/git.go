package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The git fallback (spec §3b): no forge CLI, or a local forge. Every command
// runs in dir with exactly env (the caller hardens it, as for the forge) and
// writes no ref.

// RemoteTips are origin's branches and their tips (git ls-remote --heads, no
// fetch), the base apart: its tip is returned on its own.
func RemoteTips(ctx context.Context, dir string, env []string, base string) (tips []Tip, baseTip string, err error) {
	out, err := run(ctx, dir, env, "git", "ls-remote", "--heads", "origin")
	if err != nil {
		return nil, "", err
	}
	return splitTips(string(out), "refs/heads/", base)
}

// LocalTips are the local branches and their tips (the local forge: deliver
// pushes nothing), the base apart.
func LocalTips(ctx context.Context, dir string, env []string, base string) (tips []Tip, baseTip string, err error) {
	out, err := run(ctx, dir, env, "git", "for-each-ref", "--format=%(objectname)\t%(refname)", "refs/heads")
	if err != nil {
		return nil, "", err
	}
	return splitTips(string(out), "refs/heads/", base)
}

// splitTips reads "<sha>\t<prefix><branch>" lines.
func splitTips(out, prefix, base string) ([]Tip, string, error) {
	var tips []Tip
	baseTip := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		branch, ok := strings.CutPrefix(ref, prefix)
		if !ok || !bus.ValidHead(sha) {
			continue
		}
		if branch == base {
			baseTip = sha
			continue
		}
		tips = append(tips, Tip{Branch: cleanText(branch), Head: sha})
	}
	if baseTip == "" {
		return nil, "", fmt.Errorf("no branch %s", base)
	}
	return tips, baseTip, nil
}

// FetchBase brings the base branch's objects from origin and writes no ref:
// no FETCH_HEAD, no tags, and the empty --refmap= stops git's opportunistic
// update of refs/remotes/origin/<base> (foureyes: without it the
// remote-tracking branch and origin/HEAD move).
func FetchBase(ctx context.Context, dir string, env []string, base string) error {
	if !validBranchName(base) {
		return fmt.Errorf("invalid base %q", base)
	}
	_, err := run(ctx, dir, env, "git", "fetch", "--quiet", "--no-write-fetch-head", "--no-tags", "--refmap=", "origin", base)
	return err
}

// InBase tells, for each head, whether the base (its tip baseTip) has it: an
// ancestor (a merge commit), or its contents (a squash: no difference from the
// base, "contents in base", which a revert can also give). A head that cannot
// be judged (absent here) is an error, never "not merged".
func InBase(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, h := range heads {
		if !bus.ValidHead(h) || !bus.ValidHead(baseTip) {
			return nil, errors.New("an invalid commit id")
		}
		anc, err := exitTrue(ctx, dir, env, "merge-base", "--is-ancestor", h, baseTip)
		if err != nil {
			return nil, err
		}
		if anc {
			out[h] = true
			continue
		}
		same, err := exitTrue(ctx, dir, env, "diff", "--quiet", baseTip, h)
		if err != nil {
			return nil, err
		}
		out[h] = same
	}
	return out, nil
}

// exitTrue runs a git command whose answer is its exit code: 0 true, 1 false,
// anything else an error.
func exitTrue(ctx context.Context, dir string, env []string, args ...string) (bool, error) {
	_, err := run(ctx, dir, env, "git", args...)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git %s: cannot judge", args[0])
}

// validBranchName: a plain branch name a command line may carry (no option,
// no space, no revision syntax).
func validBranchName(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t~^:?*[\\") || strings.Contains(s, "..") {
		return false
	}
	return true
}
