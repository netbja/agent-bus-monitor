package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// i opens the line; "@agent body" and Enter send a directive in a command,
// whose result clears the line; Esc keeps a draft that i brings back.
func TestShellLineSendsADirective(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, false)
	m = typeText(t, keys(t, m, 'i'), "@coder review lot 2")
	if m.mode() != ModeInput || !strings.Contains(m.grid().plain(), GlyphPrompt+" @coder review lot 2") {
		t.Fatalf("the line does not show its text (mode %d)", m.mode())
	}
	m, cmd := pressCmd(t, m, kEnter)
	if cmd == nil || len(fw.sent) != 0 {
		t.Fatal("Enter did not hand the send to a command (or sent in Update)")
	}
	if _, again := pressCmd(t, m, kEnter); again != nil {
		t.Fatal("a second Enter while the send is in flight sent again")
	}
	m = run(t, m, cmd)
	if want := []string{"cmd coder review lot 2"}; !slices.Equal(fw.sent, want) {
		t.Fatalf("sent %q, want %q", fw.sent, want)
	}
	if m.ui.edit != "" || m.ui.flash != "directive sent to coder" {
		t.Fatalf("after the send: line %q, flash %q", m.ui.edit, m.ui.flash)
	}
	m = typeText(t, m, "@docs later")
	m = keys(t, m, kEsc)
	if m.mode() != ModeShell || m.ui.msgDraft != "@docs later" {
		t.Fatalf("Esc: mode %d, draft %q", m.mode(), m.ui.msgDraft)
	}
	if m = keys(t, m, 'i'); m.ui.edit != "@docs later" {
		t.Fatalf("i did not bring the draft back: %q", m.ui.edit)
	}
}

// Nobody reads a broadcast: the first Enter warns, the second sends.
func TestShellLineBroadcastNeedsASecondEnter(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'i'), "hello all")
	m, cmd := pressCmd(t, m, kEnter)
	if cmd != nil || !strings.Contains(m.ui.flash, "nobody reads a broadcast") {
		t.Fatalf("first Enter: cmd %v, flash %q", cmd != nil, m.ui.flash)
	}
	m, cmd = pressCmd(t, m, kEnter)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"notify hello all"}) || m.ui.flash != "broadcast sent" {
		t.Fatalf("second Enter: sent %q, flash %q", fw.sent, m.ui.flash)
	}
}

// A failed send keeps the line for a retry; @all partly served clears it (a
// retry would send twice) and names who failed.
func TestShellLineSendFailures(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{fail: errors.New("bus down")}
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'i'), "@coder retry me")
	m, cmd := pressCmd(t, m, kEnter)
	if m = run(t, m, cmd); m.ui.edit != "@coder retry me" || !m.ui.flashErr || !strings.Contains(m.ui.flash, "send failed: bus down") {
		t.Fatalf("failure: line %q, flash %q", m.ui.edit, m.ui.flash)
	}
	fw.fail, fw.failFor = nil, map[string]bool{"docs": true}
	m.ui.edit = "@all stand up"
	m, cmd = pressCmd(t, m, kEnter)
	if cmd == nil { // the open ask's hint first (master holds one), then send
		m, cmd = pressCmd(t, m, kEnter)
	}
	m = run(t, m, cmd)
	if m.ui.edit != "" || !strings.Contains(m.ui.flash, "failed: docs") {
		t.Fatalf("partial: line %q, flash %q", m.ui.edit, m.ui.flash)
	}
}

// "@prefix" lists its completions over the line; the arrows move, Tab takes.
func TestShellLineCompletes(t *testing.T) {
	pinLocal(t)
	m := keys(t, shellWith(t, 140, 40, shellConfig{}, false), '@')
	if m.ui.edit != "@" || len(m.completions()) < 3 {
		t.Fatalf("@: line %q, completions %q", m.ui.edit, m.completions())
	}
	m = typeText(t, m, "co")
	if got := m.completions(); !slices.Equal(got, []string{"@coder "}) || !strings.Contains(m.grid().plain(), " @coder ") {
		t.Fatalf("@co: completions %q", got)
	}
	if m = keys(t, m, kTab); m.ui.edit != "@coder " || len(m.completions()) != 0 {
		t.Fatalf("Tab: line %q", m.ui.edit)
	}
	m.ui.edit = "@"
	m = keys(t, m, kDown, kDown)
	want := m.completions()[2]
	if m = keys(t, m, kEnter); m.ui.edit != want {
		t.Fatalf("down down Enter took %q, want %q", m.ui.edit, want)
	}
}

// A directive to an agent with an open ask is held once with a hint: Enter
// again sends it as a message, Tab opens its ask with the text as a free
// answer in the compose window.
func TestShellLineHeldForAnOpenAsk(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	base := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'i'), "@master yes, merge it")
	m, cmd := pressCmd(t, base, kEnter)
	if cmd != nil || !strings.Contains(m.ui.flash, "master has an open question") {
		t.Fatalf("held: cmd %v, flash %q", cmd != nil, m.ui.flash)
	}
	// Enter again: sent as a message.
	sent, cmd := pressCmd(t, m, kEnter)
	if sent = run(t, sent, cmd); !slices.Equal(fw.sent, []string{"cmd master yes, merge it"}) {
		t.Fatalf("Enter again sent %q", fw.sent)
	}
	// Tab: its ask, the text as the answer being written.
	m = keys(t, m, kTab)
	c := m.ui.compose
	if p := m.rd.top(); p == nil || p.kind != ovAsk || c == nil || c.kind != composeAnswer || c.area.value() != "yes, merge it" || c.agent != "master" {
		t.Fatalf("Tab: page %+v, compose %+v", m.rd.top(), c)
	}
	if m.ui.line != lineNone || m.ui.msgDraft != "@master yes, merge it" {
		t.Fatalf("the line was not left with its draft: %d %q", m.ui.line, m.ui.msgDraft)
	}
}

