package dispatchsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/mergequeue"
	"github.com/netbja/agent-bus-monitor/plan"
	"github.com/netbja/agent-bus-monitor/service"
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

	repo   func(ctx context.Context, dir string, env []string) error
	origin func(ctx context.Context, dir string, env []string) (url string, has bool, err error)
	hasGH  func(env []string) bool
	github func(ctx context.Context, dir string, env []string, checks bool) ([]mergequeue.PR, error)
	// readBase reads a base branch's rollup for the CI loop (ci-loop §4).
	readBase   func(ctx context.Context, dir string, env []string, branch string) (mergequeue.Base, error)
	remoteTips func(ctx context.Context, dir string, env []string, base string) ([]mergequeue.Tip, string, error)
	localTips  func(ctx context.Context, dir string, env []string, base string) ([]mergequeue.Tip, string, error)
	fetchBase  func(ctx context.Context, dir string, env []string, base string) error
	inBase     func(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error)
	// localBranches are the local refs, no base required: close on merge's
	// branch gone from origin (board-drift §3.2).
	localBranches func(ctx context.Context, dir string, env []string) ([]mergequeue.Tip, error)
	// losable work (cleaning-up-agents §3.1 item 5): the stash list, and the
	// git test of a branch tip on the remote; nil stashes judges nothing.
	stashes    func(ctx context.Context, dir string, env []string) ([]string, error)
	hasCommit  func(ctx context.Context, dir string, env []string, head string) (bool, error)
	isAncestor func(ctx context.Context, dir string, env []string, head, baseTip string) (bool, error)
	// master-idle §3.1: the git test of delivery (a mergequeue.Delivery,
	// kept across passes for its cache) and the plan's done subjects.
	delivered func(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error)
	done      func(ctx context.Context, vs []bus.Verdict) (map[string]bool, error)
	// serverNow is the broker's clock: the approvals' times are Redis TIME,
	// so the 72 h window is judged on it (reviewer-codex); nil reads
	// bus.ServerTime.
	serverNow func(ctx context.Context) (time.Time, error)
	// master is the services' escalation target, read at each pass (the CI
	// loop's master); nil is "master".
	master *string
	// conflicts is the conflict check (conflict-owner §2), run in scratch
	// (the project's state directory); nil checks nothing.
	conflicts func(ctx context.Context, c mergequeue.ConflictCheck, baseTip string, cands []mergequeue.Candidate) (map[string]mergequeue.Judged, string)
	scratch   string
}

