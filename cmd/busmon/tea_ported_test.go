package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Scenarios that drove tview's ui (step c, busmon Charm spec: "screen_test,
// capture_test and ui_test scenarios ... ported to model scenarios"), on the
// shell. Each names the tview test it replaces.

// shellOf is the shell on st, as the program has it after a size and a poll.
func shellOf(t testing.TB, st *shared, w, h int, fw *fakeWriter) Model {
	t.Helper()
	cfg := shellConfig{project: "demo", self: "bernard", st: st}
	if fw != nil {
		cfg.w = fw
	}
	m := newShell(cfg)
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	now := time.Now()
	return step(t, m, pollMsg{snap: takeSnapshot(st, now), at: now})
}

// From TestPlanOverlayKeys: P opens the plan; j moves; Enter opens a task,
// Esc returns to the plan with the selection kept; Enter on the done group
// expands it; Esc closes; with no plan, P says so.
func TestShellPlanPageKeys(t *testing.T) {
	f := newPlanFake(true)
	f.ledger = []bus.Verdict{{ID: "6-0", Subject: "reload-impl", Author: "coder", Reviewer: "foureyes", Decision: "reject"}}
	f.facts = map[string]bus.PlanFact{"roles": {Done: time.Now().Add(-time.Hour).UnixMilli()}}
	st := newPlanShared()
	pollOnce(context.Background(), f, st, 1)
	m := keys(t, shellOf(t, st, 140, 40, nil), 'P')
	p := m.rd.top()
	if p == nil || p.kind != ovPlan || !strings.Contains(m.grid().plain(), "Client reload") {
		t.Fatalf("P did not open the plan:\n%s", m.grid().plain())
	}
	first := p.items[p.sel].task
	m = keys(t, m, 'j')
	second := m.rd.top().items[m.rd.top().sel].task
	if first == second || second == "" {
		t.Fatalf("j did not move: %q %q", first, second)
	}
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p.kind != ovPlanTask || !strings.Contains(p.plain, "State:") {
		t.Fatalf("enter did not open the task: %v %q", p.kind, p.plain)
	}
	m = keys(t, m, kEsc)
	if p := m.rd.top(); p.kind != ovPlan || p.items[p.sel].task != second {
		t.Fatal("esc from a task returns to the plan, selection kept")
	}
	p = m.rd.top()
	for i, it := range p.items {
		if it.toggle {
			p.sel = i
		}
	}
	m = keys(t, m, kEnter)
	if p := m.rd.top(); !p.expanded || !strings.Contains(p.plain, "merged (recorded") {
		t.Fatalf("enter on done did not expand: %q", p.plain)
	}
	if m = keys(t, m, kEsc); m.rd.open() {
		t.Fatal("esc did not close")
	}
	m2 := keys(t, shellOf(t, newPlanShared(), 140, 40, nil), 'P')
	if m2.rd.open() || !strings.Contains(m2.ui.flash, "no plan") {
		t.Fatalf("P with no plan: open %v, flash %q", m2.rd.open(), m2.ui.flash)
	}
}

// From TestPlanOverlayEscapesOnce: hostile text in the plan is shown as
// written (the shell writes no markup to escape).
func TestShellPlanPageShowsHostileTextAsWritten(t *testing.T) {
	st := newPlanShared()
	st.plan = hostilePlan(time.Now())
	scr := keys(t, shellOf(t, st, 160, 50, nil), 'P').grid().plain()
	if !strings.Contains(scr, "Ti[red]tle") || !strings.Contains(scr, "goal [x]") {
		t.Fatalf("hostile text not shown as written:\n%s", scr)
	}
}

// readerScreen is the shell at w x h with page opened in the reader, as text.
func readerScreen(t testing.TB, w, h int, snap func(*snapshot), p page) string {
	t.Helper()
	m := fixtureShell(t, w, h)
	s := shellFixture(shellNow)
	if snap != nil {
		snap(&s)
	}
	for _, a := range s.agents {
		a.judgedAt = shellNow
	}
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	m.push(p)
	return m.grid().plain()
}

// noWiderThan reports a row wider than the terminal.
func noWiderThan(t *testing.T, screen string, w int) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimRight(screen, "\n"), "\n") {
		if n := Width(line); n > w {
			t.Errorf("row %d is %d cells wide, terminal is %d: %q", i, n, w, line)
		}
	}
}

// From TestScreenDetailShowsLongUnicodeBodyAndFitsNarrowTerminals: a long
// multi-line Unicode report in the reader, wrapped at its width, never past
// the terminal (below the shell's minimum size it says "too small" instead).
func TestShellReaderShowsLongUnicodeBodyAndFitsNarrowTerminals(t *testing.T) {
	pinLocal(t)
	body := "résumé des étapes:\n" +
		strings.Repeat("  • une ligne assez longue pour déborder d'un terminal étroit — vraiment\n", 12) +
		"  • 日本語の行もある\n  • dernière ligne"
	e := bus.Event{ID: "1758141663000-0", Kind: "report", Agent: "coder", RKind: bus.ReportNote,
		Message: "résumé des étapes: • une ligne assez longue…", Full: body}
	for _, size := range [][2]int{{100, 30}, {60, 20}} {
		screen := readerScreen(t, size[0], size[1], nil, messagePage(e, time.Date(2026, 9, 17, 22, 41, 3, 0, time.UTC)))
		noWiderThan(t, screen, size[0])
		for _, want := range []string{"coder", "résumé des étapes", "2026-09-17 22:41:03"} {
			if !strings.Contains(screen, want) {
				t.Errorf("%v: no %q:\n%s", size, want, screen)
			}
		}
	}
}

// From TestScreenDetailAdmitsUnknownCompletenessOnOldEntries.
func TestShellReaderAdmitsUnknownCompletenessOnOldEntries(t *testing.T) {
	e := bus.Event{ID: "1-0", Kind: "report", Agent: "helper", RKind: bus.ReportNote,
		Message: "an old report from before the marker existed"}
	if screen := readerScreen(t, 140, 40, nil, messagePage(e, time.Unix(1, 0))); !strings.Contains(screen, "completeness not marked") {
		t.Errorf("an unmarked entry must say its completeness is unverified:\n%s", screen)
	}
}

