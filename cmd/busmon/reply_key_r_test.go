package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reply-key-r: r replies on a page that offers the reply (a message, report
// or note from an agent; an agent's page), as i does; r keeps replaying on
// About and opening Requests on the shell.

func TestReplyKeyR(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.pushMessage(bus.Event{Kind: "report", Agent: "coder", ID: "9-1", Message: "preview", Full: "Done: the parser\n"}, shellNow)
	m = keys(t, m, 'r')
	if cm := m.ui.compose; cm == nil || cm.recipient() != "coder" || cm.area.value() != "re: Done: the parser\n" {
		t.Fatalf("r on a report page opened %+v", m.ui.compose)
	}

	// Not offered (a dispatcher notice): r says why, as i does.
	m = fixtureShell(t, 140, 40)
	m.pushMessage(bus.Event{Kind: "notify", From: "dispatch", ID: "9-2", Message: "x"}, shellNow)
	m = keys(t, m, 'r')
	if m.ui.compose != nil || m.ui.flash == "" {
		t.Fatalf("r on a dispatcher notice: compose %v flash %q", m.ui.compose, m.ui.flash)
	}

	// An agent's page: r writes to that agent.
	m = fixtureShell(t, 140, 40)
	m.pushAgent("coder")
	if m = keys(t, m, 'r'); m.ui.compose == nil || m.ui.compose.recipient() != "coder" {
		t.Fatalf("r on an agent's page opened %+v", m.ui.compose)
	}

	// The shell: r is still Requests.
	m = fixtureShell(t, 140, 40)
	if m = keys(t, m, 'r'); m.ui.compose != nil || !m.rd.open() || m.rd.top().kind != ovRequests {
		t.Fatalf("r on the shell no longer opens Requests")
	}
}

// The footer says r reply where the reply is offered, and never both r
// hints on one page.
func TestReplyKeyRFooter(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.pushMessage(bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "coder", ID: "9-0", Message: "rebase"}, shellNow)
	if !hintsHave(m, "reply") || !footerHas(m, "r reply") || hintsHave(m, "replay") {
		t.Fatalf("footer: %q", footerText(m))
	}
	a, _ := openAboutIn(t, fixtureShell(t, 140, 40))
	if !footerHas(a, "r replay") || hintsHave(a, "reply") {
		t.Fatalf("About footer: %q", footerText(a))
	}
}

func footerText(m Model) string {
	var b strings.Builder
	for _, s := range footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint) {
		b.WriteString(s.Text)
	}
	return b.String()
}

func footerHas(m Model, s string) bool { return strings.Contains(footerText(m), s) }

// A key bound twice in one mode is a collision unless every binding is
// OnPage and no page offers two of them (lookupOn picks the offered one).
// Checked per mode over every printable rune.
func TestNoKeyCollisionPerMode(t *testing.T) {
	modes := []Mode{ModePanel, ModeInput, ModeFilter, ModeOverlay, ModeAsk, ModeCompose, ModePick, ModeShell, ModeShellAsk, ModeTour, ModeSettings, ModeNotifySetup, ModeComposeFile}
	shared := map[string]bool{}
	for _, mode := range modes {
		for r := rune(0x21); r < 0x7f; r++ {
			k := Key{Code: keyRune, Rune: r}
			var hits []binding
			for _, b := range bindings(mode) {
				if b.match(k) {
					hits = append(hits, b)
				}
			}
			if len(hits) < 2 {
				continue
			}
			acts := map[Action]bool{}
			for _, b := range hits {
				acts[b.Act] = true
			}
			if len(acts) < 2 {
				continue // one action bound twice (an alias row): no collision
			}
			for _, b := range hits {
				if !b.OnPage && len(b.Panels) == 0 && b.Tiers == nil {
					t.Errorf("mode %d: %q is bound to %v, shadowed by %q", mode, r, hits[0].Act, b.Keys)
				}
			}
			shared[fmt.Sprintf("%d:%c", mode, r)] = true
		}
	}
	// r is the one page-resolved key: replay on About, reply elsewhere.
	if !shared[fmt.Sprintf("%d:%c", ModeOverlay, 'r')] {
		t.Fatal("r is not shared by reply and replay on pages")
	}
	pages := map[string]Model{}
	msg := fixtureShell(t, 140, 40)
	msg.pushMessage(bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "coder", ID: "9-0", Message: "x"}, shellNow)
	pages["message"] = msg
	ag := fixtureShell(t, 140, 40)
	ag.pushAgent("coder")
	pages["agent"] = ag
	pages["about"], _ = openAboutIn(t, fixtureShell(t, 140, 40))
	for name, m := range pages {
		n := 0
		for _, b := range bindings(ModeOverlay) {
			if b.OnPage && b.match(Key{Code: keyRune, Rune: 'r'}) && m.pageOffers(b.Act) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s page offers %d bindings of r", name, n)
		}
	}
}
