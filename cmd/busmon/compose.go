package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The compose window (v0.16, spec 2026-10-17-busmon-compose-window-design.md):
// a multi-line editor over the screen. One instance, created at startup; it
// writes a message (with a recipient) or a free answer to one question.

type composeKind int

const (
	composeMessage composeKind = iota
	composeAnswer
)

// answerMaxRunes is the free answer's cap (the question panel's, before v0.16).
const answerMaxRunes = 2000

// composer is the window. Its message draft (recipient and text) survives Esc;
// an answer's draft is kept per question in ui.askDrafts.
type composer struct {
	*tview.Box
	u      *ui
	open   bool
	kind   composeKind
	area   *tview.TextArea
	to     *tview.InputField
	onTo   bool // the recipient field has the focus
	toList bool // the recipient field's completion list is open
	limit  int
	title  string
	askID  string // composeAnswer: the ask and question answered
	qid    string
	header string // composeAnswer: the question's header

	msgTo, msgBody string // the message draft while the window shows an answer or is closed
}

func newComposer(u *ui) *composer {
	c := &composer{Box: tview.NewBox(), u: u}
	c.area = tview.NewTextArea().SetWrap(true).SetWordWrap(true)
	c.area.SetBackgroundColor(tcell.ColorDefault)
	c.area.SetTextStyle(th.Style(RoleText))
	c.area.SetPlaceholderStyle(th.Style(RoleMuted))
	c.area.SetSelectedStyle(th.Selected(RoleText))
	c.area.SetChangedFunc(c.clamp)
	c.to = tview.NewInputField().SetLabel("to: ")
	th.StyleInput(c.to)
	c.to.SetPlaceholder("@agent, @all, or empty for a broadcast")
	c.to.SetAutocompleteFunc(func(text string) []string {
		out := toCompletions(text, u.completionNames())
		c.toList = len(out) > 0 // tview opens its list on a non-empty result
		return out
	})
	c.to.SetDoneFunc(func(tcell.Key) { c.focusText() })
	c.SetBorder(true)
	c.SetBorderPadding(0, 0, 1, 1)
	c.SetBackgroundColor(tcell.ColorDefault)
	return c
}

// recipientCompletions is the one completion of the line and the compose
// window: "@<name> " for all, then every name in order, starting with what
// follows the "@". Nothing once a space is typed (the body has started).
func recipientCompletions(text string, names []string) []string {
	if !strings.HasPrefix(text, "@") || strings.ContainsAny(text, " \t\n") {
		return nil
	}
	sorted := slices.Sorted(slices.Values(names))
	var out []string
	for _, n := range append([]string{"all"}, sorted...) {
		if strings.HasPrefix(n, text[1:]) {
			out = append(out, "@"+n+" ")
		}
	}
	return out
}

// toCompletions is the to: field's completion: the field holds only a
// recipient, so a bare prefix ("ma") completes too (the line needs the "@":
// there plain text is a message). An empty field opens no list: Tab there
// moves to the text, and an open list would take the Tab instead.
func toCompletions(text string, names []string) []string {
	if text == "" {
		return nil
	}
	if !strings.HasPrefix(text, "@") {
		text = "@" + text
	}
	return recipientCompletions(text, names)
}

// runes is the bus's own count (ValidateText).
func runes(s string) int { return utf8.RuneCountInString(s) }

// clamp keeps the text within the limit: a change past it (an internal paste,
// a key the window let through) is cut at the last whole rune that fits.
func (c *composer) clamp() {
	t := c.area.GetText()
	if n := runes(t); n > c.limit {
		c.area.SetText(cutRunes(t, c.limit), true)
		c.u.flash, c.u.flashErr = fmt.Sprintf("limit %d characters: %d dropped", c.limit, n-c.limit), true
	}
}

