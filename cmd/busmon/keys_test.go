package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestLookupMatrix(t *testing.T) {
	ctrlC := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
	cases := []struct {
		name string
		m    Mode
		p    PanelID
		t    Tier
		ev   *tcell.EventKey
		want Action
	}{
		{"activity End is live", ModePanel, PanelActivity, TierWide, key(tcell.KeyEnd), ActLive},
		{"activity G is live", ModePanel, PanelActivity, TierWide, runeKey('G'), ActLive},
		{"agents End is last", ModePanel, PanelAgents, TierWide, key(tcell.KeyEnd), ActLast},
		{"board G is last", ModePanel, PanelBoard, TierWide, runeKey('G'), ActLast},
		{"input q is text", ModeInput, PanelAgents, TierWide, runeKey('q'), ActNone},
		{"input Tab completes", ModeInput, PanelAgents, TierWide, key(tcell.KeyTab), ActNone},
		{"input digit is text", ModeInput, PanelAgents, TierMedium, runeKey('1'), ActNone},
		{"input Enter sends", ModeInput, PanelAgents, TierWide, key(tcell.KeyEnter), ActSend},
		{"input Esc", ModeInput, PanelAgents, TierWide, key(tcell.KeyEscape), ActEscape},
		{"filter q is text", ModeFilter, PanelActivity, TierWide, runeKey('q'), ActNone},
		{"filter Enter applies", ModeFilter, PanelActivity, TierWide, key(tcell.KeyEnter), ActSend},
		{"panel q quits", ModePanel, PanelAgents, TierWide, runeKey('q'), ActQuit},
		{"panel Esc", ModePanel, PanelAgents, TierWide, key(tcell.KeyEscape), ActEscape},
		{"overlay digit ignored", ModeOverlay, PanelAgents, TierMedium, runeKey('1'), ActNone},
		{"overlay q closes", ModeOverlay, PanelAgents, TierWide, runeKey('q'), ActClose},
		{"overlay Esc closes", ModeOverlay, PanelAgents, TierWide, key(tcell.KeyEscape), ActClose},
		{"overlay Tab ignored", ModeOverlay, PanelAgents, TierWide, key(tcell.KeyTab), ActNone},
		{"overlay Y", ModeOverlay, PanelAgents, TierWide, runeKey('Y'), ActCopyThread},
		{"overlay j scrolls", ModeOverlay, PanelAgents, TierWide, runeKey('j'), ActDown},
		{"medium 1", ModePanel, PanelAgents, TierMedium, runeKey('1'), ActTab1},
		{"medium 3", ModePanel, PanelAgents, TierMedium, runeKey('3'), ActTab3},
		{"medium 4 unbound", ModePanel, PanelAgents, TierMedium, runeKey('4'), ActNone},
		{"narrow 4", ModePanel, PanelAgents, TierNarrow, runeKey('4'), ActTab4},
		{"wide 1 unbound", ModePanel, PanelAgents, TierWide, runeKey('1'), ActNone},
		{"Tab next", ModePanel, PanelAgents, TierWide, key(tcell.KeyTab), ActNextPanel},
		{"Backtab prev", ModePanel, PanelAgents, TierWide, key(tcell.KeyBacktab), ActPrevPanel},
		{"Enter opens", ModePanel, PanelBoard, TierWide, key(tcell.KeyEnter), ActOpen},
		{"v opens", ModePanel, PanelBoard, TierWide, runeKey('v'), ActOpen},
		{"slash filters", ModePanel, PanelAgents, TierWide, runeKey('/'), ActFilter},
		{"i composes", ModePanel, PanelAgents, TierWide, runeKey('i'), ActCompose},
		{"colon composes", ModePanel, PanelAgents, TierWide, runeKey(':'), ActCompose},
		{"at composes", ModePanel, PanelAgents, TierWide, runeKey('@'), ActComposeAt},
		{"F1", ModePanel, PanelAgents, TierWide, key(tcell.KeyF1), ActHelp},
		{"?", ModePanel, PanelAgents, TierWide, runeKey('?'), ActHelp},
		{"F2", ModePanel, PanelAgents, TierWide, key(tcell.KeyF2), ActRequests},
		{"r", ModePanel, PanelAgents, TierWide, runeKey('r'), ActRequests},
		{"y copies", ModePanel, PanelActivity, TierWide, runeKey('y'), ActCopy},
		{"PgDn", ModePanel, PanelActivity, TierWide, key(tcell.KeyPgDn), ActPageDown},
		{"Home", ModePanel, PanelActivity, TierWide, key(tcell.KeyHome), ActFirst},
		{"g", ModePanel, PanelActivity, TierWide, runeKey('g'), ActFirst},
		{"k", ModePanel, PanelActivity, TierWide, runeKey('k'), ActUp},
		{"unbound x", ModePanel, PanelActivity, TierWide, runeKey('x'), ActNone},
	}
	for _, c := range cases {
		if got := lookup(c.m, c.p, c.t, c.ev); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, m := range []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay} {
		if got := lookup(m, PanelAgents, TierWide, ctrlC); got != ActQuit {
			t.Errorf("Ctrl-C in mode %v: %v", m, got)
		}
	}
}

