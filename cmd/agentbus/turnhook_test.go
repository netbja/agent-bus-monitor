package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type flipCall struct{ project, agent, state string }

func runTurnHook(t *testing.T, args []string, stdin string, env []string) ([]flipCall, string) {
	t.Helper()
	var calls []flipCall
	var out bytes.Buffer
	code := turnHook(args, env, strings.NewReader(stdin), &out, func(_ context.Context, project, agent, state string) error {
		calls = append(calls, flipCall{project, agent, state})
		return nil
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	return calls, out.String()
}

func TestTurnHookFlipsOnTurnStartAndEnd(t *testing.T) {
	env := []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo"}
	calls, out := runTurnHook(t, []string{"start"}, `{"hook_event_name":"UserPromptSubmit","prompt":"[agentbus#1] demo: 1 message(s) waiting"}`, env)
	if len(calls) != 1 || calls[0] != (flipCall{"demo", "coder", "working"}) || out != "" {
		t.Fatalf("start: %+v %q", calls, out)
	}
	calls, out = runTurnHook(t, []string{"end"}, `{"hook_event_name":"Stop"}`, env)
	if len(calls) != 1 || calls[0].state != "idle" || out != "" {
		t.Fatalf("end: %+v %q", calls, out)
	}
	calls, _ = runTurnHook(t, []string{"end"}, `{"hook_event_name":"SessionStart","session_id":"s"}`, env)
	if len(calls) != 1 || calls[0].state != "idle" {
		t.Fatalf("a new session ends a stuck turn: %+v", calls)
	}
}

// What never flips: a / prompt (foureyes: the typed /clear must neither void
// the hand-off nor leave a working with no Stop), a subagent, bad input, no
// identity. Nothing is printed and the exit is always 0.
func TestTurnHookIgnores(t *testing.T) {
	env := []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo"}
	for _, c := range []struct {
		args  []string
		stdin string
		env   []string
	}{
		{[]string{"start"}, `{"hook_event_name":"UserPromptSubmit","prompt":"/clear"}`, env},
		{[]string{"start"}, `{"hook_event_name":"UserPromptSubmit","prompt":"  /compact keep the plan"}`, env},
		{[]string{"start"}, `{"hook_event_name":"UserPromptSubmit","prompt":"go","agent_id":"sub-1"}`, env},
		{[]string{"start"}, `not json`, env},
		{[]string{"sideways"}, `{"hook_event_name":"Stop"}`, env},
		{nil, `{"hook_event_name":"Stop"}`, env},
		{[]string{"end"}, `{"hook_event_name":"Stop"}`, []string{"AGENT_BUS_PROJECT=demo"}},
		{[]string{"end"}, `{"hook_event_name":"Stop"}`, []string{"AGENT_BUS_AGENT=Bad Name", "AGENT_BUS_PROJECT=demo"}},
	} {
		calls, out := runTurnHook(t, c.args, c.stdin, c.env)
		if len(calls) != 0 || out != "" {
			t.Errorf("%v %s: flipped %+v, printed %q", c.args, c.stdin, calls, out)
		}
	}
}
