package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// The merge queue's chore (spec docs/superpowers/specs/2026-11-05-merge-queue-design.md §3c):
// every pass recomputes the queue whole from the ledger and the forge (or
// git) in the services' working directory, and replaces the fenced cache
// {p}:mergequeue. A pass that cannot read something keeps the last items and
// says why: never an empty queue read as "nothing waits".

// mergeQueueEvery and mergeQueueBudget: a pass every 5 minutes, at most 20 s.
const (
	mergeQueueEvery  = 5 * time.Minute
	mergeQueueBudget = 20 * time.Second
	mergeQueueCall   = 10 * time.Second // one forge or git command
)

// mergeQueueChore holds the pass's readers (tests replace them).
type mergeQueueChore struct {
	b       *bus.Bus
	dir     string
	getenv  func(string) string
	environ func() []string

	repo       func(ctx context.Context, dir string, env []string) error
	origin     func(ctx context.Context, dir string, env []string) (url string, has bool, err error)
	hasGH      func(env []string) bool
	github     func(ctx context.Context, dir string, env []string) ([]mergequeue.PR, error)
	remoteTips func(ctx context.Context, dir string, env []string, base string) ([]mergequeue.Tip, string, error)
	localTips  func(ctx context.Context, dir string, env []string, base string) ([]mergequeue.Tip, string, error)
	fetchBase  func(ctx context.Context, dir string, env []string, base string) error
	inBase     func(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error)
}

// serviceMergeQueue is the dispatch service's chore, in its working directory.
func serviceMergeQueue(b *bus.Bus, getenv func(string) string) dispatch.MergeQueueConfig {
	dir, err := os.Getwd()
	if err != nil {
		return dispatch.MergeQueueConfig{}
	}
	c := &mergeQueueChore{b: b, dir: dir, getenv: getenv, environ: os.Environ,
		repo:   gitRepoCheck,
		origin: gitOrigin,
		hasGH: func(env []string) bool {
			_, err := mergequeue.LookIn(env, "gh")
			return err == nil
		},
		github: mergequeue.ReadGitHub, remoteTips: mergequeue.RemoteTips, localTips: mergequeue.LocalTips,
		fetchBase: mergequeue.FetchBase, inBase: mergequeue.InBase}
	return dispatch.MergeQueueConfig{Every: mergeQueueEvery, Pass: c.pass}
}

// mergeQueueEnv is every command's environment: no askpass program and no
// prompt of any kind (the github check's probeEnviron), gh told not to
// prompt, English answers.
func mergeQueueEnv(base []string) []string {
	return probeEnviron(base, []string{"LC_ALL=C", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"})
}

func gitRepoCheck(ctx context.Context, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir, cmd.Env = dir, env
	return cmd.Run()
}

func gitOrigin(ctx context.Context, dir string, env []string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return "", false, nil // no origin
	case err != nil:
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// pass recomputes and stores the queue; only the lease's errors are returned
// (a lost lease ends nothing else), every read's failure is the record's
// reason.
func (c *mergeQueueChore) pass(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, mergeQueueBudget)
	defer cancel()
	rec := c.derive(ctx)
	return c.b.FencedPutMergeQueue(ctx, token, rec)
}

// derive reads everything one pass needs; a failed read is the reason.
func (c *mergeQueueChore) derive(ctx context.Context) bus.MergeQueueRecord {
	settings := config.Machine(c.getenv)
	base := settings.String("delivery.base")
	env := mergeQueueEnv(c.environ())
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, mergeQueueCall) }
	fail := func(source, why string) bus.MergeQueueRecord {
		return bus.MergeQueueRecord{Source: source, Reason: why}
	}

	cctx, cancel := call()
	err := c.repo(cctx, c.dir, env)
	cancel()
	if err != nil {
		return fail("git", "no git repository where the services run")
	}
	cctx, cancel = call()
	url, has, err := c.origin(cctx, c.dir, env)
	cancel()
	if err != nil {
		return fail("git", "origin unreadable")
	}
	f := forge.Resolve(settings.String("delivery.forge"), url, has)
	if f.Kind != forge.Local && !has {
		return fail("git", "no origin remote")
	}
	vs, err := c.b.Verdicts(ctx, "")
	if err != nil {
		return fail("", "verdicts unavailable")
	}
	in := mergequeue.Input{Verdicts: vs}
	source := "git"
	switch {
	case f.Kind == forge.GitHub && c.hasGH(env):
		source = "gh"
		cctx, cancel = call()
		prs, err := c.github(cctx, c.dir, env)
		cancel()
		if err != nil {
			return fail(source, err.Error())
		}
		in.PRs = prs
	default: // the git fallback: GitHub without gh, GitLab and Bitbucket (no reader yet), local
		read := c.remoteTips
		if f.Kind == forge.Local {
			read = c.localTips
		}
		cctx, cancel = call()
		tips, baseTip, err := read(cctx, c.dir, env, base)
		cancel()
		if err != nil {
			return fail(source, "branches unreadable: "+err.Error())
		}
		if f.Kind != forge.Local {
			cctx, cancel = call()
			err = c.fetchBase(cctx, c.dir, env, base)
			cancel()
			if err != nil {
				return fail(source, "the base's fetch failed: "+err.Error())
			}
		}
		approved := mergequeue.ApprovedHeads(vs)
		var heads []string
		for _, tp := range tips {
			if approved[tp.Head] {
				heads = append(heads, tp.Head)
			}
		}
		cctx, cancel = call()
		in2, err := c.inBase(cctx, c.dir, env, baseTip, heads)
		cancel()
		if err != nil {
			return fail(source, "an approved head cannot be judged: "+err.Error())
		}
		in.Tips, in.Merged = tips, func(h string) bool { return in2[h] }
	}
	res := mergequeue.Derive(in)
	return bus.MergeQueueRecord{Source: source, Items: res.Items, Unapproved: res.Unapproved}
}
