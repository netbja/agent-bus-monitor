package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// Sibling steps run in series (docs/superpowers/specs/2026-11-26-sibling-enforce-design.md
// §3): before a review of another branch, or the target's done, the CLI
// judges each open step of the request with the dispatcher's own test
// (mergequeue.Delivery.Delivered: ancestor or same tree, squash, rebase) in
// the agent's checkout, and passes the ones delivered to the board script,
// which refuses while one stays listed. The bus cannot run git and the
// dispatcher runs no repository code: the CLI is the honest path, not a
// boundary; the audit line names the head and the base tip it used.

// stepJudger is what the judgement reads: git in the checkout, the forge
// setting, the project's delivery base ("" none), and the delivered test.
type stepJudger struct {
	git       reviewGit
	forge     string
	base      string
	delivered func(ctx context.Context, baseTip string, heads []string) (map[string]bool, error)
}

// realStepJudger is the judger in dir for project.
func realStepJudger(dir, project string) stepJudger {
	base, _, err := forge.ProjectDeliveryBase(os.Getenv, project)
	if err != nil {
		base = ""
	}
	d := mergequeue.NewDelivery()
	return stepJudger{git: reviewGitIn(dir), forge: config.Machine(os.Getenv).String("delivery.forge"), base: base,
		delivered: func(ctx context.Context, baseTip string, heads []string) (map[string]bool, error) {
			return d.Delivered(ctx, dir, os.Environ(), baseTip, heads)
		}}
}

// stepsToJudge are the open steps a transition must judge: every one for
// done; for a review of branch current, every other one unless parallel is
// set, and none without a branch (the script refuses that review itself).
func stepsToJudge(r *bus.RequestInfo, current string, done bool) []bus.OpenStep {
	var out []bus.OpenStep
	for _, s := range r.OpenSteps {
		switch {
		case done:
			out = append(out, s)
		case r.Parallel != nil || current == "" || s.Branch == current:
		default:
			out = append(out, s)
		}
	}
	return out
}

// judge is StepsJudged for r before a review of current (done: before the
// target's done): N r's review_n as read, Merged the steps delivered into
// the base. Exactly one head is judged per step: its remote tip while the
// branch exists; else (the remote deleted after the merge, or local mode)
// the newer of its local tip and its recorded commit (localOrReviewed). A step
// not delivered is left out, and the board script refuses with its name. A
// failed git read, or no base to judge against, is an error: nothing is
// written.
func (j stepJudger) judge(ctx context.Context, r *bus.RequestInfo, current string, done bool) (bus.StepsJudged, error) {
	out := bus.StepsJudged{N: r.ReviewN}
	steps := stepsToJudge(r, current, done)
	if len(steps) == 0 {
		return out, nil
	}
	if j.base == "" {
		return out, fmt.Errorf("step %s is open and the project has no delivery base to judge it against", steps[0].Branch)
	}
	resolve := func(ref string) string {
		h, err := j.git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil || !bus.ValidHead(h) {
			return ""
		}
		return h
	}
	url, uerr := j.git("remote", "get-url", "origin")
	has := uerr == nil && url != ""
	local := forge.Resolve(j.forge, url, has).Kind == forge.Local || !has
	var baseTip string
	if local {
		baseTip = resolve("refs/heads/" + j.base)
	} else {
		if _, err := j.git("fetch", "--quiet", "origin", j.base); err != nil {
			return out, fmt.Errorf("judging step %s: git fetch origin %s: %w", steps[0].Branch, j.base, err)
		}
		baseTip = resolve("refs/remotes/origin/" + j.base)
	}
	if baseTip == "" {
		return out, fmt.Errorf("judging step %s: the base %s does not resolve", steps[0].Branch, j.base)
	}
	headOf := map[string]string{}
	var heads []string
	for _, s := range steps {
		head := ""
		if !local {
			ls, err := j.git("ls-remote", "--heads", "origin", "refs/heads/"+s.Branch)
			if err != nil {
				return out, fmt.Errorf("judging step %s: git ls-remote: %w", s.Branch, err)
			}
			if f := strings.Fields(ls); len(f) > 0 && bus.ValidHead(f[0]) {
				if _, err := j.git("fetch", "--quiet", "origin", "refs/heads/"+s.Branch); err != nil {
					return out, fmt.Errorf("judging step %s: git fetch: %w", s.Branch, err)
				}
				head = f[0]
			}
		}
		if head == "" { // no remote tip: the local tip or the reviewed commit, the newer one
			var err error
			if head, err = j.localOrReviewed(s, resolve); err != nil {
				return out, err
			}
		}
		if head == "" {
			continue // nothing to judge: the script refuses with its name
		}
		headOf[s.Branch] = head
		heads = append(heads, head)
	}
	if len(heads) == 0 {
		return out, nil
	}
	in, err := j.delivered(ctx, baseTip, heads)
	if err != nil {
		return out, fmt.Errorf("judging step %s: %w", steps[0].Branch, err)
	}
	for _, s := range steps {
		if h := headOf[s.Branch]; h != "" && in[h] {
			out.Merged = append(out.Merged, bus.StepMerged{Branch: s.Branch, Head: h, Base: baseTip})
		}
	}
	return out, nil
}