// serviceMergeQueue is the dispatch service's chore, in its working
// directory; target is the services' escalation target (the CI loop's
// master).
func serviceMergeQueue(b *bus.Bus, getenv func(string) string, target *string) dispatch.MergeQueueConfig {
	c := serviceMergeQueueChore(b, getenv)
	if c == nil {
		return dispatch.MergeQueueConfig{}
	}
	c.master = target
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
	scratch := ""
	if paths, err := service.PathsFrom(getenv); err == nil {
		scratch = paths.StateDir(b.Project())
	}
	return &mergeQueueChore{b: b, dir: dir, getenv: getenv, environ: os.Environ, conflicts: mergequeue.CheckConflicts, scratch: scratch,
		repo:   gitRepoCheck,
		origin: gitOrigin,
		hasGH: func(env []string) bool {
			_, err := mergequeue.LookIn(env, "gh")
			return err == nil
		},
		github: mergequeue.ReadGitHub, readBase: mergequeue.ReadBaseRollup, remoteTips: mergequeue.RemoteTips, localTips: mergequeue.LocalTips, localBranches: mergequeue.LocalBranches,
		fetchBase: mergequeue.FetchBase, inBase: mergequeue.InBase,
		stashes: mergequeue.Stashes, hasCommit: mergequeue.HasCommit, isAncestor: mergequeue.IsAncestor,
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
	rec, run, ci, cf, mc := c.deriveRun(dctx)
	if ci.on {
		why, err := mergequeue.RunCI(dctx, c.b, token, c.ciConfig(ci.env, ci.cap), ci.prs, ci.vs)
		if err != nil {
			cancel()
			return err // the lease lost
		}
		rec.CI = why
	}
	if cf.on { // conflict-owner §4: after the check, before the record's write
		why, err := mergequeue.RunConflicts(dctx, c.b, token, mergequeue.ConflictConfig{Master: c.masterName(), Cap: cf.cap, EscalateAfter: cf.after}, cf.pass)
		if err != nil {
			cancel()
			return err // the lease lost
		}
		if why != "" {
			rec.ConflictCheck = strings.Join(slices.DeleteFunc([]string{rec.ConflictCheck, why}, func(s string) bool { return s == "" }), "; ")
		}
	}
	// board-drift §3.3: each fenced; a request changed since the read is
	// left. A last review that merges with steps still listed steps the
	// request back to accepted (bus.MergedOpen, sibling-enforce §3.4): its
	// request-merged event names the listed steps, its audit line says open.
	for _, m := range mc.closes {
		if _, err := c.b.FencedRequestMerged(dctx, token, m.task, m.branch, m.head, mc.base, m.reviewN); errors.Is(err, bus.ErrNotHolder) {
			cancel()
			return err
		}
	}
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
	if err := c.runStepKnown(wctx, token, runRec, rec.Undelivered, rec.Reason == ""); err != nil {
		return err
	}
	return c.losablePass(ctx, token) // cleaning-up-agents §3.1 item 5, its own budget
}

// runHeads are the approved heads (within 72 h) the git test found in the
// base this pass; known is false when the pass could not judge them.
type runHeads struct {
	heads []string
	known bool
}

// ciPass is what the CI loop takes from a pass (ci-loop §2): the open PRs of
// a gh read that succeeded, with their rollups, and the ledger; on only
// while ci.loop is on and that read is complete.
type ciPass struct {
	on  bool
	cap int
	env []string
	prs []mergequeue.PR
	vs  []bus.Verdict
}

// conflictPass is what the conflict hand-off takes from a pass
// (conflict-owner §4): on only when the pass read everything, so the open
// set is complete (a field not in it is forgotten).
type conflictPass struct {
	on    bool
	cap   int
	after time.Duration
	pass  mergequeue.ConflictPass
}

// masterName is the escalation target the chore was given, else master.
func (c *mergeQueueChore) masterName() string {
	if c.master != nil && *c.master != "" {
		return *c.master
	}
	return "master"
}

// ciConfig is the loop's configuration for this pass; the base is read with
// gh in the services' directory, one call's budget each.
func (c *mergeQueueChore) ciConfig(env []string, cap int) mergequeue.CIConfig {
	master := c.masterName()
	return mergequeue.CIConfig{Project: c.b.Project(), Master: master, Cap: cap,
		ReadBase: func(ctx context.Context, branch string) (mergequeue.Base, error) {
			cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
			defer cancel()
			return c.readBase(cctx, c.dir, env, branch)
		}}
}

// derive reads everything one pass needs; a failed read is the reason. It
// writes nothing: the CI loop runs only in pass.
func (c *mergeQueueChore) derive(ctx context.Context) bus.MergeQueueRecord {
	rec, _, _, _, _ := c.deriveRun(ctx)
	return rec
}

// MergeQueueLive derives the merge queue now, as the chore does, in the
// current directory (merge-queue --live; dispatchsvc spec §3):
// false when there is no working directory.
func MergeQueueLive(ctx context.Context, b *bus.Bus, getenv func(string) string) (bus.MergeQueueRecord, bool) {
	c := serviceMergeQueueChore(b, getenv)
	if c == nil {
		return bus.MergeQueueRecord{}, false
	}
	return c.derive(ctx), true
}

// deriveRun is derive with the run's delivered heads (run-budget §2), the
// CI loop's input and the conflict hand-off's: none known when the pass fails.
func (c *mergeQueueChore) deriveRun(ctx context.Context) (bus.MergeQueueRecord, runHeads, ciPass, conflictPass, mergeClosePass) {
	settings := config.Machine(c.getenv)
	// The project's base, as deliver's (merge-popup-setting): the template's
	// [delivery] base, else the machine's delivery.base.
	base, _, berr := forge.ProjectDeliveryBase(c.getenv, c.b.Project())
	env := mergeQueueEnv(c.environ())
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, mergeQueueCall) }
	forgeKind := ""
	fail := func(source, why string) (bus.MergeQueueRecord, runHeads, ciPass, conflictPass, mergeClosePass) {
		return bus.MergeQueueRecord{Source: source, Reason: why, Forge: forgeKind, Base: base}, runHeads{}, ciPass{}, conflictPass{}, mergeClosePass{}
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
	ci := ciPass{on: settings.Bool("ci.loop"), cap: settings.Int("ci.max_handoffs"), env: env, vs: vs}
	open := map[string]bool{} // the conflict hand-off's open PRs or live branches
	cf := conflictPass{on: true, cap: settings.Int("conflict.max_handoffs"), after: settings.Duration("conflict.escalate_after")}
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
	var branchTips []mergequeue.Tip // the branches' tips this pass read: a review request's head (board-drift §3.2)
	switch {
	case f.Kind == forge.GitHub && c.hasGH(env):
		source = "gh"
		cctx, cancel = call()
		prs, err := c.github(cctx, c.dir, env, ci.on) // the rollup only for the CI loop
		cancel()
		if err != nil {
			return fail(source, err.Error())
		}
		in.PRs, ci.prs = prs, prs
		branchTips = remote
		for _, pr := range prs { // drafts and heads in review too: a fix under review keeps its field (§4)
			open[mergequeue.ConflictField(pr.Number, pr.Branch)] = true
		}
	default: // the git fallback: GitHub without gh, GitLab and Bitbucket (no reader yet), local; no CI loop
		ci.on = false
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
		branchTips = tips
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
		// A live branch, unless its tip is in the base: approved, judged
		// above; a tracked one's other tip, judged here (a reset or a
		// fast-forward to the base, reviewer-codex); a read that fails
		// keeps it open.
		tracked, terr := c.b.ConflictRecord(ctx)
		var others []string
		for _, tp := range tips {
			if _, ok := tracked[mergequeue.ConflictField(0, tp.Branch)]; ok && terr == nil && !approved[tp.Head] {
				others = append(others, tp.Head)
			}
		}
		var in3 map[string]bool
		if len(others) > 0 {
			cctx, cancel = call()
			in3, _ = c.inBase(cctx, c.dir, env, baseTip, others)
			cancel()
		}
		for _, tp := range tips {
			if !in2[tp.Head] && !in3[tp.Head] {
				open[mergequeue.ConflictField(0, tp.Branch)] = true
			}
		}
	}
	res := mergequeue.Derive(in)
	checkTip := judgeBase
	if checkTip == "" {
		checkTip = remoteBase // the gh path: origin's base
	}
	res, conflictCheck := c.checkConflicts(ctx, env, res, checkTip, base, f.Kind != forge.Local)
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
	mc := c.closeOnMerge(ctx, env, branchTips, judgeBase, base)
	cf.pass = mergequeue.ConflictPass{Base: base, Local: f.Kind == forge.Local, Now: now, Conflicting: res.Conflicting, Open: open}
	for _, it := range res.Clean {
		cf.pass.Clean = append(cf.pass.Clean, mergequeue.ConflictField(it.Number, it.Branch))
	}
	return bus.MergeQueueRecord{Source: source, Forge: forgeKind, Base: base, Items: res.Items, Unapproved: res.Unapproved, Conflicting: res.Conflicting,
		Undelivered: undelivered, ConflictCheck: conflictCheck}, run, ci, cf, mc
}

