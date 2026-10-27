package main

import (
	"testing"
)

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
