package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/plan"
)

const planUsage = "usage: plan show [--json] | plan load <path>@<commit> | plan link <task> --pr <n> | plan done <task> | plan note <task> <text...> | plan clear"

// runPlan publishes and reads the project's plan (spec
// docs/superpowers/specs/2026-10-22-plan-view-design.md). It writes Redis
// only, never the file; repo is where load reads the file from Git.
func runPlan(ctx context.Context, b *bus.Bus, repo string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		args = []string{"show"}
	}
	verb, rest := args[0], args[1:]
	fail := func(err error) int {
		fmt.Fprintf(errw, "plan %s: %v\n", verb, err)
		return 1
	}
	switch verb {
	case "show":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) != 0 {
			break
		}
		return planShow(ctx, b, asJSON, time.Now(), out, errw)
	case "load":
		if len(rest) != 1 {
			break
		}
		msg, err := planLoad(ctx, b, repo, rest[0])
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(out, msg)
		return 0
	case "link":
		rest, pr := extractFlag(rest, "--pr")
		n, err := strconv.Atoi(strings.TrimPrefix(pr, "#"))
		if len(rest) != 1 || err != nil {
			break
		}
		if err := b.PlanLink(ctx, rest[0], n); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "%s linked to #%d\n", rest[0], n)
		return 0
	case "done":
		if len(rest) != 1 {
			break
		}
		if err := b.PlanDone(ctx, rest[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "%s recorded done\n", rest[0])
		return 0
	case "note":
		if len(rest) < 2 {
			break
		}
		if err := b.PlanNote(ctx, rest[0], strings.Join(rest[1:], " ")); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "%s noted\n", rest[0])
		return 0
	case "clear":
		if len(rest) != 0 {
			break
		}
		if err := b.ClearPlan(ctx); err != nil {
			return fail(err)
		}
		fmt.Fprintln(out, "plan cleared")
		return 0
	}
	fmt.Fprintln(errw, planUsage)
	return 2
}

// planLoad reads <path>@<commit> from Git, validates it and publishes it.
func planLoad(ctx context.Context, b *bus.Bus, repo, ref string) (string, error) {
	i := strings.LastIndex(ref, "@")
	if i <= 0 || i == len(ref)-1 {
		return "", fmt.Errorf("%q is not <path>@<commit>", ref)
	}
	path := ref[:i]
	g := mission.Git{Repo: repo}
	commit, err := g.ResolveCommit(ctx, ref[i+1:])
	if err != nil {
		return "", err
	}
	data, err := g.Show(ctx, commit, path)
	if err != nil {
		return "", fmt.Errorf("%s not found at %s", path, commit[:7])
	}
	f, err := plan.Parse(data)
	if err != nil {
		return "", err
	}
	ids := make([]string, len(f.Tasks))
	for i, t := range f.Tasks {
		ids[i] = t.ID
	}
	if err := b.LoadPlan(ctx, path, commit, data, ids); err != nil {
		return "", err
	}
	return fmt.Sprintf("loaded plan %s (%d tasks) from %s@%s", f.Plan, len(f.Tasks), path, commit[:7]), nil
}

type planTaskJSON struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Owner     string   `json:"owner"`
	State     string   `json:"state"`
	Label     string   `json:"label"`
	PR        int      `json:"pr,omitempty"`
	Verdict   string   `json:"verdict,omitempty"`
	Note      string   `json:"note,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	WaitingOn []string `json:"waiting_on,omitempty"`
	Ask       string   `json:"ask,omitempty"`
	Quiet     bool     `json:"quiet,omitempty"`
	LastFact  int64    `json:"last_fact,omitempty"`
}

func planShow(ctx context.Context, b *bus.Bus, asJSON bool, now time.Time, out, errw io.Writer) int {
	act, ok, err := plan.Load(ctx, b)
	if err != nil {
		fmt.Fprintf(errw, "plan show: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(errw, "plan show: no plan is loaded (agentbus plan load <path>@<commit>)")
		return 1
	}
	in, errs := plan.Read(ctx, b, act.File)
	rows := plan.Derive(act.File, in)
	done, total := plan.Count(rows)
	for _, e := range errs {
		fmt.Fprintf(errw, "plan show: %v\n", e)
	}
	code := 0
	if len(errs) > 0 {
		code = 1
	}
	if asJSON {
		js := struct {
			Plan     string         `json:"plan"`
			Goal     string         `json:"goal"`
			Path     string         `json:"path"`
			Commit   string         `json:"commit"`
			LoadedAt int64          `json:"loaded_at"`
			Stale    bool           `json:"stale,omitempty"`
			Done     int            `json:"done"`
			Total    int            `json:"total"`
			Tasks    []planTaskJSON `json:"tasks"`
		}{Plan: act.File.Plan, Goal: act.File.Goal, Path: act.Record.Path, Commit: act.Record.Commit,
			LoadedAt: act.Record.LoadedAt, Stale: now.Sub(time.UnixMilli(act.Record.LoadedAt)) > plan.StaleAfter, Done: done, Total: total}
		for _, r := range rows {
			js.Tasks = append(js.Tasks, planTaskJSON{ID: r.Task.ID, Title: r.Task.Title, Owner: r.Task.Owner, State: string(r.State),
				Label: rowLabel(r, now), PR: r.PR, Verdict: r.Verdict, Note: r.Note, Reason: r.Reason, WaitingOn: r.WaitingOn,
				Ask: r.Ask, Quiet: plan.Quiet(r, now), LastFact: r.LastFact})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.Encode(js)
		return code
	}
	head := fmt.Sprintf("plan %s %d/%d · %s · loaded %s ago (%s)", act.File.Plan, done, total, act.File.Goal,
		plan.Age(act.Record.LoadedAt, now), short7(act.Record.Commit))
	if now.Sub(time.UnixMilli(act.Record.LoadedAt)) > plan.StaleAfter {
		head += " · stale (master: agentbus plan load)"
	}
	fmt.Fprintln(out, head)
	for _, s := range plan.Order {
		var group []plan.Row
		for _, r := range rows {
			if r.State == s {
				group = append(group, r)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s (%d)\n", plan.Label(s), len(group))
		for _, r := range group {
			fmt.Fprintf(out, "  %-16s %s  [%s]%s\n", r.Task.ID, r.Task.Title, r.Task.Owner, rowDetails(r, now))
		}
	}
	return code
}

// rowLabel is the state as a row reads it.
func rowLabel(r plan.Row, now time.Time) string {
	if r.State == plan.Done {
		return plan.DoneLabel(r, now)
	}
	return plan.Label(r.State)
}

func rowDetails(r plan.Row, now time.Time) string {
	var parts []string
	if r.PR > 0 {
		parts = append(parts, fmt.Sprintf("#%d", r.PR))
	}
	if r.Verdict != "" && r.State != plan.Done {
		parts = append(parts, r.Verdict)
	}
	if r.State == plan.Done {
		parts = append(parts, plan.DoneLabel(r, now))
	} else if r.Reason != "" {
		parts = append(parts, r.Reason)
	}
	if len(r.WaitingOn) > 0 && r.State == plan.Upcoming {
		parts = append(parts, "waiting on "+strings.Join(r.WaitingOn, ", "))
	}
	if r.Ask != "" {
		parts = append(parts, "ask "+r.Ask)
	}
	if plan.Quiet(r, now) {
		parts = append(parts, "quiet "+plan.Age(r.LastFact, now))
	}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " · ")
}

func short7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