// cutRunes keeps the first n runes of s.
func cutRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// normalizeCompose is the text as it is sent: newlines unified, control
// characters other than newline and tab removed, trailing spaces of each line
// and blank lines at both ends trimmed.
func normalizeCompose(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// recipient is the recipient field read: "" (a broadcast), "all", or a name.
func (c *composer) recipient() string {
	return strings.TrimPrefix(strings.TrimSpace(c.to.GetText()), "@")
}

// prefixToRecipient: a text that is only "@prefix" becomes the recipient on
// Tab, completed when one name matches (the focus stays on the text), else
// typed in to: with its list open. The text itself is never read as a
// recipient when sending (ui.send takes recipient and body as fields).
func (c *composer) prefixToRecipient() bool {
	t := c.area.GetText()
	cands := recipientCompletions(t, c.u.completionNames())
	if len(cands) == 0 {
		return false
	}
	c.area.SetText("", false)
	if len(cands) == 1 {
		c.to.SetText(strings.TrimSpace(cands[0]))
		c.focusText()
		return true
	}
	c.to.SetText(t)
	c.onTo = true
	c.u.app.SetFocus(c.to)
	c.to.Autocomplete()
	return true
}

func (c *composer) focusText() {
	c.onTo = false
	c.u.app.SetFocus(c.area)
}

// focused is the window's focused field.
func (c *composer) focused() tview.Primitive {
	if c.onTo && c.kind == composeMessage {
		return c.to
	}
	return c.area
}

func (c *composer) Focus(delegate func(p tview.Primitive)) { delegate(c.focused()) }
func (c *composer) HasFocus() bool                         { return c.area.HasFocus() || c.to.HasFocus() }

func (c *composer) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return c.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		f := c.focused()
		f.InputHandler()(event, setFocus)
	})
}

// PasteHandler cuts a paste to what the limit leaves (never mid-rune) and
// gives it to the focused field; the recipient field takes one line only.
func (c *composer) PasteHandler() func(text string, setFocus func(p tview.Primitive)) {
	return c.WrapPasteHandler(func(text string, setFocus func(p tview.Primitive)) {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		if c.focused() == c.to {
			text, _, _ = strings.Cut(text, "\n")
			c.to.PasteHandler()(text, setFocus)
			return
		}
		sel, _, _ := c.area.GetSelection()
		room := c.limit - runes(c.area.GetText()) + runes(sel)
		if n := runes(text); n > room {
			text = cutRunes(text, room)
			c.u.flash, c.u.flashErr = fmt.Sprintf("limit %d characters: %d dropped from the paste", c.limit, n-max(room, 0)), true
		}
		if text != "" {
			c.area.PasteHandler()(text, setFocus)
		}
	})
}

// full: one more character would pass the limit.
func (c *composer) full() bool {
	sel, _, _ := c.area.GetSelection()
	return runes(c.area.GetText())-runes(sel) >= c.limit
}

// rect is where the window goes on a w×h screen below a header of headerH lines.
func composeRect(w, h, headerH int) Rect {
	if w < mediumMinWidth {
		top := min(headerH, h-minHeight+1)
		return Rect{0, top, w, h - top}
	}
	bw, bh := min(100, w-4), min(h-6, 16)
	bh = max(bh, min(h-2, 9)) // at least 6 text lines when the screen allows
	return Rect{(w - bw) / 2, (h - bh) / 2, bw, bh}
}

// headerLine is the window's first inner line for an answer, or the type for
// a message (the recipient field draws itself before it).
func (c *composer) kindWord() (string, Role) {
	switch r := c.recipient(); {
	case r == "":
		return "broadcast", RoleAttention
	case r == "all" || c.u.knownAgent(r):
		return "directive", RoleMuted
	default:
		return "unknown recipient", RoleAttention
	}
}

