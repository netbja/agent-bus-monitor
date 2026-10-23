package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

func pausedShared(now time.Time) *shared {
	st := demoShared(now)
	st.pause = &bus.PauseRecord{Until: now.Add(90 * time.Minute).UnixMilli(), Reason: "quota reset", By: "master", Notify: "master"}
	return st
}

func typeLine(h *harness, text string) {
	h.key(runeKey('i'))
	h.typeText(text)
	h.key(key(tcell.KeyEnter))
}

func TestPausePopupOnADirective(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	typeLine(h, "@coder hello")
	if !h.u.pp.open || len(h.sentNow()) != 0 {
		t.Fatalf("a directive while paused: popup %v sent %v", h.u.pp.open, h.sentNow())
	}
	screen := h.draw(160, 45)
	for _, want := range []string{"Project paused until", "quota reset", "by master", "Resume now", "Send anyway", "Cancel"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("popup lacks %q:\n%s", want, screen)
		}
	}
	h.key(runeKey('s'))
	if got := strings.Join(h.sentNow(), "|"); got != "cmd coder hello" || h.u.pp.open {
		t.Fatalf("send anyway: sent %q popup %v", got, h.u.pp.open)
	}
	if h.u.input.GetText() != "" {
		t.Fatalf("the line keeps %q after the send", h.u.input.GetText())
	}
}

func TestPausePopupCancelKeepsTheDraft(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	typeLine(h, "@coder hello")
	h.key(key(tcell.KeyEscape))
	if h.u.pp.open || len(h.sentNow()) != 0 || !strings.Contains(h.u.input.GetText(), "hello") {
		t.Fatalf("cancel: popup %v sent %v line %q", h.u.pp.open, h.sentNow(), h.u.input.GetText())
	}
	// Enter is Cancel by default.
	h.key(key(tcell.KeyEnter))
	if !h.u.pp.open {
		t.Fatal("a second Enter after a cancel asks again")
	}
	h.key(key(tcell.KeyEnter))
	if h.u.pp.open || len(h.sentNow()) != 0 {
		t.Fatalf("Enter on the default button sent: %v", h.sentNow())
	}
}

func TestPausePopupResumeNow(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	typeLine(h, "@coder hello")
	h.key(runeKey('r'))
	if got := strings.Join(h.sentNow(), "|"); got != "resume bernard|cmd coder hello" {
		t.Fatalf("resume now: %q", got)
	}
	if h.u.st.pause != nil {
		t.Fatal("the pause is still shown after a resume")
	}
	// A failed resume sends nothing.
	h2 := newHarness(t, pausedShared(time.Now()))
	h2.w.resumeErr = errors.New("redis down")
	h2.draw(160, 45)
	typeLine(h2, "@coder hello")
	h2.key(runeKey('r'))
	if got := strings.Join(h2.sentNow(), "|"); got != "resume bernard" || !strings.Contains(h2.u.flash, "redis down") {
		t.Fatalf("failed resume: sent %q flash %q", got, h2.u.flash)
	}
}

func TestPausePopupNotShown(t *testing.T) {
	now := time.Now()
	ended := pausedShared(now)
	ended.pause.Until = now.Add(-time.Minute).UnixMilli()
	unknown := pausedShared(now)
	unknown.pauseFailed = true
	for name, c := range map[string]struct {
		st   *shared
		line string
		sent string
		note string
	}{
		"pause over":  {ended, "@coder hi", "cmd coder hi", ""},
		"not paused":  {demoShared(now), "@coder hi", "cmd coder hi", ""},
		"read failed": {unknown, "@coder hi", "cmd coder hi", "pause ?"},
		"a broadcast": {pausedShared(now), "hello all", "", ""},
	} {
		h := newHarness(t, c.st)
		h.draw(160, 45)
		typeLine(h, c.line)
		if h.u.pp.open {
			t.Errorf("%s: popup shown", name)
		}
		if got := strings.Join(h.sentNow(), "|"); got != c.sent {
			t.Errorf("%s: sent %q, want %q", name, got, c.sent)
		}
		if c.note != "" && !strings.Contains(h.u.flash, c.note) {
			t.Errorf("%s: flash %q lacks %q", name, h.u.flash, c.note)
		}
	}
}

func TestPausePopupFromTheWindowAndTheAskPanel(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.typeText("long message")
	h.key(ctrl(tcell.KeyCtrlS))
	if !h.u.pp.open || len(h.sentNow()) != 0 {
		t.Fatalf("window send while paused: popup %v sent %v", h.u.pp.open, h.sentNow())
	}
	h.draw(160, 45)
	h.key(runeKey('s'))
	if got := strings.Join(h.sentNow(), "|"); got != "cmd coder long message" || h.u.comp.open {
		t.Fatalf("window send anyway: %q open %v", got, h.u.comp.open)
	}

	st := askShared(time.Now())
	st.pause = &bus.PauseRecord{Until: time.Now().Add(time.Hour).UnixMilli(), By: "master", Notify: "master"}
	h = newHarness(t, st)
	h.draw(160, 45)
	h.key(runeKey('!'))
	h.w.left = 1
	h.key(key(tcell.KeyEnter))
	if !h.u.pp.open || len(h.sentNow()) != 0 {
		t.Fatalf("an answer while paused: popup %v sent %v", h.u.pp.open, h.sentNow())
	}
	h.key(runeKey('s'))
	if got := strings.Join(h.sentNow(), "|"); !strings.HasPrefix(got, "answer 100-0 date") {
		t.Fatalf("answer anyway: %q", got)
	}
	if h.u.ov != ovAsk {
		t.Fatalf("the ask panel is gone after the popup: %v", h.u.ov)
	}
}

func TestPausePopupMouse(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	typeLine(h, "@coder hello")
	h.draw(160, 45)
	x, y, ok := h.u.pp.buttonAt(1) // Send anyway
	if !ok {
		t.Fatal("no button position after a draw")
	}
	click := tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
	h.u.pages.MouseHandler()(tview.MouseLeftClick, click, func(p tview.Primitive) { h.app.SetFocus(p) })
	if got := strings.Join(h.sentNow(), "|"); got != "cmd coder hello" {
		t.Fatalf("click on Send anyway: %q", got)
	}
}

func TestPollMarksAFailedPauseRead(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, pause: &bus.PauseRecord{Until: time.Now().Add(time.Hour).UnixMilli()}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if st.pauseFailed || st.pause == nil {
		t.Fatalf("a good read: failed %v pause %v", st.pauseFailed, st.pause)
	}
	f.pauseErr = errors.New("timeout")
	pollOnce(context.Background(), f, st, 2)
	if !st.pauseFailed || st.pause == nil {
		t.Fatalf("a failed read keeps the last value and marks it: failed %v pause %v", st.pauseFailed, st.pause)
	}
}

// "Send anyway" acknowledges that one send only: the same text sent again
// later asks again.
func TestPausePopupAcknowledgesOneSendOnly(t *testing.T) {
	h := newHarness(t, pausedShared(time.Now()))
	h.draw(160, 45)
	typeLine(h, "@coder hello")
	h.key(runeKey('s'))
	if len(h.sentNow()) != 1 {
		t.Fatalf("first send: %v", h.sentNow())
	}
	h.typeText("@coder hello")
	h.key(key(tcell.KeyEnter))
	if !h.u.pp.open || len(h.sentNow()) != 1 {
		t.Fatalf("the same text again asks again: popup %v sent %v", h.u.pp.open, h.sentNow())
	}
}
