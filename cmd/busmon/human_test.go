package main

import (
	"context"
	"errors"
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

func TestBroadcastWarningDoesNotSurviveEsc(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("hello team")
	h.key(key(tcell.KeyEnter)) // warned
	h.key(key(tcell.KeyEscape))
	h.key(runeKey('i')) // the draft comes back
	h.key(key(tcell.KeyEnter))
	h.w.mu.Lock()
	n := len(h.w.sent)
	h.w.mu.Unlock()
	if n != 0 {
		t.Fatalf("a broadcast was sent with one Enter after Esc: %v", h.w.sent)
	}
}

// failFor fails HumanCmd for one target only.
type failFor struct {
	*fakeWriter
	target string
}

func (f failFor) HumanCmd(ctx context.Context, from, target, command string) (string, error) {
	if target == f.target {
		return "", errors.New("connection reset")
	}
	return f.fakeWriter.HumanCmd(ctx, from, target, command)
}

func TestAllPartialFailureIsNotRetriedBlindly(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.u.w = failFor{h.w, "foureyes"}
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("@all stand-up")
	h.key(key(tcell.KeyEnter))
	screen := h.draw(160, 40)
	h.w.mu.Lock()
	sent := strings.Join(h.w.sent, "|")
	h.w.mu.Unlock()
	if !strings.Contains(sent, "cmd coder stand-up") || !strings.Contains(sent, "cmd sentinel stand-up") {
		t.Fatalf("the other agents were not served: %s", sent)
	}
	if !strings.Contains(screen, "sent to 2 of 3 agents; failed: foureyes") || h.u.input.GetText() != "" {
		t.Fatalf("partial failure: draft %q, footer:\n%s", h.u.input.GetText(), screen)
	}
}

// v0.15.1 §11.5: writing to an agent with an open question holds the text
// once with a hint; Enter again sends it, Tab opens its question panel with
// the text as a free answer; typing "!" stays literal.
func TestDirectiveToAnAgentWithAnOpenQuestion(t *testing.T) {
	sh := demoShared(time.Now())
	sh.agents["coder"].openAsk = 1
	sh.asks = []bus.Ask{{ID: "1-0", From: "coder", Title: "GO", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "go", Header: "GO", Question: "Run it?",
			Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}}
	h := newHarness(t, sh)
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("@coder yes go!")
	h.key(key(tcell.KeyEnter))
	screen := h.draw(160, 40)
	h.w.mu.Lock()
	n := len(h.w.sent)
	h.w.mu.Unlock()
	if n != 0 || !strings.Contains(screen, "coder has an open question") || h.u.input.GetText() != "@coder yes go!" {
		t.Fatalf("first Enter sent %d / footer:\n%s", n, screen)
	}
	h.key(key(tcell.KeyTab))
	if h.u.ov != ovAsk || !h.u.ask.typing || string(h.u.ask.text) != "yes go!" {
		t.Fatalf("Tab: overlay %v typing %v text %q", h.u.ov, h.u.ask.typing, string(h.u.ask.text))
	}
	h.w.mu.Lock()
	n = len(h.w.sent)
	h.w.mu.Unlock()
	if n != 0 {
		t.Fatal("Tab sent something")
	}

	h2 := newHarness(t, sh)
	h2.draw(160, 40)
	h2.key(runeKey('i'))
	h2.typeText("@coder yes go!")
	h2.key(key(tcell.KeyEnter))
	h2.key(key(tcell.KeyEnter))
	h2.w.mu.Lock()
	sent := strings.Join(h2.w.sent, "|")
	h2.w.mu.Unlock()
	if sent != "cmd coder yes go!" {
		t.Fatalf("second Enter sent %q", sent)
	}
}

// With several recipients holding questions, Tab asks which one; its number
// opens that panel, prefilled.
func TestAllWithSeveralOpenQuestionsAsksWhich(t *testing.T) {
	sh := demoShared(time.Now())
	q := func(from string) bus.Ask {
		return bus.Ask{ID: from + "-1", From: from, Title: "Q", State: "open",
			Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "a", Header: "A", Question: "?",
				Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}
	}
	sh.agents["coder"].openAsk, sh.agents["foureyes"].openAsk = 1, 1
	sh.asks = []bus.Ask{q("coder"), q("foureyes")}
	h := newHarness(t, sh)
	h.draw(160, 40)
	h.key(runeKey('i'))
	h.typeText("@all hold on")
	h.key(key(tcell.KeyEnter))
	h.key(key(tcell.KeyTab))
	screen := h.draw(160, 40)
	if !strings.Contains(screen, "answer whose question? 1 coder") || h.u.ov == ovAsk {
		t.Fatalf("no chooser:\n%s", screen)
	}
	h.key(runeKey('2'))
	if h.u.ov != ovAsk || string(h.u.ask.text) != "hold on" {
		t.Fatalf("pick 2: overlay %v text %q", h.u.ov, string(h.u.ask.text))
	}
	if a, ok := h.u.currentAsk(); !ok || a.From != "foureyes" {
		t.Fatalf("opened %+v", a)
	}
}
