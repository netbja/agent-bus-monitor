package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

func ctrl(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModCtrl) }

func (h *harness) paste(s string) {
	h.u.pages.PasteHandler()(s, func(p tview.Primitive) { h.app.SetFocus(p) })
}

func (h *harness) sentNow() []string {
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return append([]string(nil), h.w.sent...)
}

// I opens the window on the selected agent; Enter is a new line, Ctrl+S sends
// the text as written, and the window closes with its draft cleared.
func TestComposeSendsSeveralLines(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	screen := h.draw(160, 45)
	if h.u.mode() != ModeCompose || !strings.Contains(screen, "Compose") || h.u.comp.recipient() != "coder" {
		t.Fatalf("I: mode %v recipient %q\n%s", h.u.mode(), h.u.comp.recipient(), screen)
	}
	h.typeText("first line")
	h.key(key(tcell.KeyEnter))
	h.typeText("second   ")
	h.key(key(tcell.KeyEnter))
	if len(h.sentNow()) != 0 {
		t.Fatal("Enter sent")
	}
	h.key(ctrl(tcell.KeyCtrlS))
	if got := strings.Join(h.sentNow(), "|"); got != "cmd coder first line\nsecond" {
		t.Fatalf("sent %q", got)
	}
	if h.u.comp.open || h.u.comp.hasDraft() || h.u.mode() != ModePanel {
		t.Fatalf("after the send: open %v draft %v mode %v", h.u.comp.open, h.u.comp.hasDraft(), h.u.mode())
	}
}

func TestAgentDetailIRepliesToThatAgent(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.openAgent("coder")
	if h.u.mode() != ModeOverlay || h.u.ov != ovAgent {
		t.Fatalf("agent detail did not open: mode=%v overlay=%v", h.u.mode(), h.u.ov)
	}
	h.key(runeKey('i'))
	if h.u.mode() != ModeCompose || !h.u.comp.open || h.u.comp.recipient() != "coder" {
		t.Fatalf("i did not open a reply compose window: mode=%v open=%v to=%q", h.u.mode(), h.u.comp.open, h.u.comp.recipient())
	}
}

// The window passes its fields as they are: an empty recipient with a body
// starting "@coder" is a broadcast (warned once), and a recipient with an
// empty body is refused.
func TestComposeNeverRebuildsTheRecipient(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.setFocusPanel(PanelActivity)
	h.key(runeKey('I'))
	h.key(key(tcell.KeyTab)) // to the text
	h.typeText("@coder do it")
	h.key(ctrl(tcell.KeyCtrlS))
	if len(h.sentNow()) != 0 || !strings.Contains(h.u.flash, "nobody reads a broadcast") || !h.u.comp.open {
		t.Fatalf("first Ctrl+S: sent %v flash %q", h.sentNow(), h.u.flash)
	}
	h.key(ctrl(tcell.KeyCtrlS))
	if got := strings.Join(h.sentNow(), "|"); got != "notify @coder do it" {
		t.Fatalf("second Ctrl+S sent %q", got)
	}

	h2 := newHarness(t, demoShared(time.Now()))
	h2.draw(160, 45)
	h2.u.lists[PanelAgents].Select("coder")
	h2.key(runeKey('I'))
	h2.key(ctrl(tcell.KeyCtrlS))
	h2.key(ctrl(tcell.KeyCtrlS))
	if len(h2.sentNow()) != 0 || h2.u.flash != "nothing to send" || !h2.u.comp.open {
		t.Fatalf("empty body: sent %v flash %q", h2.sentNow(), h2.u.flash)
	}
	h2.u.comp.to.SetText("@nobody")
	h2.typeText("hello")
	h2.key(ctrl(tcell.KeyCtrlS))
	if len(h2.sentNow()) != 0 || !strings.Contains(h2.u.flash, "unknown recipient") {
		t.Fatalf("unknown recipient: sent %v flash %q", h2.sentNow(), h2.u.flash)
	}
}