// From TestScreenRequestsOverlayShowsEachRequestHonestly and
// TestScreenRequestsOverlayAtFortyColumns: the requests page lists each
// request, a deadline only where one is set, inside the terminal.
func TestShellRequestsPageShowsEachRequestHonestly(t *testing.T) {
	now := shellNow
	reqs := []request{
		{Tracked: true, Task: "task-20", Target: "foureyes", State: "requested",
			Created: now.Add(-42 * time.Minute), Updated: now.Add(-42 * time.Minute),
			Delivery: "output_written", Expires: now.Add(18 * time.Minute)},
		{Tracked: true, Task: "task-21", Target: "coder", State: "accepted",
			Created: now.Add(-2 * time.Hour), Updated: now.Add(-20 * time.Minute),
			Accepted: now.Add(-20 * time.Minute), Delivery: "queued"},
		{Tracked: true, Task: "task-22", Target: "coder", State: "blocked",
			Created: now.Add(-3 * time.Hour), Updated: now.Add(-time.Hour),
			BlockedReason: "waiting on an API key", Delivery: "output_written"},
	}
	screen := readerScreen(t, 140, 60, func(s *snapshot) { s.requests = reqs }, page{kind: ovRequests})
	for _, want := range []string{"task-20", "task-21", "task-22", "deadline in 18m",
		"no acceptance recorded", "waiting on an API key", "no deadline set"} {
		if !strings.Contains(screen, want) {
			t.Errorf("requests page missing %q:\n%s", want, screen)
		}
	}
	if strings.Count(screen, "deadline in") != 1 {
		t.Errorf("only the request that carries a deadline may show one:\n%s", screen)
	}
	noWiderThan(t, screen, 140)
	narrow := readerScreen(t, 60, 20, func(s *snapshot) {
		s.requests = []request{{Tracked: true, Task: "a-very-long-task-slug-indeed", Target: "foureyes",
			State: "requested", Created: now.Add(-time.Hour), Updated: now.Add(-time.Hour), Delivery: "queued"}}
	}, page{kind: ovRequests})
	noWiderThan(t, narrow, 60)
	if !strings.Contains(narrow, "a-very-long-task") {
		t.Errorf("the task must still be identifiable at 60 columns:\n%s", narrow)
	}
}

// drawShell is the shell on st at w x h, as text (tview's harness draw).
func drawShell(t testing.TB, st *shared, w, h int) string {
	t.Helper()
	return shellOf(t, st, w, h, nil).grid().plain()
}

var (
	kCtrlT = tea.Key{Code: 't', Mod: tea.ModCtrl}
	kLeft  = tea.Key{Code: tea.KeyLeft}
)

// sentBy is what the fake writer sent, joined.
func sentBy(fw *fakeWriter) string {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return strings.Join(fw.sent, "|")
}

// typeLineShell opens the line, types text, and sends it (its command run).
func typeLineShell(t testing.TB, m Model, text string) Model {
	t.Helper()
	m = typeText(t, keys(t, m, 'i'), text)
	m, cmd := pressCmd(t, m, kEnter)
	return run(t, m, cmd)
}

// --- cells_test.go: TestDrawFrame, TestDrawLineStylesAndBounds ---

func TestDrawFrameOnTheGrid(t *testing.T) {
	g := newGrid(20, 6)
	drawFrame(g, 0, 0, 20, 6, Frame{Title: Line{{Text: strings.Repeat("t", 40), Role: RoleText}}, Label: Line{{Text: "label", Role: RoleMuted}}, Scroll: ScrollState{Total: 10, Top: 0, Visible: 4}})
	for _, tt := range []struct {
		x, y int
		r    string
	}{{0, 0, "╭"}, {19, 0, "╮"}, {0, 5, "╰"}, {19, 5, "╯"}, {19, 1, "▲"}, {19, 4, "▼"}, {19, 2, "┃"}} {
		if got := g.at(tt.x, tt.y).text; got != tt.r {
			t.Errorf("(%d,%d)=%q want %q", tt.x, tt.y, got, tt.r)
		}
	}
	// Tiny frames keep their writes in their rectangle.
	for w := 0; w <= 8; w++ {
		for h := 0; h <= 6; h++ {
			s := &recSink{grid: newGrid(20, 6), rect: Rect{2, 0, w, h}}
			drawFrame(s, 2, 0, w, h, Frame{Title: Line{{Text: "日本title", Role: RoleText}}, Scroll: ScrollState{Total: 100, Top: 99, Visible: 1}})
			if len(s.bad) > 0 {
				t.Fatalf("%dx%d: writes outside %v", w, h, s.bad)
			}
		}
	}
}

func TestDrawLineStylesAndBoundsOnTheGrid(t *testing.T) {
	s := &recSink{grid: newGrid(12, 2), rect: Rect{2, 0, 5, 1}}
	drawLine(s, 2, 0, 5, Line{{Text: "日", Role: RoleError, Bold: true}, {Text: "[x]", Role: RoleText}}, true)
	if c := s.at(2, 0); c.text != "日" || c.st.Role != RoleError || !c.st.Bold || !c.st.Selected {
		t.Fatalf("first cell %+v", c)
	}
	if len(s.bad) > 0 {
		t.Fatalf("outside draw: %v", s.bad)
	}
	g := newGrid(12, 2)
	drawLine(g, 0, 1, 3, Line{{Text: "éx", Role: RoleOK}}, false)
	if c := g.at(0, 1); c.text != "é" {
		t.Fatalf("combining cluster lost: %q", c.text)
	}
}

// --- keys_test.go: TestLookupMatrix (on engine-neutral keys) ---

