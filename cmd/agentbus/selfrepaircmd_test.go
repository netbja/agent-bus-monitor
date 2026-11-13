package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// agentbus self-repair reset <agent> (self-repair spec 4.5, step 7): the
// person or master clears a crash loop and restarts the agent's budget. A
// registered agent only; a typo writes nothing.
func TestSelfRepairResetCommand(t *testing.T) {
	reset := ""
	loop := true
	d := selfRepairCmdDeps{
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) {
			return map[string]bus.AgentSnapshot{"coder": {}}, nil
		},
		Reset: func(_ context.Context, role string) (bool, error) { reset = role; return loop, nil },
	}
	run := func(args ...string) (int, string, string) {
		var out, errw bytes.Buffer
		code := runSelfRepair(context.Background(), d, args, &out, &errw)
		return code, out.String(), errw.String()
	}
	if code, out, _ := run("reset", "coder"); code != 0 || reset != "coder" || !strings.Contains(out, "coder: crash loop cleared") ||
		!strings.Contains(out, "budget counts from now") {
		t.Fatalf("reset: %d %q (%s)", code, out, reset)
	}
	loop, reset = false, ""
	if code, out, _ := run("reset", "coder"); code != 0 || reset != "coder" || !strings.Contains(out, "coder: no crash loop") {
		t.Fatalf("no loop: %d %q", code, out)
	}
	reset = ""
	for _, args := range [][]string{{}, {"reset"}, {"reset", "a", "b"}, {"clear", "coder"}, {"reset", "Bad Name"}} {
		if code, _, errw := run(args...); code != 2 || reset != "" || errw == "" {
			t.Fatalf("%v: %d %q, reset %q", args, code, errw, reset)
		}
	}
	if code, _, errw := run("reset", "codr"); code != 1 || reset != "" || !strings.Contains(errw, "no agent codr") {
		t.Fatalf("unknown agent: %d %q", code, errw)
	}
	d.Agents = func(context.Context) (map[string]bus.AgentSnapshot, error) { return nil, errors.New("down") }
	if code, _, errw := run("reset", "coder"); code != 1 || reset != "" || !strings.Contains(errw, "down") {
		t.Fatalf("bus down: %d %q", code, errw)
	}
}
