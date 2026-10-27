package main

import (
	"strings"
	"testing"
)

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
		if !strings.Contains(plainOf(help), w) {
			t.Errorf("help lacks %q", w)
		}
	}
	for _, m := range []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay} {
		for _, b := range bindings(m) {
			if b.Hint != "" && !strings.Contains(plainOf(help), b.Keys) {
				t.Errorf("help lacks key %q", b.Keys)
			}
		}
	}
	for _, sec := range []string{"KEYS", "STATES", "DELIVERY"} {
		if !strings.Contains(plainOf(help), sec) {
			t.Errorf("help lacks section %s", sec)
		}
	}
	for _, old := range []string{"⏰", "⌛", "✉", "⚠", "✂", "👂", "🔒", "⬢", "⧉"} {
		if strings.Contains(plainOf(help), old) {
			t.Errorf("help still uses retired glyph %q", old)
		}
	}
}

// Every binding reaches the help: its own row (keys + alternates), or a row it
// is folded into on purpose. A new key without a Hint or Help fails here.
func TestHelpListsEveryBinding(t *testing.T) {
	help := helpText()
	folded := map[string]bool{"j": true, "2": true, "3": true, "4": true, "@": true}
	for _, m := range []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay} {
		for _, b := range bindings(m) {
			k, d, ok := helpRow(b)
			if !ok {
				if !folded[b.Keys] {
					t.Errorf("mode %v: key %q has neither Hint nor Help", m, b.Keys)
				}
				continue
			}
			if !strings.Contains(plainOf(help), Fit(k, 20)+" "+d) && !strings.Contains(plainOf(help), k) {
				t.Errorf("help lacks %q", k)
			}
		}
	}
	for _, alt := range []string{"enter v", "F1 ?", "F2 r", "i :", "shift-tab", "pgup", "pgdn", "home g", "^C"} {
		if !strings.Contains(plainOf(help), alt) {
			t.Errorf("help lacks %q", alt)
		}
	}
}

// The footer groups the F-keys, in ascending order, after the other keys
// (A7): F5, F2, F3 and F1 were scattered among them.
func TestFooterGroupsFKeys(t *testing.T) {
	l := footerHints(ModeShell, PanelActivity, TierWide, 1000)
	var keys []string
	for _, s := range l {
		if s.Role == RoleAccent && s.Bold {
			keys = append(keys, s.Text)
		}
	}
	first := -1
	var fs []string
	for i, k := range keys {
		if strings.HasPrefix(k, "F") && len(k) == 2 {
			if first < 0 {
				first = i
			}
			fs = append(fs, k)
		}
	}
	if strings.Join(fs, " ") != "F1 F2 F3 F5" {
		t.Fatalf("F-keys %v in footer %v", fs, keys)
	}
	if first+len(fs) != len(keys) || strings.Join(keys[first:], " ") != "F1 F2 F3 F5" {
		t.Fatalf("F-keys not grouped at the end: %v", keys)
	}
}

// A narrow footer drops ordinary keys before the F-key group, F1 first kept.
func TestNarrowFooterKeepsHelp(t *testing.T) {
	l := footerHints(ModeShell, PanelActivity, TierWide, 40)
	text := ""
	for _, s := range l {
		text += s.Text
	}
	if !strings.Contains(text, "F1 help") || !strings.HasPrefix(text, " tab focus") || l.Width() != 40 {
		t.Fatalf("footer at 40 = %q", text)
	}
}
