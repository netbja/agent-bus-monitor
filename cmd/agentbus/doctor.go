package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/modelcheck"
)

// `agentbus roles doctor [--json]` (v0.15.1 §7, §11.6–7): for every registered
// agent, the model roles.toml configures for the profile it runs, the model
// it was launched with, and the model its transcript shows it uses. It reads
// Redis, the manifest, /proc and transcripts; it never calls a model.

// The rows are modelcheck.DoctorRows; this prints and publishes them.
func runDoctor(ctx context.Context, b *bus.Bus, d modelcheck.DoctorDeps, args []string, out, errw io.Writer) int {
	args, asJSON := extractBool(args, "--json")
	args, publish := extractBool(args, "--publish")
	args, agent := extractFlag(args, "--agent")
	if len(args) != 0 {
		fmt.Fprintln(errw, "usage: agentbus --project <p> roles doctor [--agent <name>] [--json] [--publish]")
		return 2
	}
	rows, err := modelcheck.DoctorRows(ctx, b, d)
	if err != nil {
		fmt.Fprintf(errw, "roles doctor: %v\n", err)
		return 1
	}
	if publish { // for busmon, which reads Redis only (the sentinel's daily run)
		verdicts := map[string]bus.ModelVerdict{}
		for _, r := range rows {
			verdicts[r.Agent] = bus.ModelVerdict{PID: r.PID, Configured: r.Configured, Launched: r.Launched, Used: r.Used,
				Verdict: r.Verdict, Why: r.Why}
		}
		if err := b.PutModelVerdicts(ctx, verdicts); err != nil {
			fmt.Fprintf(errw, "roles doctor: publishing: %v\n", err)
			return 1
		}
	}
	if agent != "" { // model-detection §3.8: what one agent runs (master's USED);
		// after --publish, which always publishes every agent's verdict
		var mine []modelcheck.DoctorRow
		for _, r := range rows {
			if r.Agent == agent {
				mine = append(mine, r)
			}
		}
		if len(mine) == 0 {
			fmt.Fprintf(errw, "roles doctor: %s is not a registered agent with a role in roles.toml\n", agent)
			return 1
		}
		rows = mine
	}
	code := 0
	for _, r := range rows {
		if modelcheck.DoctorFails(r.Verdict) {
			code = 3
		}
	}
	if asJSON {
		if writeJSON(out, errw, rows) != 0 {
			return 1
		}
		return code
	}
	fmt.Fprintf(out, "%-12s %-3s %-10s %-22s %-22s %-22s %s\n", "AGENT", "REQ", "PROFILE", "CONFIGURED", "LAUNCHED", "USED", "VERDICT")
	for _, r := range rows {
		req := "-"
		if r.Required {
			req = "yes"
		}
		fmt.Fprintf(out, "%-12s %-3s %-10s %-22s %-22s %-22s %s\n", r.Agent, req, dash(r.Profile), dash(r.Configured), dash(r.Launched), dash(r.Used), r.Verdict)
		if r.Why != "" && r.Verdict != "ok" {
			fmt.Fprintf(out, "%12s %s\n", "", r.Why)
		}
		if r.Risk != "" {
			fmt.Fprintf(out, "%12s risk: %s\n", "", r.Risk)
		}
		if p := r.Pops; p != nil {
			fmt.Fprintf(out, "%12s pops: %s\n", "", popsLine(p))
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no registered agent has a role in roles.toml")
	}
	return code
}

// popsLine is a popping role's spawn rules in words.
func popsLine(p *modelcheck.DoctorPops) string {
	var parts []string
	if len(p.Allow) > 0 {
		parts = append(parts, "allow "+strings.Join(p.Allow, ", "))
	}
	if len(p.Ask) > 0 {
		parts = append(parts, "ask "+strings.Join(p.Ask, ", ")+" (the person approves)")
	}
	if len(parts) == 0 {
		parts = append(parts, "none") // it pops, but roles.toml has no other role to pop
	}
	return strings.Join(parts, "; ") + "; any other role is not covered"
}
