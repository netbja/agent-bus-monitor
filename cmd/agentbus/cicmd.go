package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus ci reset <pr> (ci-loop §5, spec
// docs/superpowers/specs/2026-11-19-ci-loop-design.md): the person's only,
// as run reset; it deletes the PR's field in {p}:ci, so the next
// merge-queue pass decides that PR afresh with ci.max_handoffs more
// hand-offs. Exit 0, 2 on a usage error or a refusal.
func runCI(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, out, errw io.Writer) int {
	pr := 0
	if len(args) == 2 && args[0] == "reset" {
		pr, _ = strconv.Atoi(args[1])
	}
	if pr <= 0 {
		fmt.Fprintln(errw, "usage: ci reset <pr>")
		return 2
	}
	// Defence in depth, not a boundary (§5): the person runs it outside the
	// agents' environment.
	if a := getenv("AGENT_BUS_AGENT"); a != "" {
		fmt.Fprintf(errw, "ci reset: only the person re-opens a PR's CI loop (this is %s's agent environment, AGENT_BUS_AGENT)\n", a)
		return 2
	}
	had, err := b.ResetCI(ctx, pr)
	if err != nil {
		fmt.Fprintln(errw, "ci reset:", err)
		return 2
	}
	if !had {
		fmt.Fprintf(out, "ci reset: PR #%d has no CI loop record: nothing to re-open\n", pr)
		return 0
	}
	fmt.Fprintf(out, "ci reset: PR #%d's CI loop re-opened; the next merge-queue pass decides it afresh\n", pr)
	return 0
}