func (c *composer) Draw(screen tcell.Screen) {
	sx, sy, sw, sh := c.GetRect()
	if sw < minWidth || sh < minHeight {
		return
	}
	r := composeRect(sw, sh, c.u.layout.Header.H)
	c.SetRect(sx+r.X, sy+r.Y, r.W, r.H)
	defer c.SetRect(sx, sy, sw, sh) // the page keeps the whole screen (mouse)
	c.SetBorderStyle(th.FrameBorder(panelHue[PanelActivity], true))
	c.SetTitle(" " + c.title + " ")
	c.SetTitleColor(th.colors[RoleText])
	c.SetTitleAlign(tview.AlignLeft)
	c.Box.DrawForSubclass(screen, c)
	x, y, w, h := c.GetInnerRect()
	if w < 10 || h < 3 {
		return
	}
	if c.kind == composeMessage {
		word, role := c.kindWord()
		tail := Line{{Text: " " + GlyphNone + " ", Role: RoleMuted}, {Text: word, Role: role}}
		fw := max(10, w-tail.Width())
		c.to.SetRect(x, y, fw, 1)
		c.to.Draw(screen)
		DrawLine(screen, x+fw, y, w-fw, tail, false)
	} else {
		DrawLine(screen, x, y, w, Line{{Text: "Answer to ", Role: RoleMuted}, {Text: Preview(c.header), Role: RoleAccent}}, false)
	}
	c.area.SetRect(x, y+1, w, h-2)
	// A text that fits starts at its top: an offset left from a smaller
	// size (or from keys typed before the first draw) would hide it. The
	// cursor's row counts wrapped rows: a long line with no newline is many
	// rows, and pinning it at its top hid the cursor and what was typed.
	if row, _ := c.area.GetOffset(); row > 0 {
		if _, _, cur, _ := c.area.GetCursor(); cur < h-2 && wrappedRows(c.area.GetText(), w) <= h-2 {
			c.area.SetOffset(0, 0)
		}
	}
	c.area.Draw(screen)
	// The scrollbar on the window's right border, like the panels': the
	// total is an estimate (wrappedRows), never below what the offset and the
	// cursor prove.
	off, _ := c.area.GetOffset()
	_, _, cur, _ := c.area.GetCursor()
	bx, by, bw, bh := c.GetRect()
	DrawScrollbar(screen, bx+bw-1, by, bh, ScrollState{
		Total:   max(wrappedRows(c.area.GetText(), w), off+h-2, cur+1),
		Top:     off,
		Visible: h - 2,
	}, borderCell(panelHue[PanelActivity], true))
	counter := fmt.Sprintf("%d / %d", runes(c.area.GetText()), c.limit)
	crole := RoleMuted
	if runes(c.area.GetText()) >= c.limit {
		crole = RoleAttention
	}
	var foot Line
	if c.u.flash != "" {
		role := RoleOK
		if c.u.flashErr {
			role = RoleError
		}
		foot = Line{{Text: c.u.flash, Role: role}}
	} else {
		foot = c.hints()
	}
	foot = FitLine(foot, w-Width(counter)-1)
	foot = append(foot, Seg{Text: strings.Repeat(" ", max(1, w-foot.Width()-Width(counter)))}, Seg{Text: counter, Role: crole})
	DrawLine(screen, x, y+h-1, w, foot, false)
}

// hints are the window's advertised keys; an answer has no recipient field.
func (c *composer) hints() Line {
	var l Line
	for _, b := range bindings(ModeCompose) {
		if b.Hint == "" || (b.Act == ActComposeField && c.kind != composeMessage) {
			continue
		}
		if len(l) > 0 {
			l = append(l, Seg{Text: "   "})
		}
		l = append(l, Seg{Text: b.Keys, Role: RoleAccent, Bold: true}, Seg{Text: " " + b.Hint, Role: RoleMuted})
	}
	return l
}

// MouseHandler: inside the window the fields take the mouse; a click outside
// closes it like Esc; nothing reaches what is behind.
func (c *composer) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return c.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		mx, my := event.Position()
		if c.kind == composeMessage && c.to.InRect(mx, my) {
			if action == tview.MouseLeftClick {
				c.onTo = true
			}
			return c.to.MouseHandler()(action, event, setFocus)
		}
		if c.area.InRect(mx, my) {
			if action == tview.MouseLeftClick {
				c.onTo = false
			}
			return c.area.MouseHandler()(action, event, setFocus)
		}
		sx, sy, sw, sh := c.GetRect()
		r := composeRect(sw, sh, c.u.layout.Header.H)
		inside := mx >= sx+r.X && mx < sx+r.X+r.W && my >= sy+r.Y && my < sy+r.Y+r.H
		if action == tview.MouseLeftClick && !inside {
			c.u.closeComposer()
		}
		return true, nil
	})
}

