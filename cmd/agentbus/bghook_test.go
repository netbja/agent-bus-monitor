package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// background-work-visible step 1: the turn hook also reports the agent's
// background tasks, from Claude Code's own Stop and SubagentStop payloads
// (recorded: testdata/hooks), and forgets them at SessionStart. Nothing the
// agent typed is reported.

// The test binary never dials a broker from the hook: every turn-hook test
// that does not set turnBg reports nowhere.
func init() { turnBg = func(context.Context, string, string, bgReport) error { return nil } }

type bgHookCall struct {
	project, agent string
	r              bgReport
}

func runBgHook(t *testing.T, args []string, fixture string) ([]flipCall, []bgHookCall) {
	t.Helper()
	raw, err := os.ReadFile("testdata/hooks/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	var got []bgHookCall
	old := turnBg
	turnBg = func(_ context.Context, project, agent string, r bgReport) error {
		got = append(got, bgHookCall{project, agent, r})
		return nil
	}
	t.Cleanup(func() { turnBg = old })
	calls, _ := runTurnHook(t, args, string(raw), []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo", "AGENT_BUS_CLIENT=claude"})
	return calls, got
}

func TestBgHookStop(t *testing.T) {
	flips, got := runBgHook(t, []string{"end"}, "claude-stop-3-tasks.json")
	if len(flips) != 1 || flips[0].state != "idle" {
		t.Fatalf("the turn end's flip lost: %v", flips)
	}
	if len(got) != 1 || got[0].agent != "coder" || got[0].r.event != "Stop" || got[0].r.drop || len(got[0].r.tasks) != 3 {
		t.Fatalf("reported %+v", got)
	}
	tk := got[0].r.tasks
	if tk[0].Type != "shell" || tk[0].Status != "running" || tk[2].Type != "subagent" || tk[2].AgentType != "Explore" || tk[0].AgentType != "" {
		t.Fatalf("tasks %+v", tk)
	}
	if tk[0].IDHash != bus.BgIDHash("bfxqkrpvo") {
		t.Fatalf("id hash %q", tk[0].IDHash)
	}
	// Nothing the agent typed: no command, description or raw id anywhere.
	all := strings.ToLower(strings.Join([]string{got[0].r.String()}, ""))
	for _, w := range []string{"sleep", "bfxqkrpvo", "list current", "seconds"} {
		if strings.Contains(all, w) {
			t.Fatalf("%q reported: %s", w, all)
		}
	}
}

func TestBgHookSubagentStopRefreshesTheParent(t *testing.T) {
	flips, got := runBgHook(t, []string{"end"}, "claude-subagentstop.json")
	if len(flips) != 0 {
		t.Fatalf("a sub-agent's stop flipped the agent: %v", flips)
	}
	if len(got) != 1 || got[0].agent != "coder" || got[0].r.event != "SubagentStop" || len(got[0].r.tasks) != 3 {
		t.Fatalf("reported %+v", got)
	}
}

func TestBgHookCronsAndLabels(t *testing.T) {
	_, got := runBgHook(t, []string{"end"}, "claude-stop-cron.json")
	if len(got) != 1 || len(got[0].r.crons) != 1 || got[0].r.crons[0].Schedule != "*/30 * * * *" || !got[0].r.crons[0].Recurring {
		t.Fatalf("crons %+v", got)
	}
	if strings.Contains(got[0].r.String(), "say hi") {
		t.Fatal("a cron's prompt reported")
	}
	// Unknown labels and names fall back; a schedule not of cron shape is dropped.
	r := bgReportOf("Stop", []hookTask{{ID: "x", Type: "brand new", Status: "paused", AgentType: "my agent!"}, {ID: "y", Type: "subagent", Status: "running", AgentType: "my agent!"}},
		[]hookCron{{ID: "c", Schedule: "rm -rf /"}})
	if r.tasks[0].Type != "other" || r.tasks[0].Status != "other" || r.tasks[0].AgentType != "" || r.tasks[1].AgentType != "" || len(r.crons) != 0 {
		t.Fatalf("sanitised %+v", r)
	}
}

func TestBgHookSessionStartDrops(t *testing.T) {
	_, got := runBgHook(t, []string{"end"}, "claude-subagentstart.json") // SubagentStart: nothing reported
	if len(got) != 0 {
		t.Fatalf("SubagentStart reported %+v", got)
	}
	var calls []bgHookCall
	old := turnBg
	turnBg = func(_ context.Context, project, agent string, r bgReport) error {
		calls = append(calls, bgHookCall{project, agent, r})
		return nil
	}
	defer func() { turnBg = old }()
	runTurnHook(t, []string{"end"}, `{"hook_event_name":"SessionStart","session_id":"s"}`, []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo"})
	if len(calls) != 1 || !calls[0].r.drop {
		t.Fatalf("SessionStart: %+v", calls)
	}
	// A Stop with no list (Claude's registry unreachable): nothing reported, the flip kept.
	calls = nil
	if flips, _ := runTurnHook(t, []string{"end"}, `{"hook_event_name":"Stop"}`, []string{"AGENT_BUS_AGENT=coder", "AGENT_BUS_PROJECT=demo"}); len(flips) != 1 || len(calls) != 0 {
		t.Fatalf("no list: flips %v reports %+v", flips, calls)
	}
}
