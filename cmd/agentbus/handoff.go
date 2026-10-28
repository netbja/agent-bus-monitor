package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// handoffDeps is what `handoff` reads and writes, injected for tests.
type handoffDeps struct {
	Self    string // AGENT_BUS_AGENT: only an agent declares its own hand-off
	Ident   func(ctx context.Context) bus.AgentIdent
	Declare func(ctx context.Context, agent, report string, who bus.AgentIdent) (bus.Handoff, error)
	List    func(ctx context.Context) (map[string]bus.Handoff, error)
	Agents  func(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Now     func() time.Time
}

// runHandoff: `handoff [--json]` lists the records, each pending or void (and
// why); `handoff <self> --report <id>` declares the caller's own hand-off,
// carried by its report (lifecycle spec, part A).
func runHandoff(ctx context.Context, d handoffDeps, args []string, stdout, stderr io.Writer) int {
	args, asJSON := extractBool(args, "--json")
	args, report := extractFlag(args, "--report")
	if len(args) == 0 {
		return listHandoffs(ctx, d, asJSON, stdout, stderr)
	}
	agent := args[0]
	if len(args) != 1 || report == "" {
		fmt.Fprintln(stderr, "usage: handoff <self> --report <report-id>")
		return 2
	}
	if agent != d.Self {
		fmt.Fprintf(stderr, "handoff: an agent declares only its own hand-off (you are %s)\n", d.Self)
		return 1
	}
	h, err := d.Declare(ctx, agent, report, d.Ident(ctx))
	switch {
	case errors.Is(err, bus.ErrHandoffNotOwnReport):
		fmt.Fprintf(stderr, "%v: publish it first with agentbus report %s \"<hand-off>\" and pass the id it prints\n", err, agent)
		return 1
	case errors.Is(err, bus.ErrHandoffNotVerified):
		fmt.Fprintf(stderr, "%v: run it from the agent's own client, after an agentbus status from it\n", err)
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "handoff:", err)
		return 1
	}
	if asJSON {
		out, _ := json.Marshal(h)
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "hand-off declared: %s, report %s\n", agent, h.Report)
	return 0
}

func listHandoffs(ctx context.Context, d handoffDeps, asJSON bool, stdout, stderr io.Writer) int {
	recs, err := d.List(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "handoff:", err)
		return 1
	}
	snaps, err := d.Agents(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "handoff:", err)
		return 1
	}
	type row struct {
		Agent string `json:"agent"`
		bus.Handoff
		Void   bool   `json:"void"`
		Reason string `json:"reason,omitempty"`
	}
	rows := make([]row, 0, len(recs))
	for a, h := range recs {
		s, ok := snaps[a]
		void, why := h.VoidFor(s, ok)
		rows = append(rows, row{a, h, void, why})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Agent < rows[j].Agent })
	if asJSON {
		out, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "no hand-off declared")
		return 0
	}
	for _, r := range rows {
		state := "pending"
		if r.Void {
			state = "void: " + r.Reason
		}
		age := d.Now().Sub(time.UnixMilli(r.At)).Round(time.Minute)
		fmt.Fprintf(stdout, "%-12s report %s  %s ago  %s\n", r.Agent, r.Report, shortAge(age), state)
	}
	return 0
}

// shortAge is a duration as the list prints it: 3m, 2h5m, 1d4h.
func shortAge(d time.Duration) string {
	switch m := int(d.Minutes()); {
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 24*60:
		return fmt.Sprintf("%dh%dm", m/60, m%60)
	default:
		return fmt.Sprintf("%dd%dh", m/(24*60), m%(24*60)/60)
	}
}
