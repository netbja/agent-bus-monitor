package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/onboard"
	"github.com/netbja/agent-bus-monitor/plan"
)

const planDraftUsage = "usage: plan draft <path>@<commit> [--build <path>] | plan draft show [--json] | plan draft --drop"

// draftCallers may draft and drop (onboarding §8.1): never another agent,
// never the person, who approves drafts and so never asks.
var draftCallers = map[string]bool{"master": true, "architect": true}

// runPlanDraft is `plan draft` (onboarding §8.1, spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md): record a
// plan file at a commit as the draft awaiting the person's yes, show it, or
// drop it.
func runPlanDraft(ctx context.Context, b *bus.Bus, repo string, getenv func(string) string, args []string, out, errw io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(errw, "plan draft: %v\n", err)
		return 1
	}
	if len(args) >= 1 && args[0] == "show" {
		rest, asJSON := extractBool(args[1:], "--json")
		if len(rest) != 0 {
			fmt.Fprintln(errw, planDraftUsage)
			return 2
		}
		if err := planDraftShow(ctx, b, asJSON, out); err != nil {
			return fail(err)
		}
		return 0
	}
	caller := getenv("AGENT_BUS_AGENT")
	if !draftCallers[caller] {
		who := "the person's shell (no AGENT_BUS_AGENT)"
		if caller != "" {
			who = caller + "'s agent environment"
		}
		fmt.Fprintf(errw, "plan draft: drafts are master's or the architect's, and the person approves them (this is %s)\n", who)
		return 2
	}
	rest, drop := extractBool(args, "--drop")
	if drop {
		if len(rest) != 0 {
			fmt.Fprintln(errw, planDraftUsage)
			return 2
		}
		msg, err := planDraftDrop(ctx, b, caller)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(out, msg)
		return 0
	}
	rest, buildPath := extractFlag(rest, "--build")
	if len(rest) != 1 {
		fmt.Fprintln(errw, planDraftUsage)
		return 2
	}
	d, err := planDraft(ctx, b, mission.Git{Repo: repo}, caller, rest[0], buildPath)
	if err != nil {
		return fail(err)
	}
	return printJSON(out, struct {
		QID string `json:"question_id"`
		Ask string `json:"ask"`
	}{d.QID, d.Ask})
}

func printJSON(out io.Writer, v any) int {
	if err := json.NewEncoder(out).Encode(v); err != nil {
		return 1
	}
	return 0
}

// planDraft is §8.1's steps 1 to 7.
func planDraft(ctx context.Context, b *bus.Bus, g mission.Git, caller, ref, buildPath string) (bus.PlanDraft, error) {
	i := strings.LastIndex(ref, "@")
	if i <= 0 || i == len(ref)-1 {
		return bus.PlanDraft{}, fmt.Errorf("%q is not <path>@<commit>", ref)
	}
	path := ref[:i]
	commit, err := g.ResolveCommit(ctx, ref[i+1:])
	if err != nil {
		return bus.PlanDraft{}, err
	}
	data, err := g.Show(ctx, commit, path)
	if err != nil {
		return bus.PlanDraft{}, fmt.Errorf("%s not found at %s", path, commit[:7])
	}
	f, err := plan.Parse(data)
	if err != nil {
		return bus.PlanDraft{}, err
	}
	if buildPath == "" {
		buildPath = "BUILD.md"
	}
	build, err := g.Show(ctx, commit, buildPath)
	if err != nil {
		if f.Checks == plan.ChecksRequired {
			return bus.PlanDraft{}, fmt.Errorf("%s not found at %s: a plan with checks required needs its build sheet", buildPath, commit[:7])
		}
		build = nil
	}
	goal := ""
	if bs, err := onboard.ParseBuild(build); err == nil {
		goal = bs.Goal
	} else if f.Checks == plan.ChecksRequired {
		return bus.PlanDraft{}, err
	}
	if err := planMissions(ctx, g, commit, f); err != nil {
		return bus.PlanDraft{}, err
	}
	// The draft over an active plan (FileDiff, DraftDiff, stops) is step b2.
	if rec, ok, err := b.Plan(ctx); err != nil {
		return bus.PlanDraft{}, err
	} else if ok {
		return bus.PlanDraft{}, fmt.Errorf("plan %s@%s is active: a draft over an active plan is not built yet (onboarding step b2)", rec.Path, shortSHA(rec.Commit))
	}
	qid := bus.PlanDraftQID(path, data, build, "", nil)
	prev, drafted, err := b.PlanDraft(ctx)
	if err != nil {
		return bus.PlanDraft{}, err
	}
	if drafted && prev.By != caller {
		return bus.PlanDraft{}, fmt.Errorf("the draft is %s's: plan draft --drop it first", prev.By)
	}
	open, err := openAskOf(ctx, b, caller)
	if err != nil {
		return bus.PlanDraft{}, err
	}
	replaces := ""
	switch {
	case drafted && open == prev.Ask: // §8.1 step 6: replace the draft's own open ask
		replaces = prev.Ask
	case open != "":
		return bus.PlanDraft{}, fmt.Errorf("you have ask %s open: wait for its answer or withdraw it, then run plan draft again", open)
	}
	askID, _, err := b.Ask(ctx, draftAskDef(caller, f, path, qid, goal, commit), replaces)
	if err != nil {
		return bus.PlanDraft{}, err
	}
	d := bus.PlanDraft{Plan: f.Plan, Path: path, Commit: commit, File: string(data), Build: string(build), BuildPath: buildPath,
		QID: qid, Ask: askID, By: caller}
	if err := b.SetPlanDraft(ctx, d, prev); err != nil {
		if werr := b.Withdraw(ctx, caller, askID); werr != nil {
			return bus.PlanDraft{}, fmt.Errorf("%w (and withdrawing ask %s: %v)", err, askID, werr)
		}
		return bus.PlanDraft{}, fmt.Errorf("%w (ask %s withdrawn)", err, askID)
	}
	return d, nil
}

