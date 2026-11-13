package main

import (
	"context"
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
)

// selfRepairCmdDeps are what `agentbus self-repair` reads and writes.
type selfRepairCmdDeps struct {
	Agents func(context.Context) (map[string]bus.AgentSnapshot, error)
	Reset  func(ctx context.Context, role string) (wasLoop bool, err error)
}

const selfRepairUsage = "usage: agentbus self-repair reset <agent>"

// runSelfRepair is `agentbus self-repair reset <agent>` (self-repair spec
// 4.5): the person or master clears the agent's crash loop and restarts its
// budget, failed proofs included; the watcher may relaunch it again.
func runSelfRepair(ctx context.Context, d selfRepairCmdDeps, args []string, out, errw io.Writer) int {
	if len(args) != 2 || args[0] != "reset" || !bus.ValidName(args[1]) {
		fmt.Fprintln(errw, selfRepairUsage)
		return 2
	}
	role := args[1]
	snaps, err := d.Agents(ctx)
	if err != nil {
		fmt.Fprintf(errw, "self-repair reset: %v\n", err)
		return 1
	}
	if _, ok := snaps[role]; !ok {
		fmt.Fprintf(errw, "self-repair reset: no agent %s in this project\n", role)
		return 1
	}
	was, err := d.Reset(ctx, role)
	if err != nil {
		fmt.Fprintf(errw, "self-repair reset: %v\n", err)
		return 1
	}
	what := "no crash loop"
	if was {
		what = "crash loop cleared"
	}
	fmt.Fprintf(out, "%s: %s; its self-repair budget counts from now\n", role, what)
	return 0
}