// localOrReviewed is the head judged for s with no remote tip: of its local
// tip and its recorded commit, the descendant (reviewer-codex: a stale local
// ref behind the reviewed commit must not clear it, nor a recorded commit
// behind a local fix). A recorded commit this checkout does not hold is
// judged as it is (not here: never delivered); two heads that diverge are
// refused, nothing judged.
func (j stepJudger) localOrReviewed(s bus.OpenStep, resolve func(string) string) (string, error) {
	tip := resolve("refs/heads/" + s.Branch)
	switch {
	case tip == "":
		return s.Commit, nil
	case s.Commit == "" || tip == s.Commit:
		return tip, nil
	case resolve(s.Commit) == "":
		return s.Commit, nil
	}
	ancestor := func(a, b string) bool {
		_, err := j.git("merge-base", "--is-ancestor", a, b)
		return err == nil
	}
	switch {
	case ancestor(s.Commit, tip):
		return tip, nil
	case ancestor(tip, s.Commit):
		return s.Commit, nil
	}
	return "", fmt.Errorf("step %s: its local tip %s and the reviewed commit %s diverge: review the branch again, or ask the sender to abandon the step", s.Branch, tip[:8], s.Commit[:8])
}

// judgeTask reads task's request and judges its open steps for a review of
// current, or the target's done; a request the board does not hold is
// judged as having none (the script refuses it).
func judgeTask(ctx context.Context, b *bus.Bus, j stepJudger, task, current string, done bool) (bus.StepsJudged, error) {
	board, err := b.Board(ctx)
	if err != nil {
		return bus.StepsJudged{}, err
	}
	e, ok := board[task]
	if !ok || e.Request == nil {
		return bus.StepsJudged{}, nil
	}
	return j.judge(ctx, e.Request, current, done)
}

// auditStepsMerged writes one step-merged line per step applied, after the
// transition (a trail never fails the act).
func auditStepsMerged(ctx context.Context, b *bus.Bus, self, task string, s bus.StepsJudged) {
	for _, m := range s.Merged {
		b.AuditAfter(ctx, bus.AuditEntry{Action: "step-merged", Actor: self, Target: m.Branch, Outcome: bus.AuditOK,
			Detail: "request " + task + ": head " + m.Head[:8] + " in base " + m.Base[:8]})
	}
}

// refuseAgentAs refuses --as <person> from an agent: the person's act runs
// from the person's own shell (request priority's rule).
func refuseAgentAs(ctx context.Context, d requestCallerDeps, what string) error {
	if self := d.Getenv("AGENT_BUS_AGENT"); self != "" {
		return fmt.Errorf("--as: %s as the person is not %s's act (run it from your own shell)", what, self)
	}
	if d.AgentCaller == nil {
		return nil
	}
	switch caller, err := d.AgentCaller(ctx); {
	case errors.Is(err, errNoAncestry):
	case err != nil:
		return fmt.Errorf("cannot check that no registered agent runs this (%v)", err)
	case caller != "":
		return fmt.Errorf("--as: %s as the person, and this runs under the agent %s", what, caller)
	}
	return nil
}

// stepsActor is who sets parallel or abandons a step: the person with --as
// (no agent may claim it), else the caller itself (the sender, checked by
// the board).
func stepsActor(ctx context.Context, self, as, what string) (by string, person bool, err error) {
	if as == "" {
		return self, false, nil
	}
	if !bus.ValidName(as) {
		return "", false, fmt.Errorf("--as %q is not a valid name", as)
	}
	if err := refuseAgentAs(ctx, requestDeps, what); err != nil {
		return "", false, err
	}
	return as, true, nil
}

