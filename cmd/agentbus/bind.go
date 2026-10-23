package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// bindDeps is what `agentbus bind` touches, injected for tests.
type bindDeps struct {
	Bind  func(ctx context.Context, agent string, who bus.AgentIdent) error
	Procs func(ctx context.Context, pane string) ([]dispatch.Proc, string, error)
	Start func(pid int) (uint64, error)
	Env   func(string) string
}

// runBind records a verified identity for an agent whose own commands cannot
// prove it (v0.10.1): the pid must be in the pane's foreground set, and its
// start time is read now. With no --pid it only lists the candidates, so an
// operator never binds a wrapper or shell by accident.
func runBind(ctx context.Context, d bindDeps, args []string, out, errw io.Writer) int {
	args, pane := extractFlag(args, "--pane")
	args, pidArg := extractFlag(args, "--pid")
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(errw, "usage: bind <agent> [--pane <pane>] [--pid <pid>]")
		return 1
	}
	agent := args[0]
	if pane == "" && d.Env("HERDR_ENV") == "1" {
		pane = d.Env("HERDR_PANE_ID")
	}
	if pane == "" {
		fmt.Fprintln(errw, "bind: no pane (pass --pane)")
		return 1
	}
	procs, terminal, err := d.Procs(ctx, pane)
	if err != nil {
		fmt.Fprintf(errw, "bind: pane %s: %v\n", pane, err)
		return 1
	}
	if pidArg == "" {
		fmt.Fprintf(errw, "bind: pick the client's pid in pane %s and pass --pid:\n", pane)
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "PID\tPPID\tCOMMAND")
		for _, p := range procs {
			cmd := p.Cmdline
			if cmd == "" {
				cmd = p.Name
			}
			fmt.Fprintf(tw, "%d\t%d\t%s\n", p.PID, p.PPID, cmd)
		}
		tw.Flush()
		return 2
	}
	pid, err := strconv.Atoi(pidArg)
	if err != nil || pid <= 0 {
		fmt.Fprintf(errw, "bind: invalid pid %q\n", pidArg)
		return 1
	}
	found := false
	for _, p := range procs {
		found = found || p.PID == pid
	}
	if !found {
		fmt.Fprintf(errw, "bind: pid %d is not in the foreground of pane %s\n", pid, pane)
		return 1
	}
	start, err := d.Start(pid)
	if err != nil || start == 0 {
		fmt.Fprintf(errw, "bind: cannot read the start time of pid %d: %v\n", pid, err)
		return 1
	}
	if err := d.Bind(ctx, agent, bus.AgentIdent{Pane: pane, PID: pid, PIDStart: start, Terminal: terminal,
		Client: d.Env("AGENT_BUS_CLIENT"), Provider: bus.ProviderFor(d.Env("AGENT_BUS_CLIENT"), d.Env("AGENT_BUS_PROVIDER")),
		Model: d.Env("AGENT_BUS_MODEL"), ClientFP: d.Env("AGENT_BUS_CLIENT_FP")}); err != nil {
		fmt.Fprintf(errw, "bind: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "bound %s to %s pid %d\n", agent, pane, pid)
	return 0
}
