package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// agentbus self-repair reset <agent> (self-repair spec 4.5, step 7): the
// person or master clears a crash loop and restarts the agent's budget. A
// registered agent only; a typo writes nothing.
func TestSelfRepairResetCommand(t *testing.T) {
	reset := ""
	loop := true
	d := selfRepairCmdDeps{
		Self: "master",
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
	for _, args := range [][]string{{}, {"reset"}, {"reset", "a", "b"}, {"clear", "coder"}, {"reset", "Bad Name"}, {"reset", "coder", "--as", "Bad Name"}} {
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

// Who may reset (reviewer-codex, 7a): master, or a person naming themself
// (--as) from a process no registered agent is an ancestor of. Any other
// agent is refused, whether its environment names it or only its process
// ancestry does (an agent that unset AGENT_BUS_AGENT is still that agent),
// and an agent's --as is no person.
func TestSelfRepairResetCaller(t *testing.T) {
	snaps := map[string]bus.AgentSnapshot{
		"master": {PID: 100, PIDStart: 1}, "coder": {PID: 200, PIDStart: 2}, "kimi": {}, // kimi: no verified process
	}
	cases := []struct {
		name  string
		self  string
		chain []dispatch.ProcID
		args  []string
		ok    bool
		why   string
	}{
		{"master by its environment", "master", nil, nil, true, ""},
		{"master by its ancestry, environment unset", "anonymous", []dispatch.ProcID{{PID: 7, Start: 7}, {PID: 100, Start: 1}}, nil, true, ""},
		{"a person, --as, outside any agent", "anonymous", []dispatch.ProcID{{PID: 7, Start: 7}}, []string{"--as", "charlie"}, true, ""},
		{"another agent by its environment", "coder", nil, nil, false, "coder may not reset"},
		{"another agent with --as", "coder", nil, []string{"--as", "charlie"}, false, "coder may not reset"},
		{"another agent by its ancestry, environment unset", "anonymous", []dispatch.ProcID{{PID: 200, Start: 2}}, []string{"--as", "charlie"}, false, "coder may not reset"},
		{"ancestry wins over the environment", "master", []dispatch.ProcID{{PID: 200, Start: 2}}, nil, false, "coder may not reset"},
		{"a reused pid is not the agent", "anonymous", []dispatch.ProcID{{PID: 200, Start: 9}}, []string{"--as", "charlie"}, true, ""},
		{"nobody named", "anonymous", nil, nil, false, "--as <person>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reset := ""
			d := selfRepairCmdDeps{Self: c.self, Ancestors: func() []dispatch.ProcID { return c.chain },
				Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) { return snaps, nil },
				Reset:  func(_ context.Context, role string) (bool, error) { reset = role; return false, nil }}
			var out, errw bytes.Buffer
			code := runSelfRepair(context.Background(), d, append([]string{"reset", "kimi"}, c.args...), &out, &errw)
			if c.ok != (code == 0 && reset == "kimi") || !c.ok && (reset != "" || !strings.Contains(errw.String(), c.why)) {
				t.Fatalf("code %d reset %q out %q err %q", code, reset, out.String(), errw.String())
			}
		})
	}
}