// Esc keeps the draft; i then reopens the window, never the line; a failed
// send keeps the window and its text; Tab never inserts a tab.
func TestComposeDraftAndFailure(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.typeText("keep")
	h.key(key(tcell.KeyEnter))
	h.typeText("me")
	h.key(key(tcell.KeyTab))
	h.key(key(tcell.KeyTab))
	if strings.Contains(h.u.comp.area.GetText(), "\t") || h.u.comp.recipient() != "coder" {
		t.Fatalf("Tab: text %q recipient %q", h.u.comp.area.GetText(), h.u.comp.recipient())
	}
	h.key(key(tcell.KeyEscape))
	if h.u.comp.open || h.u.mode() != ModePanel {
		t.Fatal("Esc did not close")
	}
	h.key(runeKey('i'))
	if h.u.mode() != ModeCompose || h.u.comp.area.GetText() != "keep\nme" {
		t.Fatalf("i with a window draft: mode %v text %q", h.u.mode(), h.u.comp.area.GetText())
	}
	h.w.fail = errors.New("connection refused")
	h.key(ctrl(tcell.KeyCtrlS))
	if !h.u.comp.open || h.u.comp.area.GetText() != "keep\nme" || !strings.Contains(h.u.flash, "send failed") {
		t.Fatalf("failure: open %v text %q flash %q", h.u.comp.open, h.u.comp.area.GetText(), h.u.flash)
	}
	if screen := h.draw(160, 45); !strings.Contains(screen, "send failed: connection refused") {
		t.Fatalf("the window does not show the failure:\n%s", screen)
	}
	// A resize keeps the text and the focus.
	h.draw(100, 40)
	if h.u.mode() != ModeCompose || h.u.comp.area.GetText() != "keep\nme" || !h.u.comp.area.HasFocus() {
		t.Fatal("resize lost the window")
	}
	// A click outside closes like Esc, the draft kept.
	h.u.pages.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(0, 39, tcell.ButtonPrimary, 0), func(p tview.Primitive) { h.app.SetFocus(p) })
	if h.u.comp.open || !h.u.comp.hasDraft() {
		t.Fatal("outside click: not closed or draft lost")
	}
}

// Ctrl+O moves the line's text to the window, recipient split off; a paste
// with a newline into the line does the same.
func TestComposeFromTheLine(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('i'))
	h.typeText("@coder start")
	h.key(ctrl(tcell.KeyCtrlO))
	if h.u.mode() != ModeCompose || h.u.comp.recipient() != "coder" || h.u.comp.area.GetText() != "start" || h.u.msgDraft != "" {
		t.Fatalf("Ctrl+O: mode %v to %q text %q line draft %q", h.u.mode(), h.u.comp.recipient(), h.u.comp.area.GetText(), h.u.msgDraft)
	}

	h2 := newHarness(t, demoShared(time.Now()))
	h2.draw(160, 45)
	h2.key(runeKey('i'))
	h2.typeText("@coder ")
	h2.paste("one\r\ntwo")
	if h2.u.mode() != ModeCompose || h2.u.comp.area.GetText() != "one\ntwo" {
		t.Fatalf("paste into the line: mode %v text %q", h2.u.mode(), h2.u.comp.area.GetText())
	}
	h2.key(runeKey('x'))
	h2.paste("plain") // one line stays in the window it now is in
	if !strings.HasSuffix(h2.u.comp.area.GetText(), "xplain") {
		t.Fatalf("second paste: %q", h2.u.comp.area.GetText())
	}
}

// The limit counts runes, not bytes: multi-byte text fills to the exact
// limit, the next character is refused, a paste is cut on a rune boundary.
func TestComposeRuneLimit(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.u.comp.limit = 5
	h.paste("é€")
	h.typeText("ab")
	h.paste("ü日本")
	if got := h.u.comp.area.GetText(); got != "é€abü" || runes(got) != 5 {
		t.Fatalf("at the limit: %q (%d runes)", got, runes(got))
	}
	if !strings.Contains(h.u.flash, "dropped") {
		t.Fatalf("the cut paste is not reported: %q", h.u.flash)
	}
	h.typeText("z")
	if got := h.u.comp.area.GetText(); got != "é€abü" {
		t.Fatalf("limit + 1 accepted: %q", got)
	}
	if cutRunes("日本語", 2) != "日本" || cutRunes("abc", 0) != "" {
		t.Fatal("cutRunes")
	}
}

