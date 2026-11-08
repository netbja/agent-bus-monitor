package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// progress-line §4.3: the background work is read with the dispatcher facts;
// a failed read keeps the last value; an expired one goes.
func TestPollReadsTheBackground(t *testing.T) {
	since := time.Now().Add(-4 * time.Minute)
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"master": {State: "idle", TS: 1, Pane: "p1", PID: 7}, "coder": {State: "idle", TS: 1, Pane: "p2", PID: 8}},
		backgrounds: map[string]bus.Background{"master": {Text: "2 checks running", Since: since, By: "master"}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	tick := 0
	poll := func() {
		for i := 0; i < 5; i++ {
			pollOnce(context.Background(), f, st, tick)
			tick++
		}
	}
	poll()
	if b := st.agents["master"].bg; b == nil || b.Text != "2 checks running" || st.agents["coder"].bg != nil {
		t.Fatalf("master %+v coder %+v", b, st.agents["coder"].bg)
	}
	// reviewer-codex, spec §4.3: a failed read shows nothing of the old work,
	// and the row's read marker says the read failed.
	f.backgroundErr = errors.New("timeout")
	poll()
	if a := st.agents["master"]; a.bg != nil || !a.bgFailed {
		t.Fatalf("after a failed read: %+v failed=%v", a.bg, a.bgFailed)
	}
	if got := backgroundText(st.agents["master"], time.Now()); got != GlyphEllipsis+" ?" {
		t.Fatalf("the row's marker %q", got)
	}
	if got := backgroundChip(st.agents, "master"); got != "" {
		t.Fatalf("the header shows a failed read: %q", got)
	}
	f.backgroundErr = nil
	poll()
	if a := st.agents["master"]; a.bg == nil || a.bgFailed {
		t.Fatalf("a good read after: %+v failed=%v", a.bg, a.bgFailed)
	}
	f.backgrounds = nil
	poll()
	if b := st.agents["master"].bg; b != nil {
		t.Fatalf("an expired record kept: %+v", b)
	}
}

// The agent's row: "… 2 checks running 4m" after its message, muted.
func TestBackgroundOnTheAgentRow(t *testing.T) {
	now := shellNow
	a := &agentState{state: "idle", message: "turn ended", stateAt: now, bg: &bus.Background{Text: "2 checks running", Since: now.Add(-4 * time.Minute)}}
	row := agentRow("", "master", a, true, TierWide, 160, now)
	if !strings.Contains(row.text(), "turn ended") || !strings.Contains(row.text(), GlyphEllipsis+" 2 checks running 4m") {
		t.Fatalf("row %q", row.text())
	}
	for _, s := range row {
		if strings.Contains(s.Text, "2 checks") && s.Role != RoleMuted {
			t.Errorf("not muted: %+v", s)
		}
	}
	a.bg = nil
	if row := agentRow("", "master", a, true, TierWide, 160, now); strings.Contains(row.text(), GlyphEllipsis+" ") {
		t.Fatalf("no record, still shown: %q", row.text())
	}
}

// The header chip: the pilot's first, then +N; none without a record.
func TestHeaderBackgroundChip(t *testing.T) {
	bg := func(text string) *agentState {
		return &agentState{state: "idle", bg: &bus.Background{Text: text, Since: shellNow}}
	}
	agents := map[string]*agentState{"coder": bg("build"), "master": bg("2 checks running"), "foureyes": {state: "idle"}}
	if got := backgroundChip(agents, "master"); got != GlyphEllipsis+" master: 2 checks running +1" {
		t.Errorf("chip %q", got)
	}
	if got := backgroundChip(agents, ""); got != GlyphEllipsis+" coder: build +1" { // no pilot: by name
		t.Errorf("no pilot %q", got)
	}
	if got := backgroundChip(map[string]*agentState{"foureyes": {state: "idle"}}, "master"); got != "" {
		t.Errorf("none %q", got)
	}
	d := headerData{project: "demo", pilot: "master", background: backgroundChip(agents, "master"), now: shellNow, shell: true}
	wide := ""
	for _, l := range headerLines(d, 200, TierWide) {
		wide += l.text() + "\n"
	}
	if !strings.Contains(wide, "master: 2 checks running +1") {
		t.Errorf("wide header:\n%s", wide)
	}
	narrow := ""
	for _, l := range headerLines(d, 32, TierNarrow) { // room for "pilot master", not for the chip
		narrow += l.text() + "\n"
	}
	if strings.Contains(narrow, "checks") || !strings.Contains(narrow, "pilot master") {
		t.Errorf("narrow: the chip goes first:\n%s", narrow)
	}
}

// The idle view's team frame shows an idle agent with background work, its
// text after it; it still counts as idle.
func TestIdleTeamShowsBackground(t *testing.T) {
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
	m.snap.agents[idle].bg = &bus.Background{Text: "2 checks running", Since: m.now}
	after := m.idleData()
	if after.idle != before.idle {
		t.Errorf("idle count %d, was %d", after.idle, before.idle)
	}
	found := false
	for _, l := range after.team {
		if strings.Contains(l.text(), idle) && strings.Contains(l.text(), "2 checks running") {
			found = true
		}
	}
	if !found || len(after.team) != len(before.team)+1 {
		t.Fatalf("team %d rows (was %d), %s not shown with its work", len(after.team), len(before.team), idle)
	}
	m.snap.agents[idle].bg, m.snap.agents[idle].bgFailed = nil, true
	if failed := m.idleData(); len(failed.team) != len(before.team) {
		t.Fatalf("a failed read added a team row: %d (was %d)", len(failed.team), len(before.team))
	}
}

// Admitted last: with room for only one, the update notice (rank 101) stays
// and the chip goes.
func TestHeaderBackgroundChipGoesFirst(t *testing.T) {
	chip := GlyphEllipsis + " master: 2 checks running"
	d := headerData{project: "demo", pilot: "master", update: "v9.9.9", background: chip, now: shellNow, shell: true}
	text := func(w int) string {
		out := ""
		for _, l := range headerLines(d, w, TierWide) {
			out += l.text() + "\n"
		}
		return out
	}
	tried := 0
	for w := 30; w <= 120; w++ {
		got := text(w)
		hasChip, hasUpdate := strings.Contains(got, "2 checks running"), strings.Contains(got, "v9.9.9")
		if hasChip && !hasUpdate {
			t.Fatalf("%d cells: the chip took the update's room:\n%s", w, got)
		}
		if hasUpdate && !hasChip {
			tried++
		}
	}
	if tried == 0 {
		t.Fatal("no width keeps the update without the chip: the test proves nothing")
	}
}