func TestLookupMatrix(t *testing.T) {
	kc := func(c KeyCode) Key { return Key{Code: c} }
	rk := func(r rune) Key { return Key{Code: keyRune, Rune: r} }
	cases := []struct {
		name string
		m    Mode
		p    PanelID
		t    Tier
		k    Key
		want Action
	}{
		{"activity End is live", ModeShell, PanelActivity, TierWide, kc(keyEnd), ActLive},
		{"activity G is live", ModeShell, PanelActivity, TierWide, rk('G'), ActLive},
		{"agents End is last", ModeShell, PanelAgents, TierWide, kc(keyEnd), ActLast},
		{"for you End is last", ModeShell, PanelForYou, TierWide, kc(keyEnd), ActLast},
		{"board G is last", ModeShell, PanelBoard, TierWide, rk('G'), ActLast},
		{"input q is text", ModeInput, PanelAgents, TierWide, rk('q'), ActNone},
		{"input Tab completes", ModeInput, PanelAgents, TierWide, kc(keyTab), ActComplete}, // named for the tour; the field still completes
		{"input digit is text", ModeInput, PanelAgents, TierMedium, rk('1'), ActNone},
		{"input Enter sends", ModeInput, PanelAgents, TierWide, kc(keyEnter), ActSend},
		{"input Esc", ModeInput, PanelAgents, TierWide, kc(keyEscape), ActEscape},
		{"filter q is text", ModeFilter, PanelActivity, TierWide, rk('q'), ActNone},
		{"filter Enter applies", ModeFilter, PanelActivity, TierWide, kc(keyEnter), ActSend},
		{"panel q quits", ModeShell, PanelAgents, TierWide, rk('q'), ActQuit},
		{"panel Esc", ModeShell, PanelAgents, TierWide, kc(keyEscape), ActEscape},
		{"overlay digit ignored", ModeOverlay, PanelAgents, TierMedium, rk('1'), ActNone},
		{"overlay q closes", ModeOverlay, PanelAgents, TierWide, rk('q'), ActClose},
		{"overlay Esc closes", ModeOverlay, PanelAgents, TierWide, kc(keyEscape), ActClose},
		{"overlay Tab ignored", ModeOverlay, PanelAgents, TierWide, kc(keyTab), ActNone},
		{"overlay Y", ModeOverlay, PanelAgents, TierWide, rk('Y'), ActCopyThread},
		{"overlay i replies", ModeOverlay, PanelAgents, TierWide, rk('i'), ActComposeOpen},
		{"overlay Enter answers", ModeOverlay, PanelAgents, TierWide, kc(keyEnter), ActAsk},
		{"overlay w answers", ModeOverlay, PanelAgents, TierWide, rk('w'), ActAsk},
		{"panel ! answers", ModeShell, PanelAgents, TierWide, rk('!'), ActAsk},
		{"overlay j scrolls", ModeOverlay, PanelAgents, TierWide, rk('j'), ActDown},
		{"ask digit answers", ModeShellAsk, PanelAgents, TierWide, rk('2'), ActAskPick},
		{"ask o is Other", ModeShellAsk, PanelAgents, TierWide, rk('o'), ActAskOther},
		{"wide 1 is a tab", ModeShell, PanelAgents, TierWide, rk('1'), ActTab1},
		{"medium 3", ModeShell, PanelAgents, TierMedium, rk('3'), ActTab3},
		{"medium 4 is Plan (A11)", ModeShell, PanelAgents, TierMedium, rk('4'), ActTab4},
		{"medium 5 unbound", ModeShell, PanelAgents, TierMedium, rk('5'), ActNone},
		{"narrow 5", ModeShell, PanelAgents, TierNarrow, rk('5'), ActTab5},
		{"Tab next", ModeShell, PanelAgents, TierWide, kc(keyTab), ActNextPanel},
		{"Backtab prev", ModeShell, PanelAgents, TierWide, kc(keyBacktab), ActPrevPanel},
		{"Enter opens", ModeShell, PanelBoard, TierWide, kc(keyEnter), ActOpen},
		{"v opens", ModeShell, PanelBoard, TierWide, rk('v'), ActOpen},
		{"slash filters", ModeShell, PanelAgents, TierWide, rk('/'), ActFilter},
		{"i composes", ModeShell, PanelAgents, TierWide, rk('i'), ActCompose},
		{"colon composes", ModeShell, PanelAgents, TierWide, rk(':'), ActCompose},
		{"at composes", ModeShell, PanelAgents, TierWide, rk('@'), ActComposeAt},
		{"F1", ModeShell, PanelAgents, TierWide, kc(keyF1), ActHelp},
		{"?", ModeShell, PanelAgents, TierWide, rk('?'), ActHelp},
		{"F2", ModeShell, PanelAgents, TierWide, kc(keyF2), ActRequests},
		{"r", ModeShell, PanelAgents, TierWide, rk('r'), ActRequests},
		{"y copies", ModeShell, PanelActivity, TierWide, rk('y'), ActCopy},
		{"PgDn", ModeShell, PanelActivity, TierWide, kc(keyPgDn), ActPageDown},
		{"Home", ModeShell, PanelActivity, TierWide, kc(keyHome), ActFirst},
		{"g", ModeShell, PanelActivity, TierWide, rk('g'), ActFirst},
		{"k", ModeShell, PanelActivity, TierWide, rk('k'), ActUp},
		{"unbound x", ModeShell, PanelActivity, TierWide, rk('x'), ActNone},
		{"compose Ctrl+S sends", ModeCompose, PanelAgents, TierWide, kc(keyCtrlS), ActSend},
		{"compose Ctrl+T answers", ModeCompose, PanelAgents, TierWide, kc(keyCtrlT), ActComposeAsk},
	}
	for _, c := range cases {
		if got := lookup(c.m, c.p, c.t, c.k); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, m := range []Mode{ModeShell, ModeInput, ModeFilter, ModeOverlay, ModeShellAsk, ModeCompose, ModePick} {
		if got := lookup(m, PanelAgents, TierWide, Key{Code: keyCtrlC}); got != ActQuit {
			t.Errorf("Ctrl-C in mode %v: %v", m, got)
		}
	}
}

// --- completion_test.go ---

// From TestCompletionNames: the inputs offer the agents the panel shows
// (registered, not yourself), plus the dormant ones while a is on.
func TestShellCompletionNames(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	st.agents["bernard"] = &agentState{state: "idle", stateAt: now, registered: true}
	st.agents["old"] = &agentState{state: "idle", stateAt: now.Add(-48 * time.Hour), lastSeen: now.Add(-48 * time.Hour), registered: true}
	st.agents["ghost"] = &agentState{armed: true}
	m := shellOf(t, st, 160, 45, nil)
	if got := strings.Join(m.names(m.ui.showAll), " "); got != "coder foureyes sentinel" {
		t.Fatalf("names %q", got)
	}
	if m = keys(t, m, kTab, 'a'); strings.Join(m.names(m.ui.showAll), " ") != "coder foureyes old sentinel" {
		t.Fatalf("names with a: %q", strings.Join(m.names(m.ui.showAll), " "))
	}
}

// From TestComposeTabMovesAPrefixToTheRecipient: in the window, Tab on a text
// that is only "@prefix" makes it the recipient: completed when unique, else
// typed in to: with its list; the text is never read as a recipient.
func TestShellComposeTabMovesAPrefixToTheRecipient(t *testing.T) {
	fw := &fakeWriter{}
	m := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw), 'I', kTab) // to the text
	m = keys(t, typeText(t, m, "@co"), kTab)
	if c := m.ui.compose; c.to.value() != "@coder" || c.area.value() != "" || c.onTo {
		t.Fatalf("unique: to %q text %q onTo %v", c.to.value(), c.area.value(), c.onTo)
	}
	m, cmd := pressCmd(t, typeText(t, m, "go"), kCtrlS)
	if run(t, m, cmd); sentBy(fw) != "cmd coder go" {
		t.Fatalf("sent %q", sentBy(fw))
	}
	m2 := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, nil), 'I', kTab)
	m2 = keys(t, typeText(t, m2, "@"), kTab)
	if c := m2.ui.compose; c.to.value() != "@" || !c.onTo || c.area.value() != "" {
		t.Fatalf("ambiguous: to %q onTo %v text %q", c.to.value(), c.onTo, c.area.value())
	}
	m2 = keys(t, typeText(t, m2, "se"), kTab) // its list: Tab takes the entry
	if c := m2.ui.compose; c.to.value() != "@sentinel" || c.onTo {
		t.Fatalf("Tab in to: took %q onTo %v", c.to.value(), c.onTo)
	}
}

// --- compose_test.go ---

