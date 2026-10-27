package main

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fixtureAlert is the threshold the fixture's dispatcher published, so the
// goldens never follow the shell's AGENT_BUS_CONTEXT_ALERT.
var fixtureAlert = 80.0

// shellNow is the fixed time of the shell's fixture and goldens.
var shellNow = time.Date(2026, 10, 26, 15, 4, 5, 0, time.UTC)

// shellFixture is the snapshot the goldens are drawn from (busmon Charm spec,
// step b tests): a provider on hold, a fallback agent, an agent waiting with
// an open ask and one without, a pending gate, a paused project, a dormant
// agent, and every surviving agent flag, two to a row.
func shellFixture(now time.Time) snapshot {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ms := func(d time.Duration) int64 { return ago(d).UnixMilli() }
	q := func(id, text string) bus.AskQuestionState {
		return bus.AskQuestionState{AskQuestion: bus.AskQuestion{ID: id, Header: id, Question: text,
			Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}}
	}
	return snapshot{
		person: "bernard", pilot: "master", dispatch: "up", missions: "up",
		health: health{ok: true, since: ago(time.Hour)},
		pause:  &bus.PauseRecord{Until: now.Add(112 * time.Minute).UnixMilli(), By: "bernard", Reason: "lunch", At: ms(8 * time.Minute)},
		budgets: map[string]bus.BudgetSnapshot{
			"anthropic": {Provider: "anthropic", Windows: []bus.BudgetWindow{
				{Name: "5h", UsedPct: 62, ResetsAt: now.Add(2 * time.Hour).Unix(), Minutes: 300, ObservedAt: ms(time.Minute)},
				{Name: "7d", UsedPct: 41, ResetsAt: now.Add(72 * time.Hour).Unix(), Minutes: 10080, ObservedAt: ms(time.Minute)}}},
			"xai": {Provider: "xai", Windows: []bus.BudgetWindow{
				{Name: "5h", UsedPct: 100, ResetsAt: now.Add(40 * time.Minute).Unix(), Minutes: 300, ObservedAt: ms(time.Minute)}}},
		},
		holds: map[string]int64{"xai": now.Add(40 * time.Minute).Unix()},
		pace:  map[string]string{},
		asks: []bus.Ask{{ID: fmt.Sprintf("%d-0", ms(4*time.Minute)), From: "master", Title: "review lot 1?", State: "open",
			CreatedAt: ms(4 * time.Minute), Questions: []bus.AskQuestionState{q("go", "Merge lot 1 now?"), q("tag", "Tag a release?")}}},
		board: map[string]bus.BoardEntry{
			"busmon-charm-b": {Owner: "coder", State: "accepted", Updated: ago(20 * time.Minute).Unix()},
			"grok-adapter":   {Owner: "coder", State: "done", Updated: ago(3 * time.Hour).Unix()},
		},
		agents: map[string]*agentState{
			"master": {state: "waiting", message: "see my ask", stateAt: ago(12 * time.Minute), lastSeen: ago(time.Minute),
				openAsk: 2, askID: fmt.Sprintf("%d-0", ms(4*time.Minute)), registered: true, armed: true, ctxRaw: "41000", paused: true,
				ctxFill: &bus.ContextFill{Pct: 21, Session: "s-master", ObservedAt: ms(time.Minute), At: ms(0), Alert: &fixtureAlert}},
			"coder": {state: "working", message: "busmon-charm-b: b1 shell", stateAt: ago(2 * time.Minute), lastSeen: ago(time.Minute),
				registered: true, paused: true, ctxRaw: "88000", gated: 1,
				ctxFill: &bus.ContextFill{Pct: 85, Session: "s-coder", ObservedAt: ms(2 * time.Minute), At: ms(0), Alert: &fixtureAlert}, gates: map[string]string{"pr-122": "bernard|look at b1"},
				wake: &bus.WakeRecord{State: "deferred"}},
			"foureyes": {state: "waiting", message: "need a decision on the width rule", stateAt: ago(6 * time.Minute), lastSeen: ago(6 * time.Minute),
				registered: true, wake: &bus.WakeRecord{Escalated: "true"}},
			"ui-grok": {state: "idle", message: "reviewed", stateAt: ago(3 * time.Minute), lastSeen: ago(3 * time.Minute), registered: true,
				provider: "xai", held: true, swapping: true, obsAt: ago(5 * time.Second), clientUpdate: &bus.ClientUpdate{Client: "grok", Installed: "1.1"}},
			"ui-kimi": {state: "idle", message: "bernard: your call on the tabs", stateAt: ago(9 * time.Minute), lastSeen: ago(9 * time.Minute),
				registered: true, paused: true, forYou: true},
			"release": {state: "working", message: "tagging", stateAt: ago(7 * time.Minute), lastSeen: ago(7 * time.Minute), registered: true,
				fallback: &bus.FallbackRecord{Profile: "kimi-alt"}},
			"qa": {state: "blocked", message: "out of quota", stateAt: ago(11 * time.Minute), lastSeen: ago(11 * time.Minute), registered: true,
				provider: "xai", held: true},
			"sentinel": {state: "idle", message: "health ok", stateAt: ago(30 * time.Minute), lastSeen: ago(30 * time.Minute), registered: true, paused: true,
				obsPrompt: true, obsState: "blocked", obsAt: ago(10 * time.Second), obsSince: ago(90 * time.Second), pid: 41,
				model: &bus.ModelVerdict{PID: 41, Verdict: "drift"}},
			"architect": {state: "done", message: "spec merged", stateAt: ago(40 * time.Minute), lastSeen: ago(40 * time.Minute), registered: true,
				obsBusy: true, obsAt: ago(5 * time.Second),
				wake: &bus.WakeRecord{Escalated: "pending"}},
			"docs": {state: "blocked", message: "waiting for the PR", stateAt: ago(50 * time.Minute), lastSeen: ago(50 * time.Minute), registered: true,
				armed: true, stale: 2},
			"security": {state: "idle", message: "nothing new", stateAt: ago(26 * time.Hour), lastSeen: ago(26 * time.Hour), registered: true},
		},
		requests: []request{},
	}
}

