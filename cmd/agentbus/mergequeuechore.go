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
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/mergequeue"
	"github.com/netbja/agent-bus-monitor/plan"
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
	mergeQueueWrite  = 2 * time.Second  // the fenced write's share of the budget
)

// mergeQueueChore holds the pass's readers (tests replace them).
type mergeQueueChore struct {
	b       *bus.Bus
	dir     string
	budget  time.Duration // the pass's whole budget, the write's share included (0 = mergeQueueBudget)
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
	// master-idle §3.1: the git test of delivery (a mergequeue.Delivery,
	// kept across passes for its cache) and the plan's done subjects.
	delivered func(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error)
	done      func(ctx context.Context, vs []bus.Verdict) (map[string]bool, error)
	// serverNow is the broker's clock: the approvals' times are Redis TIME,
	// so the 72 h window is judged on it (reviewer-codex); nil reads
	// bus.ServerTime.
	serverNow func(ctx context.Context) (time.Time, error)
}

// serviceMergeQueue is the dispatch service's chore, in its working directory.
func serviceMergeQueue(b *bus.Bus, getenv func(string) string) dispatch.MergeQueueConfig {
	c := serviceMergeQueueChore(b, getenv)
	if c == nil {
		return dispatch.MergeQueueConfig{}
	}
	return dispatch.MergeQueueConfig{Every: mergeQueueEvery, Pass: c.pass, Due: b.MergeQueueDue} // a delivery's mark runs a pass at once (merge-popup-setting)
}

// serviceMergeQueueChore is the chore with the real readers, in the current
// directory (nil without one); merge-queue --live uses it too.
func serviceMergeQueueChore(b *bus.Bus, getenv func(string) string) *mergeQueueChore {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	delivery := mergequeue.NewDelivery()
	delivery.Call = mergeQueueCall
	return &mergeQueueChore{b: b, dir: dir, getenv: getenv, environ: os.Environ,
		repo:   gitRepoCheck,
		origin: gitOrigin,
		hasGH: func(env []string) bool {
			_, err := mergequeue.LookIn(env, "gh")
			return err == nil
		},
		github: mergequeue.ReadGitHub, remoteTips: mergequeue.RemoteTips, localTips: mergequeue.LocalTips,
		fetchBase: mergequeue.FetchBase, inBase: mergequeue.InBase,
		delivered: delivery.Delivered,
		done: func(ctx context.Context, vs []bus.Verdict) (map[string]bool, error) {
			return planDoneSubjects(ctx, b, vs)
		},
	}
}

