package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// settlingHerdr is a herdr seen right after reload's SIGTERM: for the first
// `lag` reads of the workspace's panes the coder tab still shows the exiting
// client (herdr's agent detection and the pane's foreground process lag the
// exit), then a plain shell, as `up --resume` sees it seconds later.
type settlingHerdr struct {
	relaunchHerdr
	lag     int
	relapse int // a later read that shows the client again (0: none)
	noInfo  int // the first reads whose pane process-info fails (unknown)
	reads   int
}

func (f *settlingHerdr) run(args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	switch {
	case line == "workspace list":
		f.calls = append(f.calls, line)
		return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
	case strings.HasPrefix(line, "pane process-info ") && f.reads <= f.noInfo:
		f.calls = append(f.calls, line)
		return nil, errors.New("pane is exiting")
	case strings.HasPrefix(line, "pane list --workspace w4"):
		f.reads++
		if f.reads <= f.lag || f.reads == f.relapse {
			f.panes = `{"pane_id":"w4:p2","tab_id":"w4:t2","agent":"claude"}`
			f.fg = map[string]string{"w4:p2": "claude --resume x"}
		} else {
			f.panes = `{"pane_id":"w4:p2","tab_id":"w4:t2"}`
			f.fg = map[string]string{"w4:p2": "-bash"}
		}
	}
	return f.relaunchHerdr.run(args...)
}

func settleDeps(t *testing.T, h *settlingHerdr, snaps func() map[string]bus.AgentSnapshot) upDeps {
	t.Helper()
	dir := t.TempDir()
	tpl := "name = \"demo\"\nworking_dir = \"" + dir + "\"\n\n[[tabs]]\nname = \"coder\"\ncommand = \"/r/scripts/agent-launch coder demo\"\n"
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HERDR_ENV": "1", "AGENTBUS_PROJECTS_DIR": dir}
	return upDeps{
		Herdr:     h.run,
		Env:       func(k string) string { return env[k] },
		Agents:    func(string) (map[string]bus.AgentSnapshot, error) { return snaps(), nil },
		ProcStart: func(pid int) (uint64, error) { return map[int]uint64{5151: 9}[pid], ifDead(pid) },
		Sleep:     func(time.Duration) {},
	}
}

func ifDead(pid int) error {
	if pid == 5151 {
		return nil
	}
	return os.ErrNotExist
}

// The order of events reload meets (bug of 2026-10-28, v0.23.0): the old pid
// is gone, but the tab still reads busy for a moment. The relaunch waits for
// the tab to be what up needs (no agent, every pane a plain shell), then
// relaunches in a new pane; it never types into the old one.
func TestRelaunchRoleWaitsForTheTabToSettle(t *testing.T) {
	h := &settlingHerdr{relaunchHerdr: relaunchHerdr{tabs: `{"label":"coder","tab_id":"w4:t2"}`}, lag: 2}
	old := bus.AgentSnapshot{PID: 4242, PIDStart: 1, Pane: "w4:p2", Client: "claude-code"}
	snaps := func() map[string]bus.AgentSnapshot {
		if strings.Contains(strings.Join(h.calls, "\n"), "pane run w4:p2s") {
			return map[string]bus.AgentSnapshot{"coder": {PID: 5151, PIDStart: 9, Pane: "w4:p2s"}}
		}
		return map[string]bus.AgentSnapshot{"coder": old}
	}
	var out bytes.Buffer
	if err := relaunchRole(settleDeps(t, h, snaps), "demo", "coder", "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", &out); err != nil {
		t.Fatalf("relaunchRole: %v\n%s", err, out.String())
	}
	if got := strings.Join(h.ran(), "\n"); !strings.Contains(got, "pane split w4:p2 --direction right") ||
		!strings.Contains(got, "pane run w4:p2s /r/scripts/agent-launch coder demo --resume 93ff25bd-34d8-4077-bf6c-65e6cb135bbf") {
		t.Fatalf("no relaunch in a new pane:\n%s\nout: %s", got, out.String())
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
	if strings.Contains(out.String(), "skipped (busy)") {
		t.Fatalf("relaunch judged the tab before it settled:\n%s", out.String())
	}
}

// A tab that never settles is not relaunched and not waited on for a minute
// of proof: relaunchRole says why at once, and the busy rule is unchanged.
func TestRelaunchRoleFailsWhenTheTabStaysBusy(t *testing.T) {
	h := &settlingHerdr{relaunchHerdr: relaunchHerdr{tabs: `{"label":"coder","tab_id":"w4:t2"}`}, lag: 1 << 30}
	snaps := func() map[string]bus.AgentSnapshot {
		return map[string]bus.AgentSnapshot{"coder": {PID: 4242, PIDStart: 1, Pane: "w4:p2"}}
	}
	var out bytes.Buffer
	err := relaunchRole(settleDeps(t, h, snaps), "demo", "coder", "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", &out)
	if err == nil || !strings.Contains(err.Error(), "skipped (busy)") {
		t.Fatalf("err = %v, want the busy verdict named", err)
	}
	if got := h.ran(); len(got) != 0 {
		t.Fatalf("a busy tab was relaunched: %q", got)
	}
}

// The tab settles, then relaunchOpen's own read finds it busy again: the
// relaunch is skipped there, and relaunchRole fails at once instead of
// returning as if it had relaunched (reload then waited a minute for proof).
func TestRelaunchRoleFailsWhenTheRelaunchSkipsTheTab(t *testing.T) {
	h := &settlingHerdr{relaunchHerdr: relaunchHerdr{tabs: `{"label":"coder","tab_id":"w4:t2"}`}, lag: 0, relapse: 2}
	snaps := func() map[string]bus.AgentSnapshot {
		return map[string]bus.AgentSnapshot{"coder": {PID: 4242, PIDStart: 1, Pane: "w4:p2"}}
	}
	var out bytes.Buffer
	err := relaunchRole(settleDeps(t, h, snaps), "demo", "coder", "", &out)
	if err == nil || !strings.Contains(err.Error(), "skipped (busy)") {
		t.Fatalf("err = %v, want the skipped tab named\n%s", err, out.String())
	}
	if got := h.ran(); len(got) != 0 {
		t.Fatalf("a busy tab was relaunched: %q", got)
	}
}

// Right after the exit herdr may not answer for the pane (its process-info
// fails): unknown, which is waited on like busy, never relaunched as is.
func TestRelaunchRoleWaitsThroughAnUnknownTab(t *testing.T) {
	h := &settlingHerdr{relaunchHerdr: relaunchHerdr{tabs: `{"label":"coder","tab_id":"w4:t2"}`}, noInfo: 2}
	snaps := func() map[string]bus.AgentSnapshot {
		return map[string]bus.AgentSnapshot{"coder": {PID: 4242, PIDStart: 1, Pane: "w4:p2"}}
	}
	var out bytes.Buffer
	if err := relaunchRole(settleDeps(t, h, snaps), "demo", "coder", "", &out); err != nil {
		t.Fatalf("relaunchRole: %v\n%s", err, out.String())
	}
	if got := strings.Join(h.ran(), "\n"); !strings.Contains(got, "pane run w4:p2s /r/scripts/agent-launch coder demo") {
		t.Fatalf("no relaunch after the tab became readable:\n%s", got)
	}
}
