package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/forge"
)

// request review's close-on-merge facts (board-drift §3.2): the head it was
// given and the base tip it read, so the merge queue judges the head
// delivered later, and never a head that was already in the base.

// reviewGit runs git with args and returns its trimmed output.
type reviewGit func(args ...string) (string, error)

// reviewGitWait bounds one git call (a fetch included).
const reviewGitWait = 30 * time.Second

// reviewGitIn is reviewGit in dir.
func reviewGitIn(dir string) reviewGit {
	return func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), reviewGitWait)
		defer cancel()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = dir
		out, err := c.Output()
		return strings.TrimSpace(string(out)), err
	}
}

// reviewFacts is the head (commit, else the branch's tip) and the base tip
// (origin/<base> after a fetch, or the local <base> in local mode); either
// is "" when it does not resolve, and a failed fetch records no base. A
// head already in the base is refused: there is nothing to review.
func reviewFacts(git reviewGit, forgeSetting, base, branch, commit string) (head, reviewBase string, err error) {
	resolve := func(ref string) string {
		h, err := git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil || !bus.ValidHead(h) {
			return ""
		}
		return h
	}
	switch {
	case commit != "":
		if head = resolve(commit); head == "" {
			return "", "", fmt.Errorf("--commit %s is not a commit in this checkout", commit)
		}
	case branch != "":
		head = resolve("refs/heads/" + branch)
	}
	if base == "" {
		return head, "", nil
	}
	url, uerr := git("remote", "get-url", "origin")
	has := uerr == nil && url != ""
	if forge.Resolve(forgeSetting, url, has).Kind == forge.Local || !has {
		reviewBase = resolve("refs/heads/" + base)
	} else if _, ferr := git("fetch", "--quiet", "origin", base); ferr == nil {
		reviewBase = resolve("refs/remotes/origin/" + base)
	}
	if head != "" && reviewBase != "" {
		if _, aerr := git("merge-base", "--is-ancestor", head, reviewBase); aerr == nil {
			return "", "", fmt.Errorf("nothing to review: %s is already in %s", head, base)
		}
	}
	return head, reviewBase, nil
}