// From TestComposeNeverRebuildsTheRecipient: an empty recipient with a body
// starting "@coder" is a broadcast (warned once); an empty body is refused;
// an unknown recipient is refused.
func TestShellComposeNeverRebuildsTheRecipient(t *testing.T) {
	fw := &fakeWriter{}
	m := typeText(t, keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw), 'I', kTab), "@coder do it")
	m, cmd := pressCmd(t, m, kCtrlS)
	if cmd != nil || !strings.Contains(m.ui.flash, "nobody reads a broadcast") || m.ui.compose == nil {
		t.Fatalf("first Ctrl+S: flash %q", m.ui.flash)
	}
	m, cmd = pressCmd(t, m, kCtrlS)
	if run(t, m, cmd); sentBy(fw) != "notify @coder do it" {
		t.Fatalf("second Ctrl+S sent %q", sentBy(fw))
	}
	fw2 := &fakeWriter{}
	m2 := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw2), kTab)
	m2.lists.agents.Select("coder")
	m2 = keys(t, m2, 'I', kCtrlS, kCtrlS)
	if sentBy(fw2) != "" || m2.ui.flash != "nothing to send" || m2.ui.compose == nil {
		t.Fatalf("empty body: sent %q flash %q", sentBy(fw2), m2.ui.flash)
	}
	m2.ui.compose.to.set("@nobody")
	m2 = keys(t, typeText(t, m2, "hello"), kCtrlS)
	if sentBy(fw2) != "" || !strings.Contains(m2.ui.flash, "unknown recipient") {
		t.Fatalf("unknown recipient: sent %q flash %q", sentBy(fw2), m2.ui.flash)
	}
}

// From TestComposeDraftAndFailure and TestComposeAllPartialFailure: Tab never
// inserts a tab; Esc keeps the draft and i reopens the window; a failed send
// keeps the window, its text and says why, through a resize; @all partly
// served closes it and clears the draft, with none served it stays.
func TestShellComposeDraftAndFailure(t *testing.T) {
	fw := &fakeWriter{}
	m := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw), kTab)
	m.lists.agents.Select("coder")
	m = typeText(t, keys(t, m, 'I'), "keep")
	m = typeText(t, keys(t, m, kEnter), "me")
	m = keys(t, m, kTab, kTab)
	if c := m.ui.compose; strings.Contains(c.area.value(), "\t") || c.recipient() != "coder" {
		t.Fatalf("Tab: text %q recipient %q", c.area.value(), c.recipient())
	}
	if m = keys(t, m, kEsc); m.ui.compose != nil || m.mode() != ModeShell {
		t.Fatal("Esc did not close")
	}
	if m = keys(t, m, 'i'); m.mode() != ModeCompose || m.ui.compose.area.value() != "keep\nme" {
		t.Fatalf("i with a window draft: mode %v", m.mode())
	}
	fw.fail = errorf("connection refused")
	m, cmd := pressCmd(t, m, kCtrlS)
	m = run(t, m, cmd)
	if m.ui.compose == nil || m.ui.compose.area.value() != "keep\nme" || !strings.Contains(m.grid().plain(), "send failed: connection refused") {
		t.Fatalf("failure: window %v, flash %q", m.ui.compose != nil, m.ui.flash)
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 40}); m.mode() != ModeCompose || m.ui.compose.area.value() != "keep\nme" {
		t.Fatal("resize lost the window")
	}

	fw2 := &fakeWriter{failFor: map[string]bool{"foureyes": true}}
	m2 := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw2), 'I')
	m2.ui.compose.to.set("@all")
	m2 = typeText(t, keys(t, m2, kTab), "stop")
	m2, cmd = pressCmd(t, m2, kCtrlS)
	if m2 = run(t, m2, cmd); m2.ui.compose != nil || m2.composeHasDraft() || !strings.Contains(m2.ui.flash, "failed: foureyes") {
		t.Fatalf("partial: open %v draft %v flash %q", m2.ui.compose != nil, m2.composeHasDraft(), m2.ui.flash)
	}
	fw2.failFor, fw2.fail = nil, errorf("down")
	m2 = keys(t, m2, 'I')
	m2.ui.compose.to.set("@all")
	m2 = typeText(t, keys(t, m2, kTab), "again")
	m2, cmd = pressCmd(t, m2, kCtrlS)
	if m2 = run(t, m2, cmd); m2.ui.compose == nil || !m2.composeHasDraft() {
		t.Fatal("total failure closed the window")
	}
}

// From TestComposeFromTheDetailAndToAQuestion, TestAgentDetailIRepliesToThatAgent
// and TestComposeHintSurvivesMoves: i in an agent's page writes to that
// agent; after the open-question hint, Ctrl+T answers in its ask with the
// window's text, after cursor and field moves too; an edit ends the hint.
func TestShellComposeFromTheDetailAndToAQuestion(t *testing.T) {
	sh := demoShared(time.Now())
	sh.agents["coder"].openAsk = 1
	sh.asks = []bus.Ask{{ID: "1-0", From: "coder", Title: "GO", State: "open", CreatedAt: time.Now().UnixMilli(),
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "go", Header: "GO", Question: "Run it?",
			Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}}
	m := keys(t, shellOf(t, sh, 160, 45, &fakeWriter{}), kTab)
	m.lists.agents.Select("foureyes")
	m = keys(t, m, kEnter, 'i')
	if m.mode() != ModeCompose || m.ui.compose.recipient() != "foureyes" || m.rd.open() {
		t.Fatalf("i in the agent's page: mode %v to %q reader %v", m.mode(), m.ui.compose.recipient(), m.rd.open())
	}
	m = keys(t, m, kEsc)
	m.ui.msgCompose = composeDraft{}

	m.lists.agents.Select("coder")
	m = typeText(t, keys(t, m, 'I'), "yes, run")
	if m, _ = pressCmd(t, m, kCtrlS); !strings.Contains(m.ui.flash, "Ctrl+T: answer") {
		t.Fatalf("hint: flash %q", m.ui.flash)
	}
	m = keys(t, m, kLeft, kTab, kTab, kCtrlT) // moves keep the hint
	if p := m.rd.top(); p == nil || p.kind != ovAsk || m.ui.compose == nil || m.ui.compose.kind != composeAnswer || m.ui.compose.area.value() != "yes, run" {
		t.Fatalf("Ctrl+T: page %+v compose %+v", m.rd.top(), m.ui.compose)
	}

	m2 := keys(t, shellOf(t, sh, 160, 45, &fakeWriter{}), kTab)
	m2.lists.agents.Select("coder")
	m2 = typeText(t, keys(t, m2, 'I'), "go now")
	m2 = keys(t, typeText(t, keys(t, m2, kCtrlS), "!"), kCtrlT)
	if m2.rd.open() {
		t.Fatal("Ctrl+T acted on an edited text")
	}
}