// checkConflicts judges the approved heads and the forge-flagged PRs with
// git (conflict-owner §2), oldest approved first: a conflict leaves the
// items for Conflicting with its branch, head, base tip and files; a flagged
// PR git finds clean is an item again; one git could not judge stays as the
// forge said. why says what was not judged, "" when all were.
func (c *mergeQueueChore) checkConflicts(ctx context.Context, env []string, res mergequeue.Result, baseTip, base string, fetch bool) (mergequeue.Result, string) {
	if c.conflicts == nil || len(res.Items)+len(res.Flagged) == 0 {
		return res, ""
	}
	type cand struct {
		it      bus.MergeItem
		flagged bool
	}
	var all []cand
	var notes []string
	items, conflicting := []bus.MergeItem{}, []bus.MergeConflict{}
	keep := func(it bus.MergeItem, flagged bool) { // as the forge said
		if flagged {
			conflicting = append(conflicting, bus.MergeConflict{Number: it.Number, Base: res.Bases[it.Number], Branch: it.Branch, Head: it.Head})
		} else {
			items = append(items, it)
		}
	}
	for _, list := range []struct {
		its     []bus.MergeItem
		flagged bool
	}{{res.Items, false}, {res.Flagged, true}} {
		for _, it := range list.its {
			if b := res.Bases[it.Number]; b != "" && b != base { // onto another base: never judged against this one (reviewer-codex)
				notes = append(notes, fmt.Sprintf("#%d onto %s: not checked against %s", it.Number, b, base))
				keep(it, list.flagged)
				continue
			}
			all = append(all, cand{it, list.flagged})
		}
	}
	if len(all) == 0 {
		res.Items, res.Conflicting, res.Flagged = items, conflicting, nil
		return res, strings.Join(capNotes(notes), "; ")
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].it.ApprovedAt < all[j].it.ApprovedAt })
	key := func(it bus.MergeItem) string {
		if it.Number > 0 {
			return "#" + strconv.Itoa(it.Number)
		}
		return it.Branch
	}
	cands := make([]mergequeue.Candidate, len(all))
	for i, cd := range all {
		cands[i] = mergequeue.Candidate{Key: key(cd.it), Branch: cd.it.Branch, Head: cd.it.Head}
	}
	judged, why := c.conflicts(ctx, mergequeue.ConflictCheck{Dir: c.dir, Scratch: c.scratch, Env: env, Fetch: fetch, Call: mergeQueueCall}, baseTip, cands)
	if why != "" {
		return res, strings.Join(capNotes(append([]string{why}, notes...)), "; ")
	}
	for _, cd := range all {
		j, k := judged[key(cd.it)], key(cd.it)
		cf := bus.MergeConflict{Number: cd.it.Number, Base: base, Branch: cd.it.Branch, Head: cd.it.Head}
		switch {
		case j.Why != "":
			notes = append(notes, k+": "+j.Why)
			keep(cd.it, cd.flagged)
		case j.Conflict:
			cf.BaseTip, cf.Files = baseTip, j.Files
			conflicting = append(conflicting, cf)
		default:
			items = append(items, cd.it)
			res.Clean = append(res.Clean, cd.it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ApprovedAt < items[j].ApprovedAt })
	res.Items, res.Conflicting, res.Flagged = items, conflicting, nil
	return res, strings.Join(capNotes(notes), "; ")
}

// capNotes keeps the first conflictNotes, then says how many more.
func capNotes(notes []string) []string {
	if len(notes) > conflictNotes {
		return append(notes[:conflictNotes:conflictNotes], fmt.Sprintf("%d more", len(notes)-conflictNotes))
	}
	return notes
}

// conflictNotes is how many unjudged candidates the record names.
const conflictNotes = 5

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