// --- the ui side ---

// knownAgent: a registered agent of the project.
func (u *ui) knownAgent(name string) bool {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	a := u.st.agents[name]
	return a != nil
}

// openComposer opens the message window on its draft; with no draft, on the
// line's text (Ctrl+O), else empty with to as recipient.
func (u *ui) openComposer(lineText, to string) {
	c := u.comp
	if lineText != "" {
		c.msgTo, c.msgBody = splitRecipient(lineText)
	} else if c.msgBody == "" && c.msgTo == "" && to != "" {
		c.msgTo = "@" + to
	}
	c.kind, c.limit, c.title = composeMessage, bus.CmdMaxRunes, "Compose"
	c.to.SetText(c.msgTo)
	c.area.SetText(c.msgBody, true)
	c.area.SetPlaceholder("Enter: new line " + GlyphNone + " Ctrl+S: send")
	c.onTo = c.msgTo == "" && c.msgBody == "" && to == "" && lineText == ""
	u.showComposer()
}

// openAnswerComposer opens the window for a free answer to the ask panel's
// current question, on that question's draft (or text when given).
func (u *ui) openAnswerComposer(a bus.Ask, q bus.AskQuestionState, text string) {
	c := u.comp
	if c.open && c.kind == composeMessage {
		c.saveMessage()
	}
	key := a.ID + "|" + q.ID
	if text == "" {
		text = u.askDrafts[key]
	}
	c.kind, c.limit, c.title = composeAnswer, answerMaxRunes, "Answer "+GlyphNone+" "+Preview(a.From)
	c.askID, c.qid, c.header = a.ID, q.ID, q.Header
	c.area.SetText(cutRunes(text, c.limit), true)
	c.area.SetPlaceholder("your answer " + GlyphNone + " Ctrl+S: send")
	c.onTo = false
	u.showComposer()
}

func (u *ui) showComposer() {
	c := u.comp
	c.open = true
	u.askDraft, u.broadcastDraft = "", ""
	if u.composing != ModePanel {
		u.leaveInput(true)
	}
	u.pages.ShowPage("compose")
	u.app.SetFocus(c.focused())
}

// hasDraft: the window holds a message draft (shown or not).
func (c *composer) hasDraft() bool {
	if c.open && c.kind == composeMessage {
		return strings.TrimSpace(c.area.GetText()) != ""
	}
	return strings.TrimSpace(c.msgBody) != ""
}

func (c *composer) saveMessage() {
	c.msgTo, c.msgBody = c.to.GetText(), c.area.GetText()
}

// closeComposer keeps the draft and gives the focus back: to the question
// panel the window was opened from, else to the panel that had it.
func (u *ui) closeComposer() {
	c := u.comp
	if !c.open {
		return
	}
	if c.kind == composeMessage {
		c.saveMessage()
	} else if t := c.area.GetText(); strings.TrimSpace(t) != "" {
		u.askDrafts[c.askID+"|"+c.qid] = t
	} else {
		delete(u.askDrafts, c.askID+"|"+c.qid)
	}
	u.hideComposer()
}

func (u *ui) hideComposer() {
	u.comp.open = false
	u.askDraft, u.broadcastDraft = "", ""
	u.pages.HidePage("compose")
	if u.ov == ovAsk {
		u.renderAsk()
	}
	u.app.SetFocus(u.focusPrimitive())
	u.maybeOpenRecap()
	u.maybeReminder()
}