// shellEvents is the fixture's Activity: statuses, two unread lines for the
// person, an ask and a report whose fenced code is longer than any width.
func shellEvents(now time.Time) []bus.Event {
	id := func(d time.Duration) string { return fmt.Sprintf("%d-0", now.Add(-d).UnixMilli()) }
	code := "```go\nfunc veryLongFunctionNameForTheGolden(argumentOne string, argumentTwo int, argumentThree []string) (result string, err error) {\n```"
	return []bus.Event{
		{ID: id(40 * time.Minute), Kind: "status", Agent: "architect", State: "done", Message: "spec merged"},
		{ID: id(14 * time.Minute), Kind: "cmd", Type: bus.CmdDirective, From: "bernard", Target: "coder", Message: "review lot 1", Human: true},
		{ID: id(13 * time.Minute), Kind: "report", Agent: "coder", RKind: bus.ReportNote, Message: "Lot 1 delivered",
			Full: "Lot 1 delivered\n## Done\n- adapter in `d0a72be`; tests green\n" + code},
		{ID: id(12 * time.Minute), Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "bernard", Message: "please look at the plan"},
		{ID: id(8 * time.Minute), Kind: "notify", From: "dispatch", Message: "project paused until 16:56", For: bus.ForPerson},
		{ID: id(4 * time.Minute), Kind: "cmd", Type: bus.CmdAsk, From: "master", Target: "people", Message: "review lot 1?"},
		{ID: id(2 * time.Minute), Kind: "status", Agent: "coder", State: "working", Message: "busmon-charm-b: b1 shell"},
	}
}

// fixtureShell is the shell on the fixture at w x h, as the program would
// have it after a size, a poll and the tail's backfill.
func fixtureShell(t testing.TB, w, h int) Model {
	t.Helper()
	m := newShell(shellConfig{project: "agent-bus-monitor", self: "bernard"})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, tailMsg{events: shellEvents(shellNow)})
	snap := shellFixture(shellNow)
	for _, a := range snap.agents {
		a.judgedAt = shellNow // as takeSnapshot stamps it
	}
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	return m
}

// step is one Update, the command it returns ignored (tests drive messages).
func step(t testing.TB, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// pinLocal pins the local zone: rows print local clock times.
func pinLocal(t testing.TB) {
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
}

// shellWith is the fixture shell built on cfg (its writer, settings, cache,
// prior visit), with the project pause on or off.
func shellWith(t testing.TB, w, h int, cfg shellConfig, paused bool) Model {
	t.Helper()
	cfg.project, cfg.self = "agent-bus-monitor", "bernard"
	m := newShell(cfg)
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, tailMsg{events: shellEvents(shellNow)})
	snap := shellFixture(shellNow)
	if !paused {
		snap.pause = nil
	}
	for _, a := range snap.agents {
		a.judgedAt = shellNow
	}
	return step(t, m, pollMsg{snap: snap, at: shellNow})
}

// run executes a command and feeds its message back, as the program would;
// a batch runs each of its commands that return at once (never a tick).
func run(t testing.TB, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			m = run(t, m, c)
		}
		return m
	}
	return step(t, m, msg)
}

// typeText types s, one key per rune.
func typeText(t testing.TB, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = keys(t, m, r)
	}
	return m
}