// requestParallel: request parallel <task> <reason...> [--as <person>] |
// request parallel <task> --off [--as <person>].
func requestParallelCmd(ctx context.Context, b *bus.Bus, self string, args []string, out io.Writer) error {
	args, as := extractFlag(args, "--as")
	args, off := extractBool(args, "--off")
	if len(args) < 1 || (off && len(args) != 1) || (!off && len(args) < 2) {
		return fmt.Errorf("usage: request parallel <task> <reason...> [--as <person>] | request parallel <task> --off [--as <person>]")
	}
	task, reason := args[0], strings.Join(args[1:], " ")
	by, person, err := stepsActor(ctx, self, as, "a request's parallel exception")
	if err != nil {
		return err
	}
	changed, err := b.SetRequestParallel(ctx, task, by, reason, off, person)
	if err != nil {
		return fmt.Errorf("request parallel: %w", err)
	}
	e := bus.AuditEntry{Action: "parallel", Actor: by, Target: task, Outcome: bus.AuditOK, Detail: "set: " + reason}
	if person {
		e.Human = "yes"
	}
	if off {
		e.Detail = "off"
	}
	switch {
	case off && !changed:
		_, err = fmt.Fprintf(out, "%s: parallel already off\n", task)
		return err
	case off:
		b.AuditAfter(ctx, e)
		_, err = fmt.Fprintf(out, "%s: parallel off (every listed step is judged at the next review or done)\n", task)
		return err
	}
	b.AuditAfter(ctx, e)
	_, err = fmt.Fprintf(out, "%s: parallel (%s)\n", task, reason)
	return err
}

// requestStepCmd: request step <task> <branch> --abandon <reason...> [--as <person>].
func requestStepCmd(ctx context.Context, b *bus.Bus, self string, args []string, out io.Writer) error {
	args, as := extractFlag(args, "--as")
	args, abandon := extractBool(args, "--abandon")
	if !abandon || len(args) < 3 {
		return fmt.Errorf("usage: request step <task> <branch> --abandon <reason...> [--as <person>]")
	}
	task, branch, reason := args[0], args[1], strings.Join(args[2:], " ")
	by, person, err := stepsActor(ctx, self, as, "abandoning a request's step")
	if err != nil {
		return err
	}
	head, err := b.AbandonStep(ctx, task, branch, by, reason, person)
	if err != nil {
		return fmt.Errorf("request step: %w", err)
	}
	e := bus.AuditEntry{Action: "step-abandoned", Actor: by, Target: branch, Outcome: bus.AuditOK, Detail: "request " + task + ": " + reason}
	if person {
		e.Human = "yes"
	}
	b.AuditAfter(ctx, e)
	if head != "" {
		head = " at " + head[:8]
	}
	_, err = fmt.Fprintf(out, "%s: step %s abandoned%s\n", task, branch, head)
	return err
}

// judgeDone judges task's open steps before self's done: only the target's
// done waits for them (the sender's own done comes before any accept, when
// none is listed). pending is the first listed step not delivered ("" none),
// with the sender to ask, so report --done refuses before it publishes.
func judgeDone(ctx context.Context, b *bus.Bus, self, task string) (steps bus.StepsJudged, pending, from string, err error) {
	board, err := b.Board(ctx)
	if err != nil {
		return bus.StepsJudged{}, "", "", err
	}
	e, ok := board[task]
	if !ok || e.Request == nil {
		return bus.StepsJudged{}, "", "", nil
	}
	r := e.Request
	steps.N = r.ReviewN
	if r.Target != self || len(r.OpenSteps) == 0 {
		return steps, "", r.From, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return steps, "", r.From, err
	}
	if steps, err = realStepJudger(dir, b.Project()).judge(ctx, r, "", true); err != nil {
		return steps, "", r.From, err
	}
	merged := map[string]bool{}
	for _, m := range steps.Merged {
		merged[m.Branch] = true
	}
	for _, s := range r.OpenSteps {
		if !merged[s.Branch] {
			return steps, s.Branch, r.From, nil
		}
	}
	return steps, "", r.From, nil
}
