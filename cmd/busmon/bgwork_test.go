package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// background-work-visible §3, step 3. bgAgent is an idle agent of pid 7 seen
// by the dispatcher just now, with no background work of any kind.
func bgAgent(now time.Time) *agentState {
	return &agentState{state: "idle", message: "turn ended", stateAt: now, pid: 7, client: "claude",
		obsAt: now, obsOwnPID: true}
}

// hooksRecord is a Stop's record for pid 7: two shell tasks and a sub-agent,
// the oldest first seen 14 minutes ago, and one cron.
func hooksRecord(now time.Time) *bus.BgTasks {
	return &bus.BgTasks{Source: "hooks", Event: "Stop", At: now.Add(-4 * time.Minute).UnixMilli(), PID: 7,
		Tasks: []bus.BgTask{
			{IDHash: "a", Type: "shell", Status: "running", FirstSeen: now.Add(-14 * time.Minute).UnixMilli()},
			{IDHash: "b", Type: "shell", Status: "running", FirstSeen: now.Add(-2 * time.Minute).UnixMilli()},
			{IDHash: "c", Type: "subagent", AgentType: "Explore", Status: "running", FirstSeen: now.Add(-3 * time.Minute).UnixMilli()},
		},
		Crons: []bus.BgCron{{IDHash: "d", Schedule: "*/5 * * * *", Recurring: true}}}
}

// One mark per agent: the declared text wins over the observed count; the
// hooks' tasks counted together with the oldest's age; the process source
// alone a lower bound marked ?; a cron a ⏰ after the mark, never counted.
func TestBackgroundMarkPerSource(t *testing.T) {
	now := shellNow
	a := bgAgent(now)
	if got := backgroundText(a, now); got != "" {
		t.Fatalf("no work: %q", got)
	}
	a.bgTasks = hooksRecord(now)
	if got := backgroundText(a, now); got != GlyphEllipsis+" 3 bg tasks 14m "+GlyphClock {
		t.Fatalf("hooks: %q", got)
	}
	a.bgTasks.Tasks = a.bgTasks.Tasks[:1]
	a.bgTasks.Crons = nil
	if got := backgroundText(a, now); got != GlyphEllipsis+" 1 bg task 14m" {
		t.Fatalf("one task: %q", got)
	}
	a.bg = &bus.Background{Text: "2 checks running", Since: now.Add(-4 * time.Minute)}
	if got := backgroundText(a, now); got != GlyphEllipsis+" 2 checks running 4m" {
		t.Fatalf("the declared text wins: %q", got)
	}
	a.bg, a.bgTasks = nil, nil
	a.obsBgProcs, a.obsBgOldest = 2, now.Add(-9*time.Minute)
	if got := backgroundText(a, now); got != GlyphEllipsis+" bg ≥1? 9m" {
		t.Fatalf("the process source alone: %q", got)
	}
	// A hooks reading of none does not hide a process the tree still sees.
	a.bgTasks = &bus.BgTasks{Source: "hooks", Event: "Stop", At: now.UnixMilli(), PID: 7, Tasks: []bus.BgTask{}}
	if got := backgroundText(a, now); got != GlyphEllipsis+" bg ≥1? 9m" {
		t.Fatalf("hooks none, a process seen: %q", got)
	}
	a.obsBgProcs = 0
	a.bgTasks.Crons = []bus.BgCron{{IDHash: "d", Schedule: "0 9 * * 1", Recurring: true}}
	if got := backgroundText(a, now); got != GlyphClock {
		t.Fatalf("a cron alone: %q", got)
	}
}

// Stale entries are not shown: a record of another pid (a relaunch), one past
// its hour, and an observation of another process or too old.
func TestBackgroundMarkSkipsStaleEntries(t *testing.T) {
	now := shellNow
	a := bgAgent(now)
	a.bgTasks = hooksRecord(now)
	a.bgTasks.PID = 8
	if got := backgroundText(a, now); got != "" {
		t.Fatalf("another pid's record: %q", got)
	}
	a.bgTasks = hooksRecord(now)
	a.bgTasks.At = now.Add(-bus.BgTasksTTL - time.Second).UnixMilli()
	if got := backgroundText(a, now); got != "" {
		t.Fatalf("a record past its hour: %q", got)
	}
	a.bgTasks = nil
	a.obsBgProcs, a.obsBgOldest = 1, now.Add(-time.Minute)
	a.obsOwnPID = false
	if got := backgroundText(a, now); got != "" {
		t.Fatalf("an observation of another process: %q", got)
	}
	a.obsOwnPID, a.obsAt = true, now.Add(-observedFresh-time.Second)
	if got := backgroundText(a, now); got != "" {
		t.Fatalf("an observation too old: %q", got)
	}
}