// pauseButton is the screen cell of the pause popup's button i.
func pauseButton(t testing.TB, m Model, i int) (int, int) {
	t.Helper()
	d := m.ui.pausePopup
	b := drawPausePopup(newGrid(m.width, m.height), m.pauseDialogRect(), d.rec, d.sel, m.now)[i]
	if b.W == 0 {
		t.Fatalf("button %d not drawn", i)
	}
	return b.X + 1, b.Y
}

// While the project is paused a waking send asks first: Cancel sends
// nothing, Send anyway sends it once, Resume now resumes then sends; each by
// key and by click.
func TestShellPausePopup(t *testing.T) {
	pinLocal(t)
	for _, c := range []struct {
		name string
		do   func(Model) (Model, tea.Cmd)
		want []string
	}{
		{"esc", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, kEsc) }, nil},
		{"c", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, 'c') }, nil},
		{"click cancel", func(m Model) (Model, tea.Cmd) { x, y := pauseButton(t, m, 2); return clickCmd(t, m, x, y) }, nil},
		{"s", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, 's') }, []string{"cmd coder go"}},
		{"click send", func(m Model) (Model, tea.Cmd) { x, y := pauseButton(t, m, 1); return clickCmd(t, m, x, y) }, []string{"cmd coder go"}},
		{"enter on send", func(m Model) (Model, tea.Cmd) { return pressCmd(t, keys(t, m, kBacktab), kEnter) }, []string{"cmd coder go"}},
		{"r", func(m Model) (Model, tea.Cmd) { return pressCmd(t, m, 'r') }, []string{"resume bernard", "cmd coder go"}},
		{"click resume", func(m Model) (Model, tea.Cmd) { x, y := pauseButton(t, m, 0); return clickCmd(t, m, x, y) }, []string{"resume bernard", "cmd coder go"}},
	} {
		fw := &fakeWriter{}
		m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, true), 'i'), "@coder go")
		m, cmd := pressCmd(t, m, kEnter)
		if cmd != nil || m.ui.pausePopup == nil || !strings.Contains(m.grid().plain(), "Project paused until") {
			t.Fatalf("%s: no popup before the send", c.name)
		}
		m, cmd = c.do(m)
		for cmd != nil { // resume, then the send it replays
			msg := cmd()
			var next tea.Model
			next, cmd = m.Update(msg)
			m = next.(Model)
		}
		if !slices.Equal(fw.sent, c.want) || m.ui.pausePopup != nil {
			t.Errorf("%s: sent %q, want %q (popup open %v)", c.name, fw.sent, c.want, m.ui.pausePopup != nil)
		}
		if c.want == nil && m.ui.edit != "@coder go" {
			t.Errorf("%s: the cancelled text left the line: %q", c.name, m.ui.edit)
		}
	}
}

// An ask's answer while paused asks first too, and Send anyway answers.
func TestShellPausePopupOnAnAnswer(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, true)
	id := m.snap.asks[0].ID
	m, cmd := pressCmd(t, keys(t, m, '!'), '2')
	if cmd != nil || m.ui.pausePopup == nil {
		t.Fatal("no popup before the answer")
	}
	m, cmd = pressCmd(t, m, 's')
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"answer " + id + " go no"}) {
		t.Fatalf("sent %q", fw.sent)
	}
}

// d then y forgets an agent that is not live (in a command, then it leaves
// the panel); any other key keeps it; a live agent is refused at once.
func TestShellRemoveAgent(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := keys(t, shellWith(t, 140, 60, shellConfig{w: fw}, false), kTab)
	m.ui.agentsTouched = true
	m.lists.agents.Select("architect")
	m = keys(t, m, 'd')
	if m.ui.confirm == nil || !strings.Contains(m.ui.flash, "forget agent architect") {
		t.Fatalf("d: %q", m.ui.flash)
	}
	if m = keys(t, m, 'n'); m.ui.flash != "not removed" || len(fw.sent) != 0 {
		t.Fatalf("n: %q, sent %q", m.ui.flash, fw.sent)
	}
	m, cmd := pressCmd(t, keys(t, m, 'd'), 'y')
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"forget architect"}) || m.snap.agents["architect"] != nil ||
		m.lists.agents.index("architect") >= 0 {
		t.Fatalf("y: sent %q, still listed %v", fw.sent, m.lists.agents.index("architect") >= 0)
	}
	m.lists.agents.Select("docs") // armed: listening on the bus
	if m = keys(t, m, 'd'); m.ui.confirm != nil || !strings.Contains(m.ui.flash, "is listening") {
		t.Fatalf("a live agent: %q", m.ui.flash)
	}
}
