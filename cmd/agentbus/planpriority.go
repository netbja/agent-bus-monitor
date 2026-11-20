package main

import (
	"cmp"
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
	if err := refuseAgentForPriority(ctx, requestDeps, "a plan task's priority", "on the task in the Plan tab"); err != nil {
		return err
	}
	facts, err := b.PlanFacts(ctx)
	if err != nil {
		return err
	}
	board, filePrio := planTaskOf(ctx, b, task)
	was := bus.PriorityLevel(filePrio) // the level in force: the person's, else the file's
	if p := facts[task].Priority; p != "" {
		was = bus.PriorityLevel(p)
	}
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
	entries, err := b.Board(ctx)
	if err != nil {
		return nil // the priority is recorded; the hint is best effort
	}
	if e, ok := entries[board]; ok && e.Request != nil && e.State != "done" && bus.PriorityLevel(e.Request.Priority) != level {
		_, err = fmt.Fprintf(out, "its request %s stays %s: agentbus request priority %s %s --as %s\n", board, bus.PriorityLevel(e.Request.Priority), board, level, as)
	}
	return err
}

// planTaskOf is the board task a plan task's requests use (its board field,
// else its id: bus.RequestBy's match, read the other way) and the plan
// file's priority for it.
func planTaskOf(ctx context.Context, b *bus.Bus, task string) (board, priority string) {
	rec, ok, err := b.Plan(ctx)
	if err != nil || !ok {
		return task, ""
	}
	f, err := plan.Parse([]byte(rec.File))
	if err != nil {
		return task, ""
	}
	for _, t := range f.Tasks {
		if t.ID == task {
			return cmp.Or(t.Board, task), t.Priority
		}
	}
	return task, ""
}
