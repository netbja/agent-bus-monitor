package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var kCtrlP = tea.Key{Code: 'p', Mod: tea.ModCtrl}

// palShell is the fixture shell on a fake writer, the pause off.
func palShell(t *testing.T) (Model, *fakeWriter) {
	t.Helper()
	w := &fakeWriter{}
	return shellWith(t, 140, 40, shellConfig{w: w}, false), w
}

// palette types a command line in the input (i, then the text), and its keys.
func palette(t *testing.T, m Model, line string, then ...any) Model {
	t.Helper()
	m = keys(t, m, 'i')
	m = typeText(t, m, line)
	return keys(t, m, then...)
}

func palTop(m Model) (overlayKind, bool) {
	if p := m.rd.top(); p != nil {
		return p.kind, true
	}
	return 0, false
}

// /req + Enter opens Requests as F2 does (the same route, runPanelAct).
func TestPaletteRunsAClassARowAsItsKey(t *testing.T) {
	for line, key := range map[string]any{"/req": tea.Key{Code: tea.KeyF2}, "/health": tea.Key{Code: tea.KeyF5},
		"/f1": tea.Key{Code: tea.KeyF1}, "/history": 'H', "/about": 'A', "/tokens": 'U', "/R": 'R'} {
		m, _ := palShell(t)
		viaKey, _ := palShell(t)
		m = palette(t, m, line, kEnter)
		viaKey = keys(t, viaKey, key)
		got, gotOK := palTop(m)
		want, wantOK := palTop(viaKey)
		if got != want || gotOK != wantOK || m.ui.line != lineNone {
			t.Errorf("%s: page %v %v, the key's %v %v; line %v", line, got, gotOK, want, wantOK, m.ui.line)
		}
	}
	m, _ := palShell(t)
	m = keys(t, m, 'i')
	m = typeText(t, m, "/quit")
	if _, cmd := m.Update(tea.KeyPressMsg(kEnter)); cmd == nil {
		t.Fatal("/quit returns no command (tea.Quit)")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("/quit is not tea.Quit")
	}
}

// The Enter rule (§4.3): the typed line when ↑↓ did not move; the chosen row
// when they did; typing again makes the typed line count again.
func TestPaletteEnterRule(t *testing.T) {
	m, _ := palShell(t)
	if m = palette(t, m, "/r", kEnter); func() bool { k, ok := palTop(m); return !ok || k != ovRequests }() {
		t.Fatalf("/r + Enter: %v", m.rd.top())
	}
	second := paletteTable[paletteMatch("r")[1]].Name
	m, _ = palShell(t)
	m = palette(t, m, "/r", kDown)
	want := keys(t, m, kEnter)
	viaName, _ := palShell(t)
	viaName = palette(t, viaName, "/"+second, kEnter)
	k1, ok1 := palTop(want)
	k2, ok2 := palTop(viaName)
	if k1 != k2 || ok1 != ok2 {
		t.Fatalf("/r ↓ Enter ran %v, the second row %s runs %v", k1, second, k2)
	}
	m, _ = palShell(t)
	m = palette(t, m, "/r", kDown, 'e', kEnter) // /re: no exact name; the top row (requests)
	if k, ok := palTop(m); !ok || k != ovRequests {
		t.Fatalf("/r ↓ e Enter: %v", m.rd.top())
	}
}

// Esc hides the list and keeps the text; Esc again leaves the line.
func TestPaletteEscKeepsTheText(t *testing.T) {
	m, _ := palShell(t)
	m = palette(t, m, "/hea", kEsc)
	if m.ui.line != lineMessage || m.ui.edit != "/hea" || m.paletteListOn() {
		t.Fatalf("Esc: line %v %q list %v", m.ui.line, m.ui.edit, m.paletteListOn())
	}
	m = keys(t, m, 'l')
	if !m.paletteListOn() {
		t.Fatal("an edit does not show the list again")
	}
	m = keys(t, m, kEsc, kEsc)
	if m.ui.line != lineNone {
		t.Fatal("Esc twice keeps the line")
	}
}

// Ctrl+P opens the line with / from the panels and from a page; a command
// run from a page closes it and shows its result (§4.1).
func TestPaletteCtrlPFromAPage(t *testing.T) {
	m, _ := palShell(t)
	m = keys(t, m, kCtrlP)
	if m.ui.line != lineMessage || m.ui.edit != "/" || !m.paletteListOn() {
		t.Fatalf("Ctrl+P on the panels: %v %q", m.ui.line, m.ui.edit)
	}
	if !strings.Contains(m.grid().plain(), "/requests") {
		t.Fatal("the list is not drawn")
	}
	m, _ = palShell(t)
	m = keys(t, m, tea.Key{Code: tea.KeyF5}, kCtrlP)
	if k, ok := palTop(m); !ok || k != ovHealth || m.ui.edit != "/" {
		t.Fatalf("Ctrl+P on a page: page %v, edit %q", m.rd.top(), m.ui.edit)
	}
	if !strings.Contains(m.grid().plain(), "/requests") {
		t.Fatal("the list is not drawn over the page")
	}
	m = typeText(t, m, "filter coder")
	m = keys(t, m, kEnter)
	if m.rd.open() || m.ui.focus != PanelActivity || m.ui.filter.raw != "coder" {
		t.Fatalf("/filter coder from a page: page %v focus %v filter %+v", m.rd.top(), m.ui.focus, m.ui.filter)
	}
}