func TestNormalizeCompose(t *testing.T) {
	in := "\n\n  line one  \r\nline\x07 two\t\rlast \x1b[31m\n\n"
	if got := normalizeCompose(in); got != "  line one\nline two\nlast [31m" {
		t.Fatalf("got %q", got)
	}
}

// @all with some agents served closes the window and clears the draft (a
// retry would send twice); with none served it stays.
func TestComposeAllPartialFailure(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.w.failFor = map[string]bool{"foureyes": true}
	h.draw(160, 45)
	h.key(runeKey('I'))
	h.u.comp.to.SetText("@all")
	h.typeText("stop")
	h.key(ctrl(tcell.KeyCtrlS))
	if h.u.comp.open || h.u.comp.hasDraft() || !strings.Contains(h.u.flash, "failed: foureyes") {
		t.Fatalf("partial: open %v draft %v flash %q", h.u.comp.open, h.u.comp.hasDraft(), h.u.flash)
	}
	h.w.failFor = nil
	h.w.fail = errors.New("down")
	h.key(runeKey('I'))
	h.u.comp.to.SetText("@all")
	h.typeText("again")
	h.key(ctrl(tcell.KeyCtrlS))
	if !h.u.comp.open || !h.u.comp.hasDraft() {
		t.Fatal("total failure closed the window")
	}
}

// I in an agent's detail writes to that agent; Ctrl+T after the hint answers
// in its panel with the window's text.
func TestComposeFromTheDetailAndToAQuestion(t *testing.T) {
	sh := demoShared(time.Now())
	sh.agents["coder"].openAsk = 1
	sh.asks = []bus.Ask{{ID: "1-0", From: "coder", Title: "GO", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "go", Header: "GO", Question: "Run it?",
			Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}}
	h := newHarness(t, sh)
	h.draw(160, 45)
	h.u.openAgent("foureyes")
	h.key(runeKey('I'))
	if h.u.mode() != ModeCompose || h.u.comp.recipient() != "foureyes" || h.u.ov != ovNone {
		t.Fatalf("I in the detail: mode %v to %q ov %v", h.u.mode(), h.u.comp.recipient(), h.u.ov)
	}
	h.key(key(tcell.KeyEscape))
	h.u.comp.msgTo, h.u.comp.msgBody = "", ""

	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.typeText("yes, run")
	h.key(ctrl(tcell.KeyCtrlS))
	if len(h.sentNow()) != 0 || !strings.Contains(h.u.flash, "Ctrl+T: answer") {
		t.Fatalf("hint: sent %v flash %q", h.sentNow(), h.u.flash)
	}
	h.key(ctrl(tcell.KeyCtrlT))
	if h.u.ov != ovAsk || h.u.comp.kind != composeAnswer || h.u.comp.area.GetText() != "yes, run" {
		t.Fatalf("Ctrl+T: ov %v kind %v text %q", h.u.ov, h.u.comp.kind, h.u.comp.area.GetText())
	}
}

// A free answer that fails keeps its text and the window; Esc returns to the
// question panel with the text kept for that question.
func TestComposeAnswerKeepsItsText(t *testing.T) {
	sh := askShared(time.Now())
	h := newHarness(t, sh)
	h.w.asks = sh.asks
	h.draw(160, 45)
	h.key(runeKey('!'))
	a, _ := h.u.currentAsk()
	q := a.Questions[h.u.ask.q]
	h.u.ask.opt = len(q.Options)
	h.key(key(tcell.KeyEnter))
	if h.u.mode() != ModeCompose || h.u.comp.kind != composeAnswer {
		t.Fatalf("Other: mode %v", h.u.mode())
	}
	h.typeText("my answer")
	h.key(key(tcell.KeyTab)) // consumed, no tab
	h.w.fail = errors.New("refused")
	h.key(ctrl(tcell.KeyCtrlS))
	if !h.u.comp.open || h.u.comp.area.GetText() != "my answer" {
		t.Fatalf("failed answer: open %v text %q", h.u.comp.open, h.u.comp.area.GetText())
	}
	h.key(key(tcell.KeyEscape))
	if h.u.mode() != ModeAsk || h.u.askDrafts[a.ID+"|"+q.ID] != "my answer" {
		t.Fatalf("Esc: mode %v drafts %v", h.u.mode(), h.u.askDrafts)
	}
	h.w.fail = nil
	h.key(key(tcell.KeyEnter))
	if h.u.comp.area.GetText() != "my answer" {
		t.Fatalf("reopened without its draft: %q", h.u.comp.area.GetText())
	}
	h.key(ctrl(tcell.KeyCtrlS))
	sent := h.sentNow()
	if len(sent) == 0 || !strings.HasSuffix(sent[len(sent)-1], " my answer") || len(h.u.askDrafts) != 0 || h.u.comp.open {
		t.Fatalf("answer: sent %v drafts %v open %v", sent, h.u.askDrafts, h.u.comp.open)
	}
}