// The row carries the mark after the message; the state word stays idle (Q1).
func TestObservedBackgroundOnTheAgentRow(t *testing.T) {
	now := shellNow
	a := bgAgent(now)
	a.bgTasks = hooksRecord(now)
	row := agentRow("", "coder", a, false, TierWide, 160, now).text()
	if !strings.Contains(row, "turn ended") || !strings.Contains(row, GlyphEllipsis+" 3 bg tasks 14m "+GlyphClock) || !strings.Contains(row, "idle") {
		t.Fatalf("row %q", row)
	}
}

// The header chip keeps the declared texts and counts an agent with observed
// work alone as bg; a declared one comes before an observed one.
func TestHeaderChipCountsObservedWork(t *testing.T) {
	now := shellNow
	observed := bgAgent(now)
	observed.bgTasks = hooksRecord(now)
	declared := &agentState{state: "idle", bg: &bus.Background{Text: "build", Since: now}}
	agents := map[string]*agentState{"alpha": observed, "coder": declared, "foureyes": {state: "idle"}}
	if got := backgroundChip(agents, "", now); got != GlyphEllipsis+" coder: build +1" {
		t.Errorf("chip %q", got)
	}
	delete(agents, "coder")
	if got := backgroundChip(agents, "", now); got != GlyphEllipsis+" alpha: bg" {
		t.Errorf("observed alone %q", got)
	}
	agents["alpha"].bgTasks.Tasks = nil // a cron is not running work
	if got := backgroundChip(agents, "", now); got != "" {
		t.Errorf("a cron counted %q", got)
	}
}

// The idle view's team frame shows an idle agent with observed work too.
func TestIdleTeamShowsObservedBackground(t *testing.T) {
	m := idleAt(t, 140, 40)
	idle := ""
	names, _ := visibleAgents(m.snap.agents, m.snap.pilot, false, m.now)
	for _, n := range names {
		if _, _, role, group := stateLook(m.snap.agents[n]); group > 3 && role != RoleAttention {
			idle = n
			break
		}
	}
	if idle == "" {
		t.Fatal("the fixture has no idle agent")
	}
	before := m.idleData()
	a := m.snap.agents[idle]
	a.pid, a.obsAt, a.obsOwnPID = 7, m.now, true
	a.bgTasks = hooksRecord(m.now)
	after := m.idleData()
	if after.idle != before.idle || len(after.team) != len(before.team)+1 {
		t.Fatalf("idle %d (was %d), team %d (was %d)", after.idle, before.idle, len(after.team), len(before.team))
	}
}