// Ctrl+P is ignored while the compose window has the keyboard.
func TestPaletteCtrlPNotInCompose(t *testing.T) {
	m, _ := palShell(t)
	m = keys(t, m, 'I', kCtrlP)
	if m.ui.compose == nil || m.ui.line != lineNone {
		t.Fatalf("Ctrl+P in the compose window: compose %v line %v", m.ui.compose != nil, m.ui.line)
	}
}

// Class B: /write sends as @agent text does; /master is a directive to
// master; // sends a literal / (no palette).
func TestPaletteWrites(t *testing.T) {
	m, w := palShell(t)
	m = palette(t, m, "/write coder please rebase")
	m = run(t, m, func() tea.Cmd { _, c := m.Update(tea.KeyPressMsg(kEnter)); return c }())
	if !slices.Contains(w.sent, "cmd coder please rebase") {
		t.Fatalf("/write sent %v", w.sent)
	}
	// /master goes through the line: master has an open question in the
	// fixture, so the first Enter holds it as @master text does, and a
	// second Enter sends it.
	m, w = palShell(t)
	m = palette(t, m, "/master take the next item", kEnter)
	if m.ui.edit != "@master take the next item" || !strings.Contains(m.ui.flash, "open question") || len(w.sent) != 0 {
		t.Fatalf("/master held: edit %q flash %q sent %v", m.ui.edit, m.ui.flash, w.sent)
	}
	next, cmd := m.Update(tea.KeyPressMsg(kEnter))
	m = run(t, next.(Model), cmd)
	if !slices.Contains(w.sent, "cmd master take the next item") {
		t.Fatalf("/master sent %v", w.sent)
	}
	m, _ = palShell(t)
	m = palette(t, m, "//etc/hosts is fine")
	if m.paletteOn() || m.paletteListOn() {
		t.Fatal("// opened the palette")
	}
	m, w = palShell(t)
	m = palette(t, m, "/write Coder hi", kEnter)
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "not an agent name") {
		t.Fatalf("a bad argument: sent %v flash %q", w.sent, m.ui.flash)
	}
}

// Tab completes the chosen name, then an argument.
func TestPaletteTab(t *testing.T) {
	m, _ := palShell(t)
	m = palette(t, m, "/wri", kTab)
	if m.ui.edit != "/write " {
		t.Fatalf("Tab on the name: %q", m.ui.edit)
	}
	m = typeText(t, m, "cod")
	m = keys(t, m, kTab)
	if m.ui.edit != "/write coder " {
		t.Fatalf("Tab on the agent: %q", m.ui.edit)
	}
}

// /set <key> opens the settings page on that key.
func TestPaletteSetJumpsToTheKey(t *testing.T) {
	m, _ := palShell(t)
	m.cfg.getenv = settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	m = palette(t, m, "/set notify.quiet")
	next, cmd := m.Update(tea.KeyPressMsg(kEnter))
	m = run(t, next.(Model), cmd)
	p := m.rd.top()
	if p == nil || p.kind != ovSettings {
		t.Fatalf("/set: page %v", p)
	}
	rows := p.set.rows()
	if !p.set.keys || p.sel >= len(rows) || rows[p.sel].Key != "notify.quiet" {
		t.Fatalf("/set notify.quiet: keys %v sel %d of %d", p.set.keys, p.sel, len(rows))
	}
}

// The help page lists the commands from the same table.
func TestPaletteInTheHelp(t *testing.T) {
	text := plainOf(helpText())
	for _, want := range []string{"COMMANDS", "/requests", "/write <agent> <text>", "/set [key]", "Ctrl+P"} {
		if !strings.Contains(text, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// // sends a literal / (§4.3): the line's text starts with one /.
func TestPaletteDoubleSlashSendsASlash(t *testing.T) {
	m, w := palShell(t)
	m = palette(t, m, "//etc/hosts")
	for i := 0; i < 2; i++ { // a broadcast: Enter twice, the send run
		next, cmd := m.Update(tea.KeyPressMsg(kEnter))
		m = run(t, next.(Model), cmd)
	}
	if !slices.Contains(w.sent, "notify /etc/hosts") {
		t.Fatalf("// sent %v (flash %q)", w.sent, m.ui.flash)
	}
}

// A message draft kept for i survives a command run from Ctrl+P.
func TestPaletteKeepsTheMessageDraft(t *testing.T) {
	m, _ := palShell(t)
	m = keys(t, m, 'i')
	m = typeText(t, m, "@coder half a message")
	m = keys(t, m, kEsc, kCtrlP)
	m = typeText(t, m, "health")
	m = keys(t, m, kEnter, kEsc, 'i')
	if m.ui.edit != "@coder half a message" {
		t.Fatalf("the draft after a command: %q", m.ui.edit)
	}
}
