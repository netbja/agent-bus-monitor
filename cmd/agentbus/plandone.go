package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/plan"
)

// planDoneHead is plan done's guard (onboarding §9.3): for an agent task
// with a check, the merged head, read by the command itself (a linked PR
// through the forge, a ref's sha, a merge:false request's commit), once the
// deciding approval is shown to have checked exactly it; "" for a task the
// rule does not reach, which records done as before. Any read that fails
// refuses (fail closed).
func planDoneHead(ctx context.Context, b *bus.Bus, repo string, getenv func(string) string, id string) (string, error) {
	rec, has, err := b.Plan(ctx)
	if err != nil || !has {
		return "", nil // the write itself says there is no plan
	}
	f, err := plan.Parse([]byte(rec.File))
	if err != nil {
		return "", fmt.Errorf("the loaded plan does not parse: %w", err)
	}
	var t plan.Task
	for _, x := range f.Tasks {
		if x.ID == id {
			t = x
		}
	}
	if !plan.Gated(t) {
		return "", nil
	}
	facts, err := b.PlanFacts(ctx)
	if err != nil {
		return "", err
	}
	board, err := b.Board(ctx)
	if err != nil {
		return "", err
	}
	in := plan.Inputs{Board: board, Facts: facts, Verdicts: map[string][]bus.Verdict{}}
	fact := facts[id]
	for _, s := range plan.Subjects(t, fact) {
		vs, err := b.Verdicts(ctx, s)
		if err != nil {
			return "", err
		}
		in.Verdicts[s] = vs
	}
	src, err := plan.DoneSource(t, fact, in)
	if err != nil {
		return "", err
	}
	head := src.Head
	if src.PR > 0 {
		if head, err = planMergedPR(ctx, repo, getenv, src.PR); err != nil {
			return "", fmt.Errorf("cannot read PR %d's merged head: %w", src.PR, err)
		}
		if !bus.ValidHead(head) {
			return "", fmt.Errorf("cannot read PR %d's merged head: %q is no full sha", src.PR, head)
		}
	}
	return head, plan.CheckedDone(t, fact, in, head)
}

// planMergedPR reads a merged PR's head through the forge deliver uses
// (delivery.forge, origin): GitHub's headRefOid with state MERGED, GitLab's
// sha with state merged; any other forge has no reader. A seam for tests.
var planMergedPR = func(ctx context.Context, repo string, getenv func(string) string, pr int) (string, error) {
	origin, err := exec.CommandContext(ctx, "git", "-C", repo, "remote", "get-url", "origin").Output()
	has := err == nil && strings.TrimSpace(string(origin)) != ""
	f := forge.Resolve(config.Machine(getenv).String("delivery.forge"), strings.TrimSpace(string(origin)), has)
	var out []byte
	switch f.Kind {
	case forge.GitHub:
		out, err = exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(pr), "--json", "state,headRefOid").Output()
	case forge.GitLab:
		out, err = exec.CommandContext(ctx, "glab", "mr", "view", strconv.Itoa(pr), "--output", "json").Output()
	default:
		return "", fmt.Errorf("%w (%s)", plan.ErrNoReader, f.Kind)
	}
	if err != nil {
		return "", err
	}
	return mergedHeadOf(f.Kind, out)
}

// mergedHeadOf reads the head of a merged PR from the forge's JSON.
func mergedHeadOf(k forge.Kind, out []byte) (string, error) {
	var v struct {
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"` // GitHub
		SHA        string `json:"sha"`        // GitLab
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	head := v.HeadRefOid
	if k == forge.GitLab {
		head = v.SHA
	}
	if !strings.EqualFold(v.State, "merged") {
		return "", fmt.Errorf("it is %s, not merged", strings.ToLower(v.State))
	}
	return head, nil
}