// The page's Background section: the declared text with its age and who set
// it, one line per observed task, the source and its report's age, the live
// program names, the crons with their schedule, and the ages' origin.
func TestBackgroundSectionOnThePage(t *testing.T) {
	now := shellNow
	a := bgAgent(now)
	a.bg = &bus.Background{Text: "2 checks running", Since: now.Add(-4 * time.Minute), By: "coder"}
	a.bgTasks = hooksRecord(now)
	live := &termView{found: true, procs: []string{"make", "go", "make"}}
	got := plainOf(backgroundSection(a, live, now))
	for _, want := range []string{
		"Background",
		"2 checks running", "4m", "set by coder",
		"shell " + GlyphNone + " running " + GlyphNone + " 14m",
		"subagent Explore " + GlyphNone + " running " + GlyphNone + " 3m",
		"from Claude Code's hooks, last report 4m ago",
		"running under it now: go, make",
		"*/5 * * * *", "recurring",
		"counted from when agentbus first saw",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	// The process source alone, and no live read yet.
	a.bg, a.bgTasks = nil, nil
	a.obsBgProcs, a.obsBgOldest = 2, now.Add(-9*time.Minute)
	got = plainOf(backgroundSection(a, nil, now))
	for _, want := range []string{"from the process tree", "at least 1", "9m"} {
		if !strings.Contains(got, want) {
			t.Errorf("process source lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "running under it now") {
		t.Errorf("names without a live read:\n%s", got)
	}
	// Nothing at all: no section.
	if ls := backgroundSection(bgAgent(now), nil, now); len(ls) != 0 {
		t.Errorf("an empty section: %q", plainOf(ls))
	}
}

// The live names are de-duplicated and sorted, and pass through Preview: no
// byte a program named itself reaches busmon's terminal.
func TestBackgroundLiveNames(t *testing.T) {
	got := liveNames([]string{"make", "\x1b]2;x\x07evil", "make", "go"})
	if len(got) != 3 || got[1] != "go" || got[2] != "make" {
		t.Fatalf("names %q", got)
	}
	for _, n := range got {
		if strings.ContainsRune(n, 0x1b) || strings.ContainsRune(n, 0x07) {
			t.Fatalf("a control byte: %q", got)
		}
	}
}

// The terminal read takes the program names of the agent's descendants, the
// agent's own process left out; they live in the page's memory only.
func TestTermViewReadsProgramNames(t *testing.T) {
	h := fake.New()
	tm := startTerm(t, h, "p", "coder")
	h.SetScreen(tm.ID, "claude> hello")
	h.SetProcesses(tm.ID,
		termhost.Proc{PID: tm.PID, Command: "claude"},
		termhost.Proc{PID: tm.PID + 1, PPID: tm.PID, Command: "bash"},
		termhost.Proc{PID: tm.PID + 2, PPID: tm.PID + 1, Command: "make"})
	v := readTermView(context.Background(), h, "p", "coder", 20)
	if !v.found || strings.Join(v.procs, ",") != "bash,make" {
		t.Fatalf("view %+v", v)
	}
}

// The poll reads the hooks' records and the observation's process fields.
func TestPollReadsObservedBackground(t *testing.T) {
	now := time.Now()
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"coder": {State: "idle", TS: 1, Pane: "p2", PID: 7}},
		bgTasks:  map[string]bus.BgTasks{"coder": *hooksRecord(now)},
		observed: map[string]bus.Observation{"coder": {At: now.UnixMilli(), PID: 7, State: "idle", BgProcs: 2, BgOldest: now.Add(-9 * time.Minute).UnixMilli()}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	poll := func() {
		for i := 0; i < 5; i++ {
			pollOnce(context.Background(), f, st, i)
		}
	}
	poll()
	a := st.agents["coder"]
	if a.bgTasks == nil || len(a.bgTasks.Tasks) != 3 || a.obsBgProcs != 2 || a.obsBgOldest.IsZero() {
		t.Fatalf("coder %+v procs %d oldest %v", a.bgTasks, a.obsBgProcs, a.obsBgOldest)
	}
	f.bgTasksErr = errors.New("timeout")
	poll()
	if a := st.agents["coder"]; a.bgTasks != nil || !a.bgFailed {
		t.Fatalf("after a failed read: %+v failed=%v", a.bgTasks, a.bgFailed)
	}
	f.bgTasksErr, f.bgTasks = nil, nil
	poll()
	if a := st.agents["coder"]; a.bgTasks != nil || a.bgFailed {
		t.Fatalf("a record gone: %+v failed=%v", a.bgTasks, a.bgFailed)
	}
}

// The agent's page carries the section, with the names of its page's live
// read once it arrives.
func TestAgentPageShowsTheBackgroundSection(t *testing.T) {
	m := fixtureShell(t, 140, 60)
	a := m.snap.agents["foureyes"]
	a.pid, a.obsAt, a.obsOwnPID = 7, m.now, true
	a.bgTasks = hooksRecord(m.now)
	m.pushAgent("foureyes")
	p := m.rd.top()
	if p == nil || p.kind != ovAgent {
		t.Fatal("no agent page")
	}
	got := plainOf(p.body)
	if !strings.Contains(got, "from Claude Code's hooks") || strings.Contains(got, "running under it now") {
		t.Fatalf("page before a live read:\n%s", got)
	}
	m.gotTermView(termViewMsg{serial: p.serial, view: termView{found: true, procs: []string{"make"}}})
	if got := plainOf(m.rd.top().body); !strings.Contains(got, "running under it now: make") {
		t.Fatalf("page after a live read:\n%s", got)
	}
}