// openAskOf is caller's open ask, "" none.
func openAskOf(ctx context.Context, b *bus.Bus, caller string) (string, error) {
	asks, err := b.Asks(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range asks {
		if a.From == caller && a.State == "open" {
			return a.ID, nil
		}
	}
	return "", nil
}

// draftAskDef is the adoption question (§8.1 step 5), built in code from
// the qid's inputs only, so one qid always has one wording: the commit and
// the goal are in its context, which SameQuestion does not compare.
func draftAskDef(from string, f plan.File, path, qid, goal, commit string) bus.AskDef {
	text := func(p string) string {
		return fmt.Sprintf("Adopt plan %s (%s, %d tasks) as the active plan? id %s", f.Plan, p, len(f.Tasks), qid)
	}
	q := text(path)
	if over := len([]rune(q)) - 300; over > 0 {
		q = text(clipRunes(path, len([]rune(path))-over))
	}
	ctxt := "BUILD.md@" + shortSHA(commit)
	if goal != "" {
		ctxt = "Goal: " + clipRunes(goal, 1500) + "\n" + ctxt
	}
	return bus.AskDef{From: from, Title: clipRunes("Adopt plan "+f.Plan, 100), Task: "onboarding",
		Questions: []bus.AskQuestion{{ID: qid, Header: clipRunes("Adopt "+f.Plan, 16), Question: q, Context: ctxt,
			Options: []bus.AskOption{{Label: "yes", Description: "adopt this file as the active plan"},
				{Label: "no", Description: "revise it first (say what in your own words)"},
				{Label: "later", Description: "keep the draft, decide later"}}}}}
}

// clipRunes cuts s to n runes, the last one "…" when cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// planDraftDrop deletes the draft; it withdraws its ask when the caller
// asked it and it is open, and otherwise says it left the ask alone.
func planDraftDrop(ctx context.Context, b *bus.Bus, caller string) (string, error) {
	d, ok, err := b.PlanDraft(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", bus.ErrNoDraft
	}
	open, err := openAskOf(ctx, b, caller)
	if err != nil {
		return "", err
	}
	if err := b.DropPlanDraft(ctx, d); err != nil {
		return "", err
	}
	if d.By == caller && open == d.Ask {
		if err := b.Withdraw(ctx, caller, d.Ask); err != nil {
			return "", fmt.Errorf("draft %s dropped, but withdrawing ask %s: %w", d.QID, d.Ask, err)
		}
		return fmt.Sprintf("draft %s dropped, ask %s withdrawn", d.QID, d.Ask), nil
	}
	return fmt.Sprintf("draft %s dropped; ask %s left alone (%s's, or no longer open): an answer to it approves nothing", d.QID, d.Ask, d.By), nil
}

// planDraftShow prints the record, without its bytes, and TypedApproval's
// result.
func planDraftShow(ctx context.Context, b *bus.Bus, asJSON bool, out io.Writer) error {
	d, ok, err := b.PlanDraft(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return bus.ErrNoDraft
	}
	entry, aerr := b.PlanDraftApproved(ctx, d)
	if aerr != nil && errors.Is(aerr, bus.ErrApprovalArchive) {
		return aerr // a failed read is an error, never a "not approved"
	}
	if asJSON {
		why := ""
		if aerr != nil {
			why = aerr.Error()
		}
		return json.NewEncoder(out).Encode(struct {
			Plan      string `json:"plan"`
			Path      string `json:"path"`
			Commit    string `json:"commit"`
			BuildPath string `json:"build_path"`
			Replaces  string `json:"replaces"`
			Stops     string `json:"stops"`
			QID       string `json:"qid"`
			Ask       string `json:"ask"`
			By        string `json:"by"`
			At        int64  `json:"at"`
			Approved  string `json:"approved,omitempty"`
			NotYet    string `json:"not_approved,omitempty"`
		}{d.Plan, d.Path, d.Commit, d.BuildPath, d.Replaces, d.Stops, d.QID, d.Ask, d.By, d.At, entry, why})
	}
	fmt.Fprintf(out, "draft plan %s: %s@%s (build sheet %s), by %s at %s\n", d.Plan, d.Path, shortSHA(d.Commit), d.BuildPath, d.By,
		time.UnixMilli(d.At).Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "question %s, ask %s\n", d.QID, d.Ask)
	if aerr != nil {
		fmt.Fprintf(out, "not approved: %v\n", aerr)
	} else {
		fmt.Fprintf(out, "approved: the person's yes %s (plan load %s@%s adopts it)\n", entry, d.Path, shortSHA(d.Commit))
	}
	return nil
}