// Lines typed before the window's first draw are all shown once it is drawn.
func TestComposeShowsEveryLine(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(120, 34)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.typeText("first")
	h.key(key(tcell.KeyEnter))
	h.typeText("second")
	screen := h.draw(120, 34)
	if !strings.Contains(screen, "first") || !strings.Contains(screen, "second") {
		t.Fatalf("a line is hidden:\n%s", screen)
	}
}

// The open-question hint holds while recipient and text are unchanged:
// moving the cursor or the field keeps Ctrl+T available; an edit ends it.
func TestComposeHintSurvivesMoves(t *testing.T) {
	sh := demoShared(time.Now())
	sh.agents["coder"].openAsk = 1
	sh.asks = []bus.Ask{{ID: "1-0", From: "coder", Title: "GO", State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "go", Header: "GO", Question: "Run it?",
			Options: []bus.AskOption{{Label: "Yes"}, {Label: "No"}}}}}}}
	h := newHarness(t, sh)
	h.draw(160, 45)
	h.u.lists[PanelAgents].Select("coder")
	h.key(runeKey('I'))
	h.typeText("go now")
	h.key(ctrl(tcell.KeyCtrlS))
	h.key(key(tcell.KeyLeft))
	h.key(key(tcell.KeyTab))
	h.key(key(tcell.KeyTab))
	h.key(ctrl(tcell.KeyCtrlT))
	if h.u.ov != ovAsk || h.u.comp.kind != composeAnswer || h.u.comp.area.GetText() != "go now" {
		t.Fatalf("Ctrl+T after moves: ov %v kind %v text %q", h.u.ov, h.u.comp.kind, h.u.comp.area.GetText())
	}

	h2 := newHarness(t, sh)
	h2.draw(160, 45)
	h2.u.lists[PanelAgents].Select("coder")
	h2.key(runeKey('I'))
	h2.typeText("go now")
	h2.key(ctrl(tcell.KeyCtrlS))
	h2.typeText("!")
	h2.key(ctrl(tcell.KeyCtrlT))
	if h2.u.ov == ovAsk {
		t.Fatal("Ctrl+T acted on an edited text")
	}
	h2.key(ctrl(tcell.KeyCtrlS)) // a new text: the hint again, nothing sent
	if len(h2.sentNow()) != 0 || !strings.Contains(h2.u.flash, "open question") {
		t.Fatalf("edited text: sent %v flash %q", h2.sentNow(), h2.u.flash)
	}
}

// From the line, Alt+Enter also moves the text to the window, and the line
// shows how while it is being written.
func TestLineToWindowWithAltEnter(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	h.key(runeKey('i'))
	h.typeText("@coder first")
	if screen := h.draw(160, 45); !strings.Contains(screen, "^O several lines") {
		t.Fatalf("no hint on the line:\n%s", screen)
	}
	h.key(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModAlt))
	if h.u.mode() != ModeCompose || h.u.comp.recipient() != "coder" || h.u.comp.area.GetText() != "first" || len(h.sentNow()) != 0 {
		t.Fatalf("Alt+Enter: mode %v to %q text %q sent %v", h.u.mode(), h.u.comp.recipient(), h.u.comp.area.GetText(), h.sentNow())
	}
}
