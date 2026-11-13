package main

import (
	"context"
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// selfRepairCmdDeps are what `agentbus self-repair` reads and writes.
type selfRepairCmdDeps struct {
	Self      string                   // AGENT_BUS_AGENT ("anonymous" when unset)
	Ancestors func() []dispatch.ProcID // the caller's process ancestry, nearest first; nil: none read
	Agents    func(context.Context) (map[string]bus.AgentSnapshot, error)
	Reset     func(ctx context.Context, role string) (wasLoop bool, err error)
}

const selfRepairUsage = "usage: agentbus self-repair reset <agent> [--as <person>]"

// selfRepairResetter is the master role, the one agent that may reset (4.5).
const selfRepairResetter = "master"

// runSelfRepair is `agentbus self-repair reset <agent>` (self-repair spec
// 4.5): the person or master clears the agent's crash loop and restarts its
// budget, failed proofs included; the watcher may relaunch it again.
//
// Who runs it (reviewer-codex, 7a): the caller is the agent a process of its
// ancestry is (a registered agent's verified pid and start), else the agent
// its environment names, else nobody, and then --as names the person. Only
// master and a person may reset. The CLI cannot prove a person, as with
// reload's --as; it proves only that no other agent runs it.
func runSelfRepair(ctx context.Context, d selfRepairCmdDeps, args []string, out, errw io.Writer) int {
	args, person := extractFlag(args, "--as")
	if len(args) != 2 || args[0] != "reset" || !bus.ValidName(args[1]) || person != "" && !bus.ValidName(person) {
		fmt.Fprintln(errw, selfRepairUsage)
		return 2
	}
	role := args[1]
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errw, "self-repair reset: "+format+"\n", a...)
		return 1
	}
	snaps, err := d.Agents(ctx)
	if err != nil {
		return fail("%v", err)
	}
	caller := ""
	if d.Ancestors != nil {
		for _, p := range d.Ancestors() {
			for name, s := range snaps {
				if s.PID > 0 && s.PIDStart != 0 && s.PID == p.PID && s.PIDStart == p.Start {
					caller = name
				}
			}
			if caller != "" {
				break
			}
		}
	}
	if caller == "" && d.Self != "" && d.Self != "anonymous" {
		caller = d.Self
	}
	switch {
	case caller == selfRepairResetter:
	case caller != "":
		return fail("%s may not reset %s: only master or a person (--as <person>) clears a self-repair budget", caller, role)
	case person == "":
		return fail("name yourself: --as <person> (or run it as master)")
	}
	if _, ok := snaps[role]; !ok {
		return fail("no agent %s in this project", role)
	}
	was, err := d.Reset(ctx, role)
	if err != nil {
		return fail("%v", err)
	}
	what := "no crash loop"
	if was {
		what = "crash loop cleared"
	}
	fmt.Fprintf(out, "%s: %s; its self-repair budget counts from now\n", role, what)
	return 0
}