// From TestLineToWindowWithAltEnter: Alt+Enter (like Ctrl+O) moves the
// line's text to the window, and the footer says how while it is written.
func TestShellLineToWindowWithAltEnter(t *testing.T) {
	m := typeText(t, keys(t, shellOf(t, demoShared(time.Now()), 160, 45, nil), 'i'), "@coder first")
	if !strings.Contains(m.grid().plain(), "^O several lines") {
		t.Fatalf("no hint on the line:\n%s", m.grid().plain())
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if m.mode() != ModeCompose || m.ui.compose.recipient() != "coder" || m.ui.compose.area.value() != "first" {
		t.Fatalf("Alt+Enter: mode %v", m.mode())
	}
}

// From TestComposeScrollbar: the window's right border is a scrollbar once
// its text overflows, plain while it fits.
func TestShellComposeScrollbar(t *testing.T) {
	m := typeText(t, keys(t, shellOf(t, demoShared(time.Now()), 160, 45, nil), 'I', kTab), "short")
	right := func(m Model) string {
		f, _ := m.composeRects()
		g := m.grid()
		var sb strings.Builder
		for y := f.Y + 1; y < f.Y+f.H-1; y++ {
			sb.WriteString(g.at(f.X+f.W-1, y).text)
		}
		return sb.String()
	}
	if got := right(m); strings.Contains(got, glyphThumb) || strings.Contains(got, GlyphDown) {
		t.Fatalf("a scrollbar on a text that fits: %q", got)
	}
	m = step(t, m, tea.PasteMsg{Content: strings.Repeat(" word", 400)})
	if got := right(m); !strings.Contains(got, glyphThumb) || !strings.HasSuffix(got, GlyphDown) {
		t.Fatalf("no scrollbar on an overflowing text: %q", got)
	}
}

// --- foryou_test.go: TestPollAndTailSetTheHint ---

func TestShellTailSetsTheHint(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"master": {State: "idle", Message: "lot 1 delivered; awaiting bernard", TS: 1},
		"coder":  {State: "idle", Message: "done", TS: 1}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{},
		health: health{ok: true}, person: "bernard"}
	pollOnce(context.Background(), f, st, 1)
	if !st.agents["master"].forYou || st.agents["coder"].forYou {
		t.Fatalf("poll: master %v coder %v", st.agents["master"].forYou, st.agents["coder"].forYou)
	}
	m := shellOf(t, st, 140, 40, nil)
	m = step(t, m, tailMsg{events: []bus.Event{
		{ID: "2-0", Kind: "status", Agent: "master", State: "working", Message: "on it"},
		{ID: "3-0", Kind: "status", Agent: "coder", State: "idle", Message: "handed to Bernard"}}})
	if st.agents["master"].forYou || !st.agents["coder"].forYou || !m.snap.agents["coder"].forYou {
		t.Fatalf("tail: master %v coder %v", st.agents["master"].forYou, st.agents["coder"].forYou)
	}
}

// --- human_test.go ---

// From TestSendToAllAgents: @all reaches every agent the panel shows.
func TestShellSendToAllAgents(t *testing.T) {
	fw := &fakeWriter{}
	m := typeLineShell(t, shellOf(t, demoShared(time.Now()), 160, 40, fw), "@all stand-up in 5 min")
	for _, a := range []string{"coder", "foureyes", "sentinel"} {
		if !strings.Contains(sentBy(fw), "cmd "+a+" stand-up in 5 min") {
			t.Fatalf("@all did not reach %s: %s", a, sentBy(fw))
		}
	}
	if !strings.Contains(m.grid().plain(), "directive sent to 3 agents") {
		t.Fatalf("footer:\n%s", m.grid().plain())
	}
}

// From TestBroadcastWarningDoesNotSurviveEsc.
func TestShellBroadcastWarningDoesNotSurviveEsc(t *testing.T) {
	fw := &fakeWriter{}
	m := typeText(t, keys(t, shellOf(t, demoShared(time.Now()), 160, 40, fw), 'i'), "hello team")
	m = keys(t, m, kEnter, kEsc, 'i') // warned, then away and back
	m, cmd := pressCmd(t, m, kEnter)
	if run(t, m, cmd); sentBy(fw) != "" {
		t.Fatalf("a broadcast was sent with one Enter after Esc: %s", sentBy(fw))
	}
}

// From TestAllWithSeveralOpenQuestionsAsksWhich: with several recipients
// holding questions, Tab asks which; every other key is swallowed; its number
// opens that ask with the text as the answer.
func TestShellAllWithSeveralOpenQuestionsAsksWhich(t *testing.T) {
	sh := demoShared(time.Now())
	q := func(from string) bus.Ask {
		return bus.Ask{ID: from + "-1", From: from, Title: "Q", State: "open", CreatedAt: time.Now().UnixMilli(),
			Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "a", Header: "A", Question: "?",
				Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}
	}
	sh.agents["coder"].openAsk, sh.agents["foureyes"].openAsk = 1, 1
	sh.asks = []bus.Ask{q("coder"), q("foureyes")}
	fw := &fakeWriter{}
	m := typeText(t, keys(t, shellOf(t, sh, 160, 40, fw), 'i'), "@all hold on")
	m = keys(t, m, kEnter, kTab)
	if !strings.Contains(m.grid().plain(), "answer whose question? 1 coder") || m.rd.open() {
		t.Fatalf("no chooser:\n%s", m.grid().plain())
	}
	m = keys(t, m, kEnter, '7') // swallowed
	if sentBy(fw) != "" || m.ui.edit != "@all hold on" {
		t.Fatalf("a key during the choice sent %q or typed %q", sentBy(fw), m.ui.edit)
	}
	m = keys(t, m, '2')
	if p := m.rd.top(); p == nil || p.kind != ovAsk || p.key != "foureyes-1" || m.ui.compose == nil || m.ui.compose.area.value() != "hold on" {
		t.Fatalf("pick 2: page %+v compose %+v", m.rd.top(), m.ui.compose)
	}
}

// --- pausepopup_test.go ---

// From TestPausePopupNotShown: a pause over, no pause, a failed pause read
// (said in the flash) and a broadcast send without the popup.
func TestShellPausePopupNotShown(t *testing.T) {
	now := time.Now()
	ended := pausedShared(now)
	ended.pause.Until = now.Add(-time.Minute).UnixMilli()
	unknown := pausedShared(now)
	unknown.pauseFailed = true
	for name, c := range map[string]struct {
		st         *shared
		line, sent string
		note       string
	}{
		"pause over":  {ended, "@coder hi", "cmd coder hi", ""},
		"not paused":  {demoShared(now), "@coder hi", "cmd coder hi", ""},
		"read failed": {unknown, "@coder hi", "cmd coder hi", "pause ?"},
		"a broadcast": {pausedShared(now), "hello all", "", ""},
	} {
		fw := &fakeWriter{}
		m := typeLineShell(t, shellOf(t, c.st, 160, 45, fw), c.line)
		if m.ui.pausePopup != nil {
			t.Errorf("%s: popup shown", name)
		}
		if sentBy(fw) != c.sent {
			t.Errorf("%s: sent %q, want %q", name, sentBy(fw), c.sent)
		}
		if c.note != "" && !strings.Contains(m.ui.flash, c.note) {
			t.Errorf("%s: flash %q lacks %q", name, m.ui.flash, c.note)
		}
	}
}

