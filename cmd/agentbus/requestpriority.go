package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/netbja/agent-bus-monitor/bus"
)

// request priority and request next (spec
// docs/superpowers/specs/2026-11-04-request-priority-design.md §4-§5).

// requestCallerDeps are what the person-only check reads (tests replace them).
type requestCallerDeps struct {
	Getenv      func(string) string
	AgentCaller func(ctx context.Context) (string, error) // the registered agent this process descends from
}

var requestDeps = requestCallerDeps{Getenv: os.Getenv, AgentCaller: agentAncestor}

// errNothingReady is request next with no request in hand or ready: exit 64.
var errNothingReady = errors.New("nothing ready")

// refuseAgentForPriority: a priority is the person's act, as suspend is: an
// agent's environment or an agent among the ancestors is refused, and so is
// a check that cannot be made.
func refuseAgentForPriority(ctx context.Context, d requestCallerDeps) error {
	if self := d.Getenv("AGENT_BUS_AGENT"); self != "" {
		return fmt.Errorf("a request's priority is the person's act, not %s's (run it from your own shell, or press + or - in busmon)", self)
	}
	if d.AgentCaller == nil {
		return nil
	}
	switch caller, err := d.AgentCaller(ctx); {
	case errors.Is(err, errNoAncestry):
	case err != nil:
		return fmt.Errorf("cannot check that no registered agent runs this (%v)", err)
	case caller != "":
		return fmt.Errorf("a request's priority is the person's act, and this runs under the agent %s", caller)
	}
	return nil
}

// requestPriority: request priority <task> high|normal|low --as <person>.
func requestPriority(ctx context.Context, b *bus.Bus, args []string, out io.Writer) error {
	args, as := extractFlag(args, "--as")
	if len(args) != 2 || as == "" {
		return fmt.Errorf("usage: request priority <task> high|normal|low --as <person>")
	}
	task, level := args[0], args[1]
	if !bus.ValidPriority(level) {
		return fmt.Errorf("request priority: %q is not high, normal or low", level)
	}
	if !bus.ValidName(as) {
		return fmt.Errorf("request priority: --as %q is not a valid name", as)
	}
	if err := refuseAgentForPriority(ctx, requestDeps); err != nil {
		return err
	}
	board, err := b.Board(ctx)
	if err != nil {
		return err
	}
	was := bus.PriorityNormal
	if e, ok := board[task]; ok && e.Request != nil {
		was = bus.PriorityLevel(e.Request.Priority)
	}
	changed, err := b.SetRequestPriority(ctx, task, level, as)
	if err != nil {
		return err
	}
	if !changed {
		_, err = fmt.Fprintf(out, "%s: already %s\n", task, level)
		return err
	}
	_, err = fmt.Fprintf(out, "%s: %s (was %s)\n", task, level, was)
	return err
}

// requestNext: request next [<agent>] [--json], the agent's next request by
// bus.NextRequest; errNothingReady when there is none.
func requestNext(ctx context.Context, b *bus.Bus, self string, args []string, asJSON bool, out io.Writer) error {
	agent := self
	if len(args) > 1 {
		return fmt.Errorf("usage: request next [<agent>] [--json]")
	}
	if len(args) == 1 {
		agent = args[0]
	}
	if !bus.ValidName(agent) {
		return fmt.Errorf("request next: invalid agent %q", agent)
	}
	board, err := b.Board(ctx)
	if err != nil {
		return err
	}
	task, why := bus.NextRequest(board, agent)
	if task == "" {
		return errNothingReady
	}
	r := board[task].Request
	level := bus.PriorityLevel(r.Priority)
	if asJSON {
		return json.NewEncoder(out).Encode(struct {
			Agent    string `json:"agent"`
			Task     string `json:"task"`
			Priority string `json:"priority"`
			Request  string `json:"request"`
			Why      string `json:"why,omitempty"`
		}{agent, task, level, r.ID, why})
	}
	line := task + "  " + level + "  " + r.ID
	if why != "" {
		line += "  (" + why + ")"
	}
	_, err = fmt.Fprintln(out, line)
	return err
}
