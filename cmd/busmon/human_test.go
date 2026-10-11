package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestHumanIdentity(t *testing.T) {
	for _, c := range []struct{ flag, env, want string }{
		{"", "", "pilot"}, {"bernard", "", "bernard"}, {"", "alice", "alice"}, {"bernard", "alice", "bernard"},
	} {
		if got, err := humanIdentity(c.flag, c.env); err != nil || got != c.want {
			t.Fatalf("humanIdentity(%q, %q) = %q, %v; want %q", c.flag, c.env, got, err, c.want)
		}
	}
	if _, err := humanIdentity("Bernard Jaulin", ""); err == nil {
		t.Fatal("an invalid name was accepted")
	}
}

func TestHumanLabel(t *testing.T) {
	for _, c := range []struct{ self, pilot, want string }{
		{"bernard", "bernard", GlyphHuman + " Bernard [human · pilot]"},
		{"pilot", "", GlyphHuman + " Pilot [human]"},
		{"bernard", "codex", GlyphHuman + " Bernard [human]"},
	} {
		if got := humanLabel(c.self, c.pilot); got != c.want {
			t.Fatalf("humanLabel(%q, %q) = %q, want %q", c.self, c.pilot, got, c.want)
		}
	}
}

func TestLookEventShowsHumans(t *testing.T) {
	look := lookEvent(bus.Event{Kind: "cmd", Type: "directive", From: "bernard", Target: "codex", Message: "x", Human: true})
	if look.From != GlyphHuman+" Bernard" {
		t.Fatalf("human from = %q", look.From)
	}
	if look := lookEvent(bus.Event{Kind: "cmd", Type: "directive", From: "codex", Target: "claude", Message: "x"}); look.From != "codex" {
		t.Fatalf("agent from = %q", look.From)
	}
}

func TestHeaderShowsTheHuman(t *testing.T) {
	now := time.Now()
	for _, c := range []struct{ pilot, want string }{{"bernard", "[human · pilot]"}, {"", "Bernard [human]"}} {
		lines := headerLines(headerData{project: "demo", self: "bernard", pilot: c.pilot, now: now, health: health{ok: true, since: now}}, 160, TierWide)
		text := ""
		for _, l := range lines {
			for _, s := range l {
				text += s.Text
			}
		}
		if !strings.Contains(text, c.want) {
			t.Fatalf("header %q lacks %q", text, c.want)
		}
	}
}

func TestSendToAllAgents(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("@all stand-up in 5 min")
	h.key(key(tcell.KeyEnter))
	screen := h.draw(160, 40)
	h.w.mu.Lock()
	sent := strings.Join(h.w.sent, "|")
	h.w.mu.Unlock()
	for _, a := range []string{"coder", "foureyes", "sentinel"} {
		if !strings.Contains(sent, "cmd "+a+" stand-up in 5 min") {
			t.Fatalf("@all did not reach %s: %s", a, sent)
		}
	}
	if !strings.Contains(screen, "directive sent to 3 agents") {
		t.Fatalf("footer:\n%s", screen)
	}
}

func TestBroadcastNeedsASecondEnter(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("hello team")
	h.key(key(tcell.KeyEnter))
	screen := h.draw(160, 40)
	h.w.mu.Lock()
	n := len(h.w.sent)
	h.w.mu.Unlock()
	if n != 0 || !strings.Contains(screen, "nobody reads a broadcast") || h.u.input.GetText() != "hello team" {
		t.Fatalf("first Enter sent %d / footer:\n%s", n, screen)
	}
	h.key(key(tcell.KeyEnter))
	screen = h.draw(160, 40)
	h.w.mu.Lock()
	sent := strings.Join(h.w.sent, "|")
	h.w.mu.Unlock()
	if sent != "notify hello team" || !strings.Contains(screen, "broadcast sent") {
		t.Fatalf("second Enter: sent %q, footer:\n%s", sent, screen)
	}
}