// From TestPausePopupFromTheWindowAndTheAskPanel (the window's half; the
// ask's is TestShellPausePopupOnAnAnswer): Send anyway sends and closes it.
func TestShellPausePopupFromTheWindow(t *testing.T) {
	fw := &fakeWriter{}
	m := keys(t, shellOf(t, pausedShared(time.Now()), 160, 45, fw), kTab)
	m.lists.agents.Select("coder")
	m = typeText(t, keys(t, m, 'I'), "long message")
	if m = keys(t, m, kCtrlS); m.ui.pausePopup == nil || sentBy(fw) != "" {
		t.Fatalf("window send while paused: popup %v sent %q", m.ui.pausePopup != nil, sentBy(fw))
	}
	m, cmd := pressCmd(t, m, 's')
	if m = run(t, m, cmd); sentBy(fw) != "cmd coder long message" || m.ui.compose != nil {
		t.Fatalf("window send anyway: %q open %v", sentBy(fw), m.ui.compose != nil)
	}
}

// From TestPausePopupAcknowledgesOneSendOnly: Send anyway is for that one
// send; the same text sent again later asks again.
func TestShellPausePopupAcknowledgesOneSendOnly(t *testing.T) {
	fw := &fakeWriter{}
	m := typeLineShell(t, shellOf(t, pausedShared(time.Now()), 160, 45, fw), "@coder hello")
	m, cmd := pressCmd(t, m, 's')
	if m = run(t, m, cmd); sentBy(fw) != "cmd coder hello" {
		t.Fatalf("first send: %q", sentBy(fw))
	}
	m = typeText(t, m, "@coder hello")
	if m = keys(t, m, kEnter); m.ui.pausePopup == nil || sentBy(fw) != "cmd coder hello" {
		t.Fatalf("the same text again asks again: popup %v sent %q", m.ui.pausePopup != nil, sentBy(fw))
	}
}

// --- pause_test.go ---

// From TestAllSkipsWhatOnlyReports: @all targets only registered agents.
func TestShellAllSkipsWhatOnlyReports(t *testing.T) {
	st := demoShared(time.Now())
	st.agents["conductor"] = &agentState{lastSeen: time.Now()} // reports, not in {p}:agents
	m := shellOf(t, st, 160, 45, nil)
	if names := strings.Join(m.names(false), ","); strings.Contains(names, "conductor") || !strings.Contains(names, "coder") {
		t.Fatalf("@all targets %q", names)
	}
}

// From TestAllFanOutReadsAsOneActivityLine: a person's fan-out within a
// second is one Activity line, @all (3), that each agent's filter finds.
func TestShellAllFanOutReadsAsOneActivityLine(t *testing.T) {
	m := shellOf(t, demoShared(time.Now()), 160, 45, nil)
	base := time.Now().UnixMilli()
	var evs []bus.Event
	for i, a := range []string{"coder", "foureyes", "master"} {
		evs = append(evs, bus.Event{ID: fmt.Sprintf("%d-0", base+int64(i)), Kind: "cmd", Type: bus.CmdDirective,
			From: "bernard", Target: a, Message: "end of session", Human: true})
	}
	evs = append(evs, bus.Event{ID: fmt.Sprintf("%d-0", base+5), Kind: "cmd", Type: bus.CmdDirective,
		From: "bernard", Target: "coder", Message: "another thing", Human: true})
	m = step(t, m, tailMsg{events: evs})
	var events []feedLine
	for _, fl := range m.feed.feed {
		if !fl.sep {
			events = append(events, fl)
		}
	}
	if len(events) != 2 || len(events[0].to) != 3 {
		t.Fatalf("feed %d lines, first to %v", len(events), events[0].to)
	}
	if row := activityRow(events[0], TierWide, 140).text(); !strings.Contains(row, "@all (3)") || strings.Count(row, "end of session") != 1 {
		t.Fatalf("row %q", row)
	}
	if !parseFilter("@master").match(events[0]) {
		t.Fatal("@master filter misses the fan-out line")
	}
}

// --- quota_test.go: TestAgentOverlayShowsOneSharedAccountRowWithBothResets ---

func TestShellAgentPageShowsOneSharedAccountRowWithBothResets(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	st.agents["coder"].provider = "anthropic"
	st.budgets["anthropic"] = bus.BudgetSnapshot{Windows: []bus.BudgetWindow{
		{Name: "weekly", UsedPct: 76, ResetsAt: now.Add(72 * time.Hour).Unix(), Minutes: 10080, ObservedAt: now.UnixMilli()},
		{Name: "session", UsedPct: 6, ResetsAt: now.Add(time.Hour).Unix(), Minutes: 300, ObservedAt: now.UnixMilli()},
	}}
	m := shellOf(t, st, 160, 45, nil)
	m.pushAgent("coder")
	body := linesPlain(m.rd.top().body)
	if got := strings.Count(body, "Account  claude:"); got != 1 {
		t.Fatalf("account rows = %d, want one:\n%s", got, body)
	}
	for _, want := range []string{"5h 6% (resets ", "7d 76% (resets ", "shared by every agent using this provider",
		"Account  claude: 5h 6%", " · 7d 76%"} {
		if !strings.Contains(body, want) {
			t.Fatalf("agent page lacks %q:\n%s", want, body)
		}
	}
}

// --- remove_test.go: TestDeleteClosesAnOpenThreadWithAReply ---

func TestShellDeleteClosesAnOpenThreadWithAReply(t *testing.T) {
	st := dormantShared(time.Now())
	id := fmt.Sprintf("%d-0", time.Now().Add(-time.Hour).UnixMilli())
	st.cmdHistory = []bus.Event{{Kind: "cmd", ID: id, Type: "directive", From: "master", Target: "claude-hl-coder", Message: "x"}}
	fw := &fakeWriter{}
	m := keys(t, shellOf(t, st, 160, 45, fw), '3') // Requests
	m.lists.requests.Select("thr:" + id)
	if m.lists.requests.Selected() != "thr:"+id {
		t.Fatalf("no row for the thread")
	}
	m, cmd := pressCmd(t, keys(t, m, 'd'), 'y')
	m = run(t, m, cmd)
	if sentBy(fw) != "close master "+id {
		t.Fatalf("sent %q", sentBy(fw))
	}
	for _, it := range m.lists.requests.items {
		if it.Key == "thr:"+id {
			t.Fatal("the closed thread is still listed")
		}
	}
}

// --- ui_test.go ---