func TestFooterMatchesBindings(t *testing.T) {
	for _, m := range []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay} {
		for _, p := range []PanelID{PanelAgents, PanelActivity, PanelBoard, PanelRequests} {
			for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
				shown := footerHints(m, p, tier, 400).text()
				for _, b := range bindings(m) {
					if b.Hint == "" || !b.applies(p, tier) {
						continue
					}
					if !strings.Contains(shown, b.Keys+" "+b.Hint) {
						t.Errorf("mode %v panel %v tier %v: footer %q lacks %q", m, p, tier, shown, b.Keys+" "+b.Hint)
					}
				}
				for _, b := range bindings(m) {
					if b.Hint != "" && !b.applies(p, tier) && strings.Contains(shown, b.Keys+" "+b.Hint) {
						t.Errorf("mode %v panel %v tier %v: footer advertises inactive %q", m, p, tier, b.Keys+" "+b.Hint)
					}
				}
				for w := 1; w <= 200; w += 13 {
					l := footerHints(m, p, tier, w)
					if l.Width() != w {
						t.Fatalf("footer width %d at %d", l.Width(), w)
					}
					if strings.Contains(l.text(), GlyphEllipsis) {
						t.Fatalf("footer cut a hint in half at %d: %q", w, l.text())
					}
				}
			}
		}
	}
	if s := footerHints(ModePanel, PanelActivity, TierWide, 400).text(); !strings.Contains(s, "live") {
		t.Errorf("Activity footer lacks the live hint: %q", s)
	}
	if s := footerHints(ModePanel, PanelAgents, TierWide, 400).text(); strings.Contains(s, " live") {
		t.Errorf("Agents footer advertises live: %q", s)
	}
}

func TestHelpCoversVocabulary(t *testing.T) {
	help := helpText()
	words := []string{
		// flags (spec §3.3.2)
		"escalated", "escalating", "gate N", "deferred", "unobserved", "waking", "woken", "pilot", "armed", "stale N", "wake:",
		// states
		"working", "idle", "blocked", "done", "no state declared",
		// activity types
		"status", "report", "auto", "notify", "request", "directive", "reply", "challenge", "verdict", "escalation",
		// header and delivery
		"used", "possible, not proven", "dispatch up", "dispatch down", "bus down", "queue", "lag", "not proof",
		"no deadline", "body not retained", "truncated at publish",
	}
	for _, w := range words {
		if !strings.Contains(help, w) {
			t.Errorf("help lacks %q", w)
		}
	}
	for _, m := range []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay} {
		for _, b := range bindings(m) {
			if b.Hint != "" && !strings.Contains(help, b.Keys) {
				t.Errorf("help lacks key %q", b.Keys)
			}
		}
	}
	for _, sec := range []string{"KEYS", "STATES", "DELIVERY"} {
		if !strings.Contains(help, sec) {
			t.Errorf("help lacks section %s", sec)
		}
	}
	for _, old := range []string{"⏰", "⌛", "✉", "⚠", "✂", "👂", "🔒", "⬢", "⧉"} {
		if strings.Contains(help, old) {
			t.Errorf("help still uses retired glyph %q", old)
		}
	}
}