// composeKey is the window's key handler (ModeCompose).
func (u *ui) composeKey(ev *tcell.EventKey) *tcell.EventKey {
	c := u.comp
	act := lookup(ModeCompose, u.focus, u.tier(), keyOf(ev))
	u.flash, u.flashErr = "", false
	// The recipient's completion list is open: Tab and Enter take an entry,
	// Esc closes the list; none of them leaves the field or the window.
	if c.onTo && c.toList {
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyEnter, tcell.KeyEscape:
			c.toList = false
			return ev
		}
	}
	// A warning (open question, broadcast) holds while the recipient and the
	// text are unchanged: send compares them, so moving the cursor or the
	// field keeps it, and an edit ends it.
	switch act {
	case ActQuit:
		u.app.Stop()
	case ActSend:
		u.composeSend()
	case ActComposeField:
		if c.kind == composeMessage && !c.onTo && c.prefixToRecipient() {
			break
		}
		if c.kind == composeMessage {
			if c.onTo {
				c.focusText()
			} else {
				c.onTo = true
				u.app.SetFocus(c.to)
			}
		}
	case ActComposeAsk:
		tgt := c.recipient()
		body := normalizeCompose(c.area.GetText())
		if c.kind == composeMessage && u.askDraft != "" && u.askDraft == draftKey(tgt, body) {
			c.saveMessage()
			if u.answerInPanel(tgt, body, tgt != "") {
				c.msgTo, c.msgBody = "", ""
			}
		}
	case ActClose:
		u.closeComposer()
	default:
		if c.focused() == c.area && c.full() && (ev.Key() == tcell.KeyRune || ev.Key() == tcell.KeyEnter) {
			u.flash, u.flashErr = fmt.Sprintf("limit %d characters", c.limit), true
			return nil
		}
		return ev
	}
	return nil
}

// composeSend sends the window's text through the one send path.
func (u *ui) composeSend() {
	c := u.comp
	body := normalizeCompose(c.area.GetText())
	if c.kind == composeAnswer {
		if body == "" {
			u.flash, u.flashErr = "nothing to send", true
			return
		}
		a, ok := findAskLocked(u, c.askID)
		if !ok {
			u.flash, u.flashErr = "this question is no longer open", true
			return
		}
		if u.pauseGate("answer|"+c.askID+"|"+c.qid+"|"+body, u.composeSend) {
			return
		}
		for _, q := range a.Questions {
			if q.ID == c.qid {
				if u.sendAnswer(a, q, "", body) {
					delete(u.askDrafts, c.askID+"|"+c.qid)
					u.hideComposer()
				}
				return
			}
		}
		u.flash, u.flashErr = "this question is no longer open", true
		return
	}
	tgt := c.recipient()
	if tgt != "" && body != "" && u.pauseGate(draftKey(tgt, body), u.composeSend) {
		return
	}
	if res := u.send(tgt, body, tgt != "", true); res == sendSent || res == sendPartial {
		c.msgTo, c.msgBody = "", ""
		c.to.SetText("")
		c.area.SetText("", false)
		flash, isErr := u.flash, u.flashErr
		u.hideComposer()
		u.flash, u.flashErr = flash, isErr
	}
}

func findAskLocked(u *ui, id string) (bus.Ask, bool) {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	return findAsk(u.st.asks, id)
}

// splitRecipient reads the line's leading "@name" (an agent or all, with or
// without a body yet) as the window's recipient; only when the window opens.
func splitRecipient(line string) (to, body string) {
	if !strings.HasPrefix(line, "@") {
		return "", line
	}
	name, rest, _ := strings.Cut(line[1:], " ")
	if name != "all" && !bus.ValidName(name) {
		return "", line
	}
	return "@" + name, strings.TrimSpace(rest)
}

// wrappedRows estimates how many rows text takes at width when word-wrapped
// (tview keeps the TextArea's own count private): the scrollbar's total.
func wrappedRows(text string, width int) int {
	if width <= 0 {
		return 0
	}
	n := 0
	for _, line := range strings.Split(text, "\n") {
		n += max(1, len(tview.WordWrap(line, width)))
	}
	return n
}
