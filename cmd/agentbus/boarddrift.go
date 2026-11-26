package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// board drift (board-drift §5): a read-only report, one line per finding,
// for the sentinel's daily review. It never closes, reassigns or edits a
// task: that stays with the owner, the sender or master.

// driftInput is everything boardDrift judges, read by the caller. Live nil:
// the holder was not read; Manifest nil: no roles.toml read.
type driftInput struct {
	Project   string
	Merged    []dispatchsvc.DriftMerged
	MergedWhy string // why the merged branches were not judged ("" judged)
	Board     map[string]bus.BoardEntry
	Agents    map[string]bus.AgentSnapshot
	Live      map[string]bool
	Manifest  *rolecfg.Manifest
	Resume    map[string]bus.ResumeRecord
}

// driftFinding is one line: its kind (merged, unjudged, body-lost,
// no-terminal, stranded-pop, not-judged), the task or agent it names.
type driftFinding struct {
	Kind  string `json:"kind"`
	Task  string `json:"task,omitempty"`
	Agent string `json:"agent,omitempty"`
	Text  string `json:"text"`
}

// boardDrift lists §5's findings: open requests on merged branches, bodies
// lost, agents with no terminal; then what could not be judged.
func boardDrift(in driftInput) []driftFinding {
	var out []driftFinding
	var notJudged []driftFinding
	skip := func(what, why string) {
		notJudged = append(notJudged, driftFinding{Kind: "not-judged", Text: "not judged: " + what + ": " + why})
	}
	if in.MergedWhy != "" {
		skip("merged branches", in.MergedWhy)
	}
	for _, m := range in.Merged {
		if m.Unread != "" { // a head not read is never "not merged" (reviewer-codex, foureyes)
			notJudged = append(notJudged, driftFinding{Kind: "not-judged", Task: m.Task, Agent: m.Owner, Text: fmt.Sprintf(
				"not judged: %s (%s, %s): %s", m.Task, m.State, m.Owner, m.Unread)})
			continue
		}
		if m.Unjudged {
			out = append(out, driftFinding{Kind: "unjudged", Task: m.Task, Agent: m.Owner, Text: fmt.Sprintf(
				"cannot be judged: %s (%s, %s): no branch tip and no recorded commit; record it: agentbus request review %s --commit <sha>",
				m.Task, m.State, m.Owner, m.Task)})
			continue
		}
		out = append(out, driftFinding{Kind: "merged", Task: m.Task, Agent: m.Owner, Text: fmt.Sprintf(
			"merged: %s (%s, %s): branch %s head %s is in %s; its owner closes it (agentbus request done %s \"…\") or, for a step, sends its review",
			m.Task, m.State, m.Owner, m.Branch, shortHead(m.Head), m.Base, m.Task)})
	}

	tasks := make([]string, 0, len(in.Board))
	for t := range in.Board {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	for _, t := range tasks {
		e := in.Board[t]
		r := e.Request
		if r == nil || !bus.BoardOwes(e.State) || r.Availability != "missing" {
			continue
		}
		out = append(out, driftFinding{Kind: "body-lost", Task: t, Agent: r.Target, Text: fmt.Sprintf(
			"body lost: %s (%s, %s → %s): its body is gone from the stream; %s or %s closes it: agentbus request done %s \"<why>\"",
			t, e.State, r.From, r.Target, r.Target, r.From, t)})
	}

	if in.Live == nil {
		skip("terminals", "the project's holder was not read")
	}
	if in.Manifest == nil {
		skip("required roles", "no roles.toml read")
	}
	if in.Live != nil {
		names := make([]string, 0, len(in.Agents))
		for n := range in.Agents {
			names = append(names, n)
		}
		sort.Strings(names)
		gone := func(n string) bool {
			s := in.Agents[n]
			return bus.ShownState(s.State, s.Pane, true, in.Live[n]) == bus.StateNoTerminal
		}
		if in.Manifest != nil {
			for _, n := range names {
				if in.Manifest.RoleRequired(n) && gone(n) {
					out = append(out, driftFinding{Kind: "no-terminal", Agent: n, Text: "no terminal: " + n + " is required and has no terminal"})
				}
			}
		}
		for _, n := range names {
			pop := in.Agents[n].Tier == "pop" || in.Manifest != nil && in.Manifest.RoleTier(n) == "pop"
			if !pop || !gone(n) {
				continue
			}
			var owned []string
			for _, t := range tasks {
				if e := in.Board[t]; e.Owner == n && bus.BoardOwes(e.State) {
					owned = append(owned, fmt.Sprintf("%s (%s)", t, e.State))
				}
			}
			if len(owned) == 0 {
				continue
			}
			back := "agentbus --project " + in.Project + " pop " + n
			if r, ok := in.Resume[n]; ok && bus.ValidSessionID(r.Session) {
				back += " --resume " + r.Session
			}
			out = append(out, driftFinding{Kind: "stranded-pop", Agent: n, Text: "no terminal: " + dispatch.PopStrandedText(n, owned, back)})
		}
	}
	return append(out, notJudged...)
}

// runBoardDrift reads §5's inputs for project (git in the current
// directory, as the merge queue's) and prints the findings.
func runBoardDrift(ctx context.Context, b *bus.Bus, project string, asJSON bool) int {
	board, err := b.Board(ctx)
	if err != nil {
		die(err.Error())
	}
	agents, err := b.Agents(ctx)
	if err != nil {
		die(err.Error())
	}
	resume, err := b.ResumeRecords(ctx)
	if err != nil {
		die(err.Error())
	}
	in := driftInput{Project: project, Board: board, Agents: agents, Resume: resume, Live: holderLive(ctx, project)}
	if path, err := manifestPath(os.Environ()); err == nil && path != "" {
		if m, err := rolecfg.Load(path); err == nil {
			in.Manifest = m
		}
	}
	in.Merged, in.MergedWhy = dispatchsvc.BoardDriftMerged(ctx, b, os.Getenv)
	return printDrift(os.Stdout, boardDrift(in), asJSON)
}

// shortHead is a head's first seven characters.
func shortHead(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// printDrift prints the findings (--json: as a list), "no drift" when none;
// its exit code is 1 when there is drift.
func printDrift(w io.Writer, fs []driftFinding, asJSON bool) int {
	if asJSON {
		if fs == nil {
			fs = []driftFinding{}
		}
		b, _ := json.MarshalIndent(fs, "", "  ")
		fmt.Fprintln(w, string(b))
	} else if len(fs) == 0 {
		fmt.Fprintln(w, "no drift")
	}
	if len(fs) == 0 {
		return 0
	}
	if !asJSON {
		for _, f := range fs {
			fmt.Fprintln(w, f.Text)
		}
	}
	return 1
}