// From TestTooSmallRoundTrip: a reader page survives a too-small terminal.
func TestShellTooSmallRoundTrip(t *testing.T) {
	m := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, nil), tea.Key{Code: tea.KeyF1})
	if m.rd.top() == nil || m.rd.top().kind != ovHelp {
		t.Fatal("help did not open")
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 20, Height: 5}); !strings.Contains(m.grid().plain(), "too small") {
		t.Fatalf("too small screen:\n%s", m.grid().plain())
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	if m.rd.top() == nil || m.rd.top().kind != ovHelp || !strings.Contains(m.grid().plain(), "KEYS") {
		t.Fatalf("help lost after the round trip:\n%s", m.grid().plain())
	}
}

// From TestFocusSurvivesTierChange: the focused tab keeps the focus through
// the medium and narrow tiers and back.
func TestShellFocusSurvivesTierChange(t *testing.T) {
	m := keys(t, shellOf(t, demoShared(time.Now()), 160, 45, nil), '2') // Board
	if m.ui.focus != PanelBoard {
		t.Fatalf("focus %v, want Board", m.ui.focus)
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30}); m.layout.Tier != TierMedium || m.layout.Active != PanelBoard || m.ui.focus != PanelBoard {
		t.Fatalf("medium: active %v focus %v", m.layout.Active, m.ui.focus)
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 64, Height: 22}); m.layout.Tier != TierNarrow || m.layout.Visible[0] != PanelBoard {
		t.Fatalf("narrow shows %v", m.layout.Visible)
	}
	if m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 45}); m.ui.focus != PanelBoard {
		t.Fatal("focus lost back in wide")
	}
}

// From TestEscLadder: Esc clears the selection (still paused), then resumes
// auto-scroll, then clears the filter, and never quits.
func TestShellEscLadder(t *testing.T) {
	m := shellOf(t, demoShared(time.Now()), 160, 45, nil)
	m = step(t, m, tailMsg{events: feedEvents()})
	m.ui.filter = parseFilter("tests")
	m = keys(t, m, '1', kUp) // Activity; select the newest, still live
	act := &m.lists.activity
	if act.Selected() == "" || !act.Live() {
		t.Fatalf("Up: sel %q live %v", act.Selected(), act.Live())
	}
	if m = keys(t, m, 'f'); act.Selected() == "" || act.Live() {
		t.Fatalf("f: sel %q live %v", act.Selected(), act.Live())
	}
	if m = keys(t, m, kEsc); act.Selected() != "" || act.Live() {
		t.Fatalf("Esc 1: sel %q live %v", act.Selected(), act.Live())
	}
	if m = keys(t, m, kEsc); !act.Live() || m.ui.filter.empty() {
		t.Fatalf("Esc 2: live %v filter %q", act.Live(), m.ui.filter.raw)
	}
	if m = keys(t, m, kEsc); !m.ui.filter.empty() {
		t.Fatal("Esc 3 kept the filter")
	}
	if _, cmd := pressCmd(t, m, kEsc); cmd != nil {
		t.Fatal("Esc with nothing left did something")
	}
}

// From TestDraftsSeparate: the message line's draft and the filter edit are
// separate; a broadcast needs its second Enter; a directive goes at once.
func TestShellDraftsSeparate(t *testing.T) {
	fw := &fakeWriter{}
	m := typeText(t, keys(t, shellOf(t, demoShared(time.Now()), 160, 45, fw), 'i'), "hello q1")
	if m.ui.edit != "hello q1" || m.mode() != ModeInput {
		t.Fatalf("typing: %q mode %v", m.ui.edit, m.mode())
	}
	if m = keys(t, m, kEsc); m.mode() != ModeShell || m.ui.msgDraft != "hello q1" {
		t.Fatalf("Esc: mode %v draft %q", m.mode(), m.ui.msgDraft)
	}
	m = keys(t, typeText(t, keys(t, m, '/'), "xyz"), kEsc) // cancel the filter edit
	if !m.ui.filter.empty() || m.ui.focus != PanelActivity {
		t.Fatalf("filter cancel: %q focus %v", m.ui.filter.raw, m.ui.focus)
	}
	if m = keys(t, m, 'i'); m.ui.edit != "hello q1" {
		t.Fatalf("message draft lost: %q", m.ui.edit)
	}
	m, cmd := pressCmd(t, keys(t, m, kEnter), kEnter)
	if m = run(t, m, cmd); sentBy(fw) != "notify hello q1" {
		t.Fatalf("sent %q", sentBy(fw))
	}
	m, cmd = pressCmd(t, typeText(t, m, "@coder go"), kEnter)
	if run(t, m, cmd); sentBy(fw) != "notify hello q1|cmd coder go" {
		t.Fatalf("directive: %q", sentBy(fw))
	}
}

// --- unread_test.go ---

// From TestReportOfAWaitingAgentIsForThePerson: an agent's newest report while
// it waits for the person is for the person.
func TestShellReportOfAWaitingAgentIsForThePerson(t *testing.T) {
	st := demoShared(time.Now())
	m := shellOf(t, st, 160, 45, nil)
	m = step(t, m, tailMsg{events: []bus.Event{
		{ID: "100-0", Kind: "report", Agent: "foureyes", Message: "older"},
		{ID: "101-0", Kind: "report", Agent: "foureyes", Message: "please decide"},
		{ID: "102-0", Kind: "report", Agent: "coder", Message: "not waiting"},
		{ID: "103-0", Kind: "status", Agent: "foureyes", State: "waiting", Message: "decide"}}})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, time.Now()), at: time.Now()})
	if u := m.feed.unread; !u.forPerson["101-0"] || u.forPerson["100-0"] || u.forPerson["102-0"] {
		t.Fatalf("classified %v", u.forPerson)
	}
}

// From TestOpeningAMarkedLineReadsIt (step c's carried item 1): opening a
// marked line reads it, and the cache keeps it (0600), with only the ids
// still in the retained window; its save runs in a command.
func TestShellOpeningAMarkedLineReadsItAndSavesTheCache(t *testing.T) {
	dir := t.TempDir()
	cfg := shellConfig{project: "demo", self: "bernard", st: demoShared(time.Now()),
		cache: &busmonCache{Project: "demo", Read: []string{"1-0"}}, cachePath: filepath.Join(dir, "busmon", "demo.json")}
	m := newShell(cfg)
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m = step(t, m, tailMsg{events: []bus.Event{{ID: "200-0", Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "bernard", Message: "look"}}})
	if !m.feed.unread.forPerson["200-0"] || m.feed.feed[len(m.feed.feed)-1].mark != markUnread {
		t.Fatal("a cmd to the person is not marked unread")
	}
	m = keys(t, m, '1')
	m.lists.activity.Select(feedKey(t, m, "200-0"))
	m, _ = pressCmd(t, m, kEnter)    // expanded: not read yet
	m, cmd := pressCmd(t, m, kEnter) // the reader: read, the save in a command
	if !slices.Equal(m.sig.cache.Read, []string{"200-0"}) {
		t.Fatalf("cache read %v (1-0 is no longer retained)", m.sig.cache.Read)
	}
	if _, err := os.Stat(cfg.cachePath); err == nil {
		t.Fatal("the cache was written in Update")
	}
	m = run(t, m, cmd)
	info, err := os.Stat(cfg.cachePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %v %v", info, err)
	}
	if loaded, _ := loadBusmonCache(cfg.cachePath, "demo"); !slices.Equal(loaded.Read, []string{"200-0"}) {
		t.Fatalf("saved read marks %v", loaded.Read)
	}
	if m = keys(t, m, kEsc); m.feed.feed[len(m.feed.feed)-1].mark != markNone {
		t.Fatal("still marked after opening")
	}
	// A new shell on that cache starts with the line read.
	again := newShell(shellConfig{project: "demo", self: "bernard", cache: &busmonCache{Read: []string{"200-0"}}})
	if !again.feed.unread.read["200-0"] {
		t.Fatal("the read marks are not loaded at start")
	}
}

