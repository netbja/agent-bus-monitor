package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
)

type flipCall struct{ project, agent, state string }

type proveCall struct{ project, agent, session string }

func runTurnHook(t *testing.T, args []string, stdin string, env []string) ([]flipCall, string) {
	t.Helper()
	calls, _, out := runTurnHookProving(t, args, stdin, env)
	return calls, out
}

func runTurnHookProving(t *testing.T, args []string, stdin string, env []string) ([]flipCall, []proveCall, string) {
	t.Helper()
	var calls []flipCall
	var proved []proveCall
	var out bytes.Buffer
	code := turnHook(args, env, strings.NewReader(stdin), &out, func(_ context.Context, project, agent, state string) error {
		calls = append(calls, flipCall{project, agent, state})
		return nil
	}, func(project, agent, session string) {
		proved = append(proved, proveCall{project, agent, session})
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	return calls, proved, out.String()
}

// codex-token-attribution step 1: a Codex launch's SessionStart (startup or
// resume, the same id) hands its session_id to the prover, as Claude Code's
// resume hook does; the turn still ends and nothing is printed. Any other
// event, client, a subagent, or an id the prover cannot take hands nothing.
func TestTurnHookHandsTheCodexSessionToTheProver(t *testing.T) {
	codex := []string{"AGENT_BUS_AGENT=reviewer-codex", "AGENT_BUS_PROJECT=demo", "AGENT_BUS_CLIENT=codex"}
	const id = "01a1a38b-a635-76d0-bbc6-348e3c47e603"
	for _, source := range []string{"startup", "resume"} {
		calls, proved, out := runTurnHookProving(t, []string{"end"}, `{"hook_event_name":"SessionStart","source":"`+source+`","session_id":"`+id+`"}`, codex)
		if len(proved) != 1 || proved[0] != (proveCall{"demo", "reviewer-codex", id}) || len(calls) != 1 || calls[0].state != "idle" || out != "" {
			t.Fatalf("%s: proved %+v, flipped %+v, printed %q", source, proved, calls, out)
		}
	}
	claude := []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo", "AGENT_BUS_CLIENT=claude-code"}
	for _, c := range []struct {
		args  []string
		stdin string
		env   []string
	}{
		{[]string{"end"}, `{"hook_event_name":"SessionStart","session_id":"` + id + `"}`, claude}, // Claude proves through its resume hook
		{[]string{"end"}, `{"hook_event_name":"Stop","session_id":"` + id + `"}`, codex},
		{[]string{"start"}, `{"hook_event_name":"SessionStart","session_id":"` + id + `"}`, codex},
		{[]string{"end"}, `{"hook_event_name":"SessionStart","session_id":"../x"}`, codex},
		{[]string{"end"}, `{"hook_event_name":"SessionStart"}`, codex},
		{[]string{"end"}, `{"hook_event_name":"SessionStart","session_id":"` + id + `","agent_id":"sub"}`, codex},
		{[]string{"end"}, `{"hook_event_name":"SessionStart","session_id":"` + id + `"}`, []string{"AGENT_BUS_AGENT=reviewer-codex", "AGENT_BUS_PROJECT=demo"}},
	} {
		if _, proved, _ := runTurnHookProving(t, c.args, c.stdin, c.env); len(proved) != 0 {
			t.Errorf("%v %s %v: proved %+v", c.args, c.stdin, c.env, proved)
		}
	}
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
		// Grok marks a subagent's events with subagentType (auto-state step 5).
		{[]string{"start"}, `{"hook_event_name":"UserPromptSubmit","prompt":"go","subagentType":"explore"}`, env},
		{[]string{"end"}, `{"hook_event_name":"Stop","reason":"end_turn","subagentType":"explore"}`, env},
		{[]string{"end"}, `{"hook_event_name":"StopFailure","error":"rate_limit","subagentType":"general"}`, env},
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

// proveFromHook starts the same prover as the resume hook: roles
// prove-session with the resume log, the project, the agent, the session,
// the event's time and the hook's ancestry; nothing without a chain, with an
// id the prover cannot take, or with a bad project.
func TestProveFromHookStartsTheProver(t *testing.T) {
	state := t.TempDir()
	env := []string{"HOME=" + t.TempDir(), "XDG_STATE_HOME=" + state}
	const id = "01a1a38b-a635-76d0-bbc6-348e3c47e603"
	at := time.Unix(0, 1234)
	fg := hookFacts{Ancestors: func() []dispatch.ProcID { return []dispatch.ProcID{{PID: 10, Start: 7}, {PID: 1, Start: 1}} }, Now: func() time.Time { return at }}
	var got [][]string
	spawn := func(argv []string) error { got = append(got, argv); return nil }
	proveFromHook(env, "demo", "reviewer-codex", id, spawn, fg)
	paths, _ := service.PathsFrom(func(k string) string { v, _ := lookupEnv(env, k); return v })
	want := []string{"roles", "prove-session", filepath.Join(paths.StateDir("demo"), "resume.log"), "demo", "reviewer-codex", id, "1234", "--", "10:7", "1:1"}
	if len(got) != 1 || strings.Join(got[0], " ") != strings.Join(want, " ") {
		t.Fatalf("spawned %q\nwant %q", got, want)
	}
	got = nil
	noChain := hookFacts{Ancestors: func() []dispatch.ProcID { return nil }, Now: time.Now}
	proveFromHook(env, "demo", "reviewer-codex", id, spawn, noChain)
	proveFromHook(env, "demo", "reviewer-codex", "../x", spawn, fg)
	proveFromHook(env, "Bad Project", "reviewer-codex", id, spawn, fg)
	proveFromHook(env, "demo", "Bad Agent", id, spawn, fg)
	if len(got) != 0 {
		t.Fatalf("spawned %q", got)
	}
}
