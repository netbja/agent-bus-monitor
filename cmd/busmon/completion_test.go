package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Both inputs offer the agents the panel shows (registered, not yourself),
// plus the dormant ones while a (show all) is on.
func TestCompletionNames(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	st.agents["bernard"] = &agentState{state: "idle", stateAt: now, registered: true}
	st.agents["old"] = &agentState{state: "idle", stateAt: now.Add(-48 * time.Hour), lastSeen: now.Add(-48 * time.Hour), registered: true}
	st.agents["ghost"] = &agentState{armed: true}
	h := newHarness(t, st)
	got := strings.Join(h.u.completionNames(), " ")
	if got != "coder foureyes sentinel" {
		t.Fatalf("names %q", got)
	}
	h.u.showAll = true
	if got := strings.Join(h.u.completionNames(), " "); got != "coder foureyes old sentinel" {
		t.Fatalf("names with a: %q", got)
	}
}

// In the line, Enter with the list open takes the entry and sends nothing.
func TestLineCompletesAllAndDoesNotSend(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('i'))
	h.typeText("@a")
	h.key(key(tcell.KeyEnter))
	if got := h.u.input.GetText(); got != "@all " {
		t.Fatalf("input %q", got)
	}
	if len(h.sentNow()) != 0 {
		t.Fatalf("sent %v", h.sentNow())
	}
}

// In the compose window, Tab on a text that is only "@prefix" makes it the
// recipient: completed when unique, else typed in to: with its list open.
// The text is never read as a recipient at send time
// (TestComposeNeverRebuildsTheRecipient).
func TestComposeTabMovesAPrefixToTheRecipient(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.setFocusPanel(PanelActivity)
	h.key(runeKey('I'))
	h.key(key(tcell.KeyTab)) // to the text
	h.typeText("@co")
	h.key(key(tcell.KeyTab))
	if got := h.u.comp.to.GetText(); got != "@coder" || h.u.comp.area.GetText() != "" || h.u.comp.onTo {
		t.Fatalf("unique: to %q text %q onTo %v", got, h.u.comp.area.GetText(), h.u.comp.onTo)
	}
	h.typeText("go")
	h.key(ctrl(tcell.KeyCtrlS))
	if got := strings.Join(h.sentNow(), "|"); got != "cmd coder go" {
		t.Fatalf("sent %q", got)
	}

	h2 := newHarness(t, demoShared(time.Now()))
	h2.draw(160, 45)
	h2.u.setFocusPanel(PanelActivity)
	h2.key(runeKey('I'))
	h2.key(key(tcell.KeyTab))
	h2.typeText("@")
	h2.key(key(tcell.KeyTab))
	if got := h2.u.comp.to.GetText(); got != "@" || !h2.u.comp.onTo || h2.u.comp.area.GetText() != "" {
		t.Fatalf("ambiguous: to %q onTo %v text %q", got, h2.u.comp.onTo, h2.u.comp.area.GetText())
	}
	// The to: list is open: Esc closes it, not the window; Tab takes an entry.
	h2.typeText("s")
	h2.key(key(tcell.KeyEscape))
	if !h2.u.comp.open {
		t.Fatal("Esc with the list open closed the window")
	}
	h2.typeText("e")
	h2.key(key(tcell.KeyTab))
	if got := h2.u.comp.to.GetText(); got != "@sentinel " || !h2.u.comp.onTo {
		t.Fatalf("Tab in to: took %q onTo %v", got, h2.u.comp.onTo)
	}
}

func TestHelpExplainsComposeTab(t *testing.T) {
	if !strings.Contains(plainOf(helpText()), "a text that is only @prefix becomes the recipient") {
		t.Fatal("help does not explain Tab in the compose window")
	}
}

// The to: field holds only a recipient, so it completes a bare prefix, as it
// did before the two completions were merged; the line still needs the "@"
// (there plain text is a message). An empty field opens no list, so Tab
// there still moves to the text.
func TestToFieldCompletesABarePrefix(t *testing.T) {
	names := []string{"master", "coder"}
	if got := strings.Join(toCompletions("ma", names), "|"); got != "@master " {
		t.Fatalf("ma = %q", got)
	}
	if got := toCompletions("", names); got != nil {
		t.Fatalf("empty = %q", got)
	}
	if got := strings.Join(toCompletions("@", names), "|"); got != "@all |@coder |@master " {
		t.Fatalf("@ = %q", got)
	}
	if got := strings.Join(toCompletions("@co", names), "|"); got != "@coder " {
		t.Fatalf("@co = %q", got)
	}
	if recipientCompletions("ma", names) != nil {
		t.Fatal("the line completes a bare word")
	}
}