// --- askpanel_test.go ---

// From TestAskPanelStaysOnARefusal: a refused answer keeps the page and says why.
func TestShellAskStaysOnARefusal(t *testing.T) {
	fw := &fakeWriter{fail: errorf("ask refused: this question is already answered")}
	m := keys(t, shellOf(t, askShared(time.Now()), 160, 45, fw), '!')
	m, cmd := pressCmd(t, m, kEnter)
	if m = run(t, m, cmd); m.rd.top() == nil || m.rd.top().kind != ovAsk || !strings.Contains(m.ui.flash, "already answered") {
		t.Fatalf("page %+v flash %q: a refusal must keep the page and say why", m.rd.top(), m.ui.flash)
	}
}

// From TestAskPanelFollowsAReplacement: an answer that lands on a replacement
// moves the page to it, on its next open question.
func TestShellAskFollowsAReplacement(t *testing.T) {
	st := askShared(time.Now())
	fw := &fakeWriter{answerID: "200-0", left: 1}
	m := keys(t, shellOf(t, st, 160, 45, fw), '!')
	old, repl := demoAsk(), demoAsk()
	repl.ID = "200-0"
	repl.Questions[1].Answer = &bus.AskAnswer{Choice: "Mon 10:00", By: "bernard"}
	old.State, old.ReplacedBy = "replaced", "200-0"
	m, cmd := pressCmd(t, m, kEnter)
	st.asks = []bus.Ask{old, repl} // the poll brings the replacement
	m = step(t, m, pollMsg{snap: takeSnapshot(st, time.Now()), at: time.Now()})
	m = run(t, m, cmd)
	if p := m.rd.top(); p == nil || p.kind != ovAsk || p.ask.id != "200-0" || p.ask.q != 2 {
		t.Fatalf("page %+v: must follow the replacement to its open question", m.rd.top())
	}
}

// From TestYCopiesTheQuestion.
func TestShellYCopiesTheQuestion(t *testing.T) {
	m := keys(t, shellOf(t, askShared(time.Now()), 160, 45, nil), '!')
	if p := m.rd.top(); !strings.Contains(p.plain, "When should it open?") || !strings.Contains(p.plain, "1. Mon 10:00") {
		t.Fatalf("copy buffer %q", p.plain)
	}
	m, cmd := pressCmd(t, m, 'y')
	if cmd == nil || m.rd.top().kind != ovAsk || !strings.Contains(m.ui.flash, "question copied") {
		t.Fatalf("y: flash %q", m.ui.flash)
	}
}

// From TestBellRingsOncePerAskNotOnItsCompletion: a waiting declaration
// rings, its ask rings, the ask's completion does not.
func TestShellBellOncePerAskNotOnItsCompletion(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	m := newShell(shellConfig{project: "demo", self: "bernard", st: st, settings: busmonSettings{Bell: true}})
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	poll := func(f func(a *agentState)) {
		a := *st.agents["master"]
		f(&a)
		st.agents["master"] = &a
		m = step(t, m, pollMsg{snap: takeSnapshot(st, now), at: now})
	}
	st.agents["master"] = &agentState{state: "waiting", stateAt: now, registered: true}
	poll(func(*agentState) {}) // the waiting declaration
	poll(func(a *agentState) { a.askID, a.openAsk = "100-0", 2 })
	poll(func(*agentState) {})
	poll(func(a *agentState) { a.askID, a.openAsk = "", 0 }) // answered: the same declaration
	if m.sig.bells != 2 {
		t.Fatalf("bells %d, want 2 (the waiting, the ask; not the completion)", m.sig.bells)
	}
}

// --- reminder_test.go: TestReminderWaitsForComposeDraftAndOtherModal ---

// The reminder waits for a line draft and a reader page (the compose window
// is TestShellReminder's), then opens once they clear.
func TestShellReminderWaitsForADraftAndAPage(t *testing.T) {
	now := time.Now()
	st := &shared{agents: map[string]*agentState{}, asks: []bus.Ask{{ID: "1-0", From: "coder", State: "open", Title: "Review?",
		CreatedAt: now.Add(-10 * time.Minute).UnixMilli(), Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1", Question: "Review?"}}}}}}
	m := newShell(shellConfig{project: "demo", self: "bernard", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m.ui.msgDraft = "draft"
	m.cfg.settings = busmonSettings{PopupAfter: time.Minute, Snooze: time.Hour}
	if m = step(t, m, pollMsg{snap: takeSnapshot(st, now), at: now}); m.ui.reminder != nil {
		t.Fatal("reminder covered a line draft")
	}
	m.ui.msgDraft = ""
	m = keys(t, m, '?') // a page
	if m.ui.reminder != nil {
		t.Fatal("reminder covered a reader page")
	}
	if m = keys(t, m, kEsc); m.ui.reminder == nil {
		t.Fatal("the reminder did not open once the page closed")
	}
}

func errorf(s string) error { return errors.New(s) }

// An answer that lands on a replacement before the poll brings it keeps the
// ask page open; the next poll moves the page to the replacement (foureyes'
// review of step c: the order TestShellAskFollowsAReplacement does not take).
func TestShellAskStaysOpenUntilTheReplacementArrives(t *testing.T) {
	st := askShared(time.Now())
	m := keys(t, shellOf(t, st, 160, 45, &fakeWriter{answerID: "200-0", left: 1}), '!')
	m, cmd := pressCmd(t, m, kEnter)
	if m = run(t, m, cmd); m.rd.top() == nil || m.rd.top().kind != ovAsk {
		t.Fatal("the page closed when the answer landed before the poll")
	}
	old, repl := demoAsk(), demoAsk()
	repl.ID = "200-0"
	old.State, old.ReplacedBy = "replaced", "200-0"
	st.asks = []bus.Ask{old, repl}
	m = step(t, m, pollMsg{snap: takeSnapshot(st, time.Now()), at: time.Now()})
	if p := m.rd.top(); p == nil || p.kind != ovAsk || p.ask.id != "200-0" {
		t.Fatalf("after the poll the page is %+v, want the replacement 200-0", m.rd.top())
	}
}