// planDoneSubjects are the review subjects of the active plan's tasks
// recorded done (review-queue's DoneSubjects, without its pr:<n>): none
// without a plan.
func planDoneSubjects(ctx context.Context, b *bus.Bus, vs []bus.Verdict) (map[string]bool, error) {
	out := map[string]bool{}
	act, ok, err := plan.Load(ctx, b)
	if err != nil || !ok {
		return out, err
	}
	pin, errs := plan.ReadWithVerdicts(ctx, b, act.File, plan.BySubject(vs, nil), true)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	for _, r := range plan.Derive(act.File, pin) {
		if sub := r.Task.ReviewSubject(); r.State == plan.Done && sub != "" {
			out[sub] = true
		}
	}
	return out, nil
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
// reason. The reads get the budget less the write's share, and the write its
// own context from the caller's: reads that spend their time still record
// their failure, never leave the last success looking current.
func (c *mergeQueueChore) pass(ctx context.Context, token string) error {
	budget := c.budget
	if budget == 0 {
		budget = mergeQueueBudget
	}
	write := min(mergeQueueWrite, budget/2)
	dctx, cancel := context.WithTimeout(ctx, budget-write)
	rec, run := c.deriveRun(dctx)
	cancel()
	wctx, wcancel := context.WithTimeout(ctx, write)
	defer wcancel()
	if err := c.b.FencedPutMergeQueue(wctx, token, rec); err != nil {
		return err
	}
	// The run's step (run-budget §2): its clock, and the deliveries judged
	// this pass; a gap longer than three passes counts as paused. Then its
	// limits (§3): the drain, and the stop once nothing is in flight.
	runRec, _, err := c.b.FencedRunPass(wctx, token, run.heads, run.known, 3*mergeQueueEvery)
	if err != nil {
		return err
	}
	return c.runStepKnown(wctx, token, runRec, rec.Undelivered, rec.Reason == "")
}

// runHeads are the approved heads (within 72 h) the git test found in the
// base this pass; known is false when the pass could not judge them.
type runHeads struct {
	heads []string
	known bool
}

// derive reads everything one pass needs; a failed read is the reason.
func (c *mergeQueueChore) derive(ctx context.Context) bus.MergeQueueRecord {
	rec, _ := c.deriveRun(ctx)
	return rec
}

// deriveRun is derive with the run's delivered heads (run-budget §2): none
// known when the pass fails.
func (c *mergeQueueChore) deriveRun(ctx context.Context) (bus.MergeQueueRecord, runHeads) {
	settings := config.Machine(c.getenv)
	// The project's base, as deliver's (merge-popup-setting): the template's
	// [delivery] base, else the machine's delivery.base.
	base, _, berr := projectDeliveryBase(c.getenv, c.b.Project())
	env := mergeQueueEnv(c.environ())
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, mergeQueueCall) }
	forgeKind := ""
	fail := func(source, why string) (bus.MergeQueueRecord, runHeads) {
		return bus.MergeQueueRecord{Source: source, Reason: why, Forge: forgeKind, Base: base}, runHeads{}
	}
	if berr != nil { // a template that does not decode: no base anybody chose
		return fail("", "the project's base: "+berr.Error())
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
	forgeKind = string(f.Kind)
	if f.Kind != forge.Local && !has {
		return fail("git", "no origin remote")
	}
	vs, err := c.b.Verdicts(ctx, "")
	if err != nil {
		return fail("", "verdicts unavailable")
	}
	clock := c.serverNow
	if clock == nil {
		clock = c.b.ServerTime
	}
	now, err := clock(ctx)
	if err != nil {
		return fail("", "broker time unavailable")
	}
	in := mergequeue.Input{Verdicts: vs}
	source := "git"
	// Origin's tips and the fetched base, in every mode that pushes (the gh
	// path too, for master-idle's undelivered heads).
	var remote []mergequeue.Tip
	remoteBase := ""
	judgeBase := "" // the base tip the git test judges against (origin's, or the local one)
	if f.Kind != forge.Local {
		cctx, cancel = call()
		remote, remoteBase, err = c.remoteTips(cctx, c.dir, env, base)
		cancel()
		if err != nil {
			return fail(source, "branches unreadable: "+err.Error())
		}
		cctx, cancel = call()
		err = c.fetchBase(cctx, c.dir, env, base)
		cancel()
		if err != nil {
			return fail(source, "the base's fetch failed: "+err.Error())
		}
	}
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
		tips, baseTip := remote, remoteBase
		judgeBase = baseTip
		if f.Kind == forge.Local {
			cctx, cancel = call()
			tips, baseTip, err = c.localTips(cctx, c.dir, env, base)
			cancel()
			if err != nil {
				return fail(source, "branches unreadable: "+err.Error())
			}
			judgeBase = baseTip
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
	var undelivered []bus.MergeUndelivered
	if f.Kind != forge.Local {
		u, why := c.undelivered(ctx, env, vs, remote, remoteBase, base, now)
		if why != "" {
			return fail(source, why)
		}
		undelivered = u
	}
	if judgeBase == "" {
		judgeBase = remoteBase // the gh path: origin's base
	}
	run := c.runDelivered(ctx, env, vs, judgeBase, now)
	return bus.MergeQueueRecord{Source: source, Forge: forgeKind, Base: base, Items: res.Items, Unapproved: res.Unapproved, Conflicting: res.Conflicting,
		Undelivered: undelivered}, run
}

// runDelivered are the approved heads within 72 h that the git test
// (master-idle §3.1) finds in the base: a run's deliveries are counted
// from them, forge or not (run-budget §2). A failed judgement is not known:
// the run counts nothing this pass, and the queue's record is unaffected.
func (c *mergeQueueChore) runDelivered(ctx context.Context, env []string, vs []bus.Verdict, baseTip string, now time.Time) runHeads {
	heads := mergequeue.RecentApprovedHeads(vs, now, health.ReviewQueueWindow)
	if len(heads) == 0 {
		return runHeads{known: true}
	}
	in, err := c.delivered(ctx, c.dir, env, baseTip, heads)
	if err != nil {
		return runHeads{}
	}
	out := []string{}
	for _, h := range heads {
		if in[h] {
			out = append(out, h)
		}
	}
	return runHeads{heads: out, known: true}
}

// undelivered are the approved heads master has not delivered (master-idle
// §3.1): no tip on origin, not dropped, not done, and not found in the
// fetched base by the git test; why is the failed read, or "".
func (c *mergeQueueChore) undelivered(ctx context.Context, env []string, vs []bus.Verdict, remote []mergequeue.Tip, baseTip, base string, now time.Time) ([]bus.MergeUndelivered, string) {
	cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
	local, _, err := c.localTips(cctx, c.dir, env, base)
	cancel()
	if err != nil {
		local = nil // only names a head's branch: a head without one is still listed
	}
	dropped, err := c.b.DroppedHeads(ctx)
	if err != nil {
		return nil, "dropped heads unavailable"
	}
	done, err := c.done(ctx, vs)
	if err != nil {
		return nil, "plan unavailable"
	}
	isDropped := map[string]bool{}
	for h := range dropped {
		isDropped[h] = true
	}
	cands := mergequeue.UndeliveredCandidates(mergequeue.UndeliveredInput{Verdicts: vs, Now: now, Window: health.ReviewQueueWindow,
		Remote: remote, Local: local, Dropped: isDropped, Done: done})
	if len(cands) == 0 {
		return []bus.MergeUndelivered{}, ""
	}
	heads := make([]string, len(cands))
	for i, u := range cands {
		heads[i] = u.Head
	}
	in, err := c.delivered(ctx, c.dir, env, baseTip, heads)
	if err != nil {
		return nil, "an approved head's delivery cannot be judged: " + err.Error()
	}
	out := []bus.MergeUndelivered{}
	for _, u := range cands {
		if !in[u.Head] {
			out = append(out, u)
		}
	}
	return out, ""
}
