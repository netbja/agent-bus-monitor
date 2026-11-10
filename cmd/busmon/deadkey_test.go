package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// decoded is what bubbletea's decoder makes of bytes a terminal sends: the
// key presses, as busmon's Update gets them.
func decoded(t testing.TB, in string) []tea.KeyPressMsg {
	t.Helper()
	var d uv.EventDecoder
	var out []tea.KeyPressMsg
	for b := []byte(in); len(b) > 0; {
		n, ev := d.Decode(b)
		if n == 0 {
			t.Fatalf("%q: undecodable", in)
		}
		b = b[n:]
		if k, ok := ev.(uv.KeyPressEvent); ok {
			out = append(out, tea.KeyPressMsg(k))
		}
	}
	return out
}

func feed(t testing.TB, m Model, in string) Model {
	t.Helper()
	for _, k := range decoded(t, in) {
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, tea.Key(k))
		m = run(t, m, cmd)
	}
	return m
}

// deadKeyShapes are the ways a terminal can mark a dead key's character (or
// an AltGr one) with Alt: herdr's pane on a Swiss French keyboard dropped ~ ^
// and ` in busmon's text fields (compose-dead-keys). A plain form always
// reached the text; these did not.
var deadKeyShapes = []struct{ name, in, want string }{
	{"esc-prefixed ~", "\x1b~", "~"},
	// ESC ^ is not here: it opens a privacy message (PM) for every decoder,
	// so no terminal can send alt+^ that way and be read as a key.
	{"esc-prefixed `", "\x1b`", "`"},
	{"kitty alt ~", "\x1b[126;3u", "~"},
	{"kitty alt ^", "\x1b[94;3u", "^"},
	{"kitty alt `", "\x1b[96;3u", "`"},
	{"kitty ctrl+alt (AltGr) ~", "\x1b[126;7u", "~"},
	{"kitty ctrl+alt (AltGr) `", "\x1b[96;7u", "`"},
}

func TestDeadKeyCharacterReachesTheComposeText(t *testing.T) {
	for _, c := range deadKeyShapes {
		m := atShell(t, 100, 30, &atFake{})
		m = feed(t, m, "a"+c.in+"b")
		if got := m.ui.compose.area.value(); got != "a"+c.want+"b" {
			t.Errorf("%s: the text is %q, want %q", c.name, got, "a"+c.want+"b")
		}
	}
}

func TestDeadKeyCharacterReachesTheComposeRecipient(t *testing.T) {
	for _, c := range deadKeyShapes {
		m := keys(t, fixtureShell(t, 100, 30), 'I')
		if cs := m.ui.compose; cs == nil || !cs.onTo {
			t.Fatal("the window is not on its recipient")
		}
		before := m.ui.compose.to.value()
		m = feed(t, m, c.in)
		if got := m.ui.compose.to.value(); got != before+c.want {
			t.Errorf("%s: the recipient is %q, want %q", c.name, got, before+c.want)
		}
	}
}

func TestDeadKeyCharacterReachesTheInputLine(t *testing.T) {
	for _, c := range deadKeyShapes {
		m := keys(t, fixtureShell(t, 100, 30), 'i')
		if m.mode() != ModeInput {
			t.Fatal("the line is not open")
		}
		m = feed(t, m, "a"+c.in+"b")
		if m.ui.edit != "a"+c.want+"b" {
			t.Errorf("%s: the line is %q, want %q", c.name, m.ui.edit, "a"+c.want+"b")
		}
	}
}

// An @ that came marked with Alt is still typed: it opens the file list, and
// an Alt-marked ~ after it is the home query (@~/ was the reason ~ mattered).
func TestAltMarkedAtAndTildeOpenTheHomeList(t *testing.T) {
	m := atShell(t, 100, 30, &atFake{})
	m = feed(t, m, "see \x1b[64;7u\x1b~/")
	p := m.ui.compose.at
	if p == nil || !p.home {
		t.Fatalf("the home list is not open (popup %+v, text %q)", p, m.ui.compose.area.value())
	}
}

// What stays as it was: alt-enter opens the window from the line, and a
// Ctrl+Alt letter (a shortcut, never AltGr's character) types nothing.
func TestAltStillMeansAltWhereABindingOrALetterSaysSo(t *testing.T) {
	m := keys(t, fixtureShell(t, 100, 30), 'i')
	m = feed(t, m, "hi\x1b[98;7u")
	if m.ui.edit != "hi" {
		t.Errorf("ctrl+alt+b typed: %q", m.ui.edit)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if m.ui.compose == nil {
		t.Error("alt-enter no longer opens the window")
	}
}
