package main

import (
	"context"
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// plan priority <task> high|normal|low --as <person> (views-redesign §4, step
// 4b): a plan task's level, kept with the plan's facts on the broker; the
// person's act, refused to an agent as request priority is. A request sent
// for the task later starts at this level (bus.RequestBy); one already sent
// keeps its own, which request priority changes.
func planPriority(ctx context.Context, b *bus.Bus, args []string, out io.Writer) error {
	args, as := extractFlag(args, "--as")
	if len(args) != 2 || as == "" {
		return fmt.Errorf("usage: plan priority <task> high|normal|low --as <person>")
	}
	task, level := args[0], args[1]
	if !bus.ValidPriority(level) {
		return fmt.Errorf("%q is not high, normal or low", level)
	}
	if !bus.ValidName(as) {
		return fmt.Errorf("--as %q is not a valid name", as)
	}
	if err := refuseAgentForPriority(ctx, requestDeps, "a plan task's priority"); err != nil {
		return err
	}
	facts, err := b.PlanFacts(ctx)
	if err != nil {
		return err
	}
	was := bus.PriorityLevel(facts[task].Priority)
	changed, err := b.SetPlanPriority(ctx, task, level, as)
	if err != nil {
		return err
	}
	if !changed {
		_, err = fmt.Fprintf(out, "%s: already %s\n", task, level)
		return err
	}
	if _, err := fmt.Fprintf(out, "%s: %s (was %s)\n", task, level, was); err != nil {
		return err
	}
	// A request already sent for the task keeps its own level: say how to move it.
	board := planBoardTask(ctx, b, task)
	entries, err := b.Board(ctx)
	if err != nil {
		return nil // the priority is recorded; the hint is best effort
	}
	if e, ok := entries[board]; ok && e.Request != nil && e.State != "done" && bus.PriorityLevel(e.Request.Priority) != level {
		_, err = fmt.Fprintf(out, "its request %s stays %s: agentbus request priority %s %s --as %s\n", board, bus.PriorityLevel(e.Request.Priority), board, level, as)
	}
	return err
}

// planBoardTask is the board task a plan task's requests use: its board
// field, else its id (bus.RequestBy's match, read the other way).
func planBoardTask(ctx context.Context, b *bus.Bus, task string) string {
	rec, ok, err := b.Plan(ctx)
	if err != nil || !ok {
		return task
	}
	f, err := plan.Parse([]byte(rec.File))
	if err != nil {
		return task
	}
	for _, t := range f.Tasks {
		if t.ID == task && t.Board != "" {
			return t.Board
		}
	}
	return task
}
