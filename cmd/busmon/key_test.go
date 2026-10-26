package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// The key tables match an engine-neutral Key (busmon Charm spec, step a):
// keyOf is the tcell adapter, and every tcell key a table binds maps to one.
func TestKeyOfTcell(t *testing.T) {
	for tk, want := range map[tcell.Key]KeyCode{
		tcell.KeyEnter: keyEnter, tcell.KeyEscape: keyEscape, tcell.KeyTab: keyTab, tcell.KeyBacktab: keyBacktab,
		tcell.KeyBackspace: keyBackspace, tcell.KeyBackspace2: keyBackspace, tcell.KeyDelete: keyDelete,
		tcell.KeyUp: keyUp, tcell.KeyDown: keyDown, tcell.KeyLeft: keyLeft, tcell.KeyRight: keyRight,
		tcell.KeyHome: keyHome, tcell.KeyEnd: keyEnd, tcell.KeyPgUp: keyPgUp, tcell.KeyPgDn: keyPgDn,
		tcell.KeyF1: keyF1, tcell.KeyF2: keyF2, tcell.KeyF3: keyF3, tcell.KeyF4: keyF4, tcell.KeyF5: keyF5,
		tcell.KeyCtrlC: keyCtrlC, tcell.KeyCtrlO: keyCtrlO, tcell.KeyCtrlS: keyCtrlS, tcell.KeyCtrlT: keyCtrlT,
	} {
		if got := keyOf(tcell.NewEventKey(tk, 0, tcell.ModNone)); got.Code != want {
			t.Errorf("tcell key %v -> %v, want %v", tk, got.Code, want)
		}
	}
	if k := keyOf(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)); k.Code != keyRune || k.Rune != 'q' {
		t.Fatalf("rune: %+v", k)
	}
	if k := keyOf(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModAlt)); !k.Alt || !altEnter(k) {
		t.Fatalf("alt-enter: %+v", k)
	}
	if k := keyOf(tcell.NewEventKey(tcell.KeyF12, 0, tcell.ModNone)); k.Code != keyNone {
		t.Fatalf("an unbound key maps to none: %+v", k)
	}
}

func TestLookupMatchesNeutralKeys(t *testing.T) {
	if got := lookup(ModePanel, PanelAgents, TierWide, Key{Code: keyTab}); got != ActNextPanel {
		t.Fatalf("tab = %v", got)
	}
	if got := lookup(ModePanel, PanelAgents, TierWide, Key{Code: keyCtrlC}); got != ActQuit {
		t.Fatalf("^C = %v", got)
	}
	if got := lookup(ModePanel, PanelAgents, TierMedium, Key{Code: keyRune, Rune: '2'}); got != ActTab2 {
		t.Fatalf("2 = %v", got)
	}
}
