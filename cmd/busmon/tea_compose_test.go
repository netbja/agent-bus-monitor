package main

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

var (
	kCtrlS = tea.Key{Code: 's', Mod: tea.ModCtrl}
	kCtrlO = tea.Key{Code: 'o', Mod: tea.ModCtrl}
)

// The window's cap is in runes, enforced in Update: a paste past it is cut
// to what is left, never mid-rune, and a rune typed at the limit is refused,
// whatever its width (bubbles' textarea counted cells and stopped wide runes
// at half the cap).
func TestShellComposeRuneCap(t *testing.T) {
	pinLocal(t)
	for _, r := range []string{"a", "é", "日", "😀"} {
		m := keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'I', kTab) // the text
		m = step(t, m, tea.PasteMsg{Content: strings.Repeat(r, bus.CmdMaxRunes+10)})
		v := m.ui.compose.area.value()
		if n := utf8.RuneCountInString(v); n != bus.CmdMaxRunes || !utf8.ValidString(v) {
			t.Fatalf("%q pasted: %d runes (cap %d), valid %v", r, n, bus.CmdMaxRunes, utf8.ValidString(v))
		}
		if !strings.Contains(m.ui.flash, "10 dropped") {
			t.Fatalf("%q: flash %q", r, m.ui.flash)
		}
		if m = keys(t, m, []rune(r)[0]); utf8.RuneCountInString(m.ui.compose.area.value()) != bus.CmdMaxRunes {
			t.Fatalf("%q typed at the limit was taken", r)
		}
		m = keys(t, m, kBackspce, []rune(r)[0])
		if utf8.RuneCountInString(m.ui.compose.area.value()) != bus.CmdMaxRunes {
			t.Fatalf("%q: a rune typed under the limit was refused", r)
		}
	}
}

// I opens the window on the recipient field; Tab takes its completion and
// moves to the text; Enter is a new line; Ctrl+S sends recipient and body as
// fields through the send path, in a command, and the window closes.
func TestShellComposeSends(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'I')
	if c := m.ui.compose; c == nil || !c.onTo || m.mode() != ModeCompose {
		t.Fatal("I did not open the window on its recipient")
	}
	m = typeText(t, m, "@cod")
	if m = keys(t, m, kTab); m.ui.compose.to.value() != "@coder" || m.ui.compose.onTo {
		t.Fatalf("Tab: to %q, onTo %v", m.ui.compose.to.value(), m.ui.compose.onTo)
	}
	m = typeText(t, m, "line one")
	m = keys(t, m, kEnter)
	m = typeText(t, m, "line two")
	if got := m.grid().plain(); !strings.Contains(got, "line one") || !strings.Contains(got, "to: @coder") || !strings.Contains(got, "directive") {
		t.Fatalf("the window does not show its fields:\n%s", got)
	}
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"cmd coder line one\nline two"}) || m.ui.compose != nil {
		t.Fatalf("sent %q, window open %v", fw.sent, m.ui.compose != nil)
	}
	if m.composeHasDraft() {
		t.Fatal("the sent text stayed as a draft")
	}
}

// Ctrl+O moves the line's text to the window; Esc closes it, the draft
// kept, and i (a window draft is never flattened into the line) reopens it.
func TestShellComposeFromTheLine(t *testing.T) {
	pinLocal(t)
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'i'), "@docs first part")
	m = keys(t, m, kCtrlO)
	c := m.ui.compose
	if c == nil || c.to.value() != "@docs" || c.area.value() != "first part" || m.ui.line != lineNone {
		t.Fatalf("Ctrl+O: %+v", c)
	}
	m = keys(t, m, kEsc)
	if m.ui.compose != nil || m.ui.msgCompose.body != "first part" {
		t.Fatalf("Esc: open %v, draft %+v", m.ui.compose != nil, m.ui.msgCompose)
	}
	if m = keys(t, m, 'i'); m.ui.compose == nil || m.ui.compose.area.value() != "first part" {
		t.Fatal("i did not reopen the window on its draft")
	}
}

// Clicks: on the recipient row it takes the keyboard, in the text the text
// does (and the cursor goes there), outside the window it closes, the draft
// kept.
func TestShellComposeClicks(t *testing.T) {
	pinLocal(t)
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'I', kTab), "hello world")
	f, tr := m.composeRects()
	if m = clickAt(t, m, f.X+6, f.Y+1); !m.ui.compose.onTo {
		t.Fatal("a click on the recipient row did not focus it")
	}
	if m = clickAt(t, m, tr.X+5, tr.Y); m.ui.compose.onTo || m.ui.compose.area.cur != 5 {
		t.Fatalf("a click in the text: onTo %v, cursor %d", m.ui.compose.onTo, m.ui.compose.area.cur)
	}
	m = typeText(t, m, ",")
	if got := m.ui.compose.area.value(); got != "hello, world" {
		t.Fatalf("typed at the click: %q", got)
	}
	if m = clickAt(t, m, 0, m.height-1); m.ui.compose != nil || m.ui.msgCompose.body != "hello, world" {
		t.Fatal("a click outside did not close the window with its draft")
	}
}

// A free answer written in the window is sent as the question's answer, and
// the window closes on its success.
func TestShellComposeAnswers(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, false)
	id := m.snap.asks[0].ID
	m = keys(t, m, '!', 'o') // Other: the window on the first question
	if c := m.ui.compose; c == nil || c.kind != composeAnswer || c.qid != "go" {
		t.Fatalf("o: %+v", m.ui.compose)
	}
	m = typeText(t, m, "only after the tests")
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"answer " + id + " go only after the tests"}) || m.ui.compose != nil {
		t.Fatalf("sent %q, window open %v", fw.sent, m.ui.compose != nil)
	}
	if p := m.rd.top(); p == nil || p.kind != ovAsk || p.ask.q != 1 {
		t.Fatalf("the ask did not move to its next question: %+v", p)
	}
}

// The text area wraps at a space, moves by row with the arrows and keeps the
// cursor in view.
func TestShellComposeTextArea(t *testing.T) {
	ta := textArea{limit: 100}
	ta.insert("one two three four")
	if rs := ta.rows(9); len(rs) != 3 || string(ta.text[rs[1].from:rs[1].to]) != "three " {
		var got []string
		for _, r := range rs {
			got = append(got, string(ta.text[r.from:r.to]))
		}
		t.Fatalf("rows at 9: %q", got)
	}
	ta.vertical(9, -1)
	if r, _ := ta.cursorAt(9); r != 1 {
		t.Fatalf("up: row %d", r)
	}
	ta.keepCursor(9, 1)
	if ta.top != 1 {
		t.Fatalf("keepCursor: top %d", ta.top)
	}
	ta.set("a日b")
	ta.left()
	ta.backspace()
	if ta.value() != "ab" || ta.cur != 1 {
		t.Fatalf("backspace by cluster: %q at %d", ta.value(), ta.cur)
	}
}
