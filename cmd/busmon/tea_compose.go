package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The compose window (busmon Charm spec, "Input"; v0.16's window): a message
// with its recipient, or a free answer to one question. Its text area is our
// own, over Line: bubbles' textarea counts its limit in cells, so a cap of N
// runes stopped at N/2 wide runes typed one by one (the step-b cap test, Bernard's
// decision 4). The cap is enforced in Update, in runes, never mid-rune.

// textArea is the window's text and cursor, in runes; top is the first
// visual row shown, kept by Update so View only reads it.
type textArea struct {
	text  []rune
	cur   int
	top   int
	limit int
}

func (t *textArea) value() string { return string(t.text) }

// insert puts s at the cursor, cut to what the limit leaves; it reports the
// runes dropped. s is made displayable first: the measure is one method.
func (t *textArea) insert(s string) (dropped int) {
	s = agreeWidths(DetailText(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")))
	rs := []rune(s)
	if room := t.limit - len(t.text); len(rs) > room {
		dropped, rs = len(rs)-max(room, 0), rs[:max(room, 0)]
	}
	t.text = append(t.text[:t.cur:t.cur], append(rs, t.text[t.cur:]...)...)
	t.cur += len(rs)
	return dropped
}

func (t *textArea) set(s string) {
	t.text, t.cur, t.top = nil, 0, 0
	t.insert(s)
}

// clusters are the rune offsets where each grapheme cluster starts, and the
// text's end: the cursor moves and deletes by cluster.
func (t *textArea) clusters() []int {
	out := []int{0}
	g := uniseg.NewGraphemes(string(t.text))
	n := 0
	for g.Next() {
		n += len(g.Runes())
		out = append(out, n)
	}
	return out
}

func (t *textArea) left() {
	cs := t.clusters()
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i] < t.cur {
			t.cur = cs[i]
			return
		}
	}
}

func (t *textArea) right() {
	for _, c := range t.clusters() {
		if c > t.cur {
			t.cur = c
			return
		}
	}
}

func (t *textArea) backspace() {
	end := t.cur
	t.left()
	t.text = append(t.text[:t.cur:t.cur], t.text[end:]...)
}

func (t *textArea) del() {
	start := t.cur
	t.right()
	t.text = append(t.text[:start:start], t.text[t.cur:]...)
	t.cur = start
}

// row is one visual row of the text: runes [from, to), wrapped at a space
// when the row has one, else at the width.
type row struct{ from, to int }

func (t *textArea) rows(width int) []row {
	width = max(1, width)
	var out []row
	from := 0
	for from <= len(t.text) {
		nl := from
		for nl < len(t.text) && t.text[nl] != '\n' {
			nl++
		}
		line := from
		for {
			end, w, space := line, 0, -1
			g := uniseg.NewGraphemes(string(t.text[line:nl]))
			for g.Next() {
				cw := g.Width()
				if w+cw > width && end > line {
					break
				}
				if r := g.Runes(); len(r) == 1 && r[0] == ' ' {
					space = end + 1
				}
				w += cw
				end += len(g.Runes())
			}
			if end < nl && space > line { // wrap after the last space
				end = space
			}
			out = append(out, row{line, end})
			if end >= nl {
				break
			}
			line = end
		}
		from = nl + 1
	}
	return out
}

// cursorAt is the cursor's visual row and its cell column.
func (t *textArea) cursorAt(width int) (int, int) {
	rs := t.rows(width)
	for i, r := range rs {
		next := len(t.text) + 1
		if i+1 < len(rs) {
			next = rs[i+1].from
		}
		if t.cur >= r.from && (t.cur < next || i == len(rs)-1) && t.cur <= r.to {
			return i, Width(string(t.text[r.from:t.cur]))
		}
	}
	return max(0, len(rs)-1), 0
}

// vertical moves the cursor a row up or down, to the nearest column.
func (t *textArea) vertical(width, delta int) {
	rs := t.rows(width)
	i, col := t.cursorAt(width)
	j := clamp(i+delta, 0, len(rs)-1)
	if j == i {
		return
	}
	r := rs[j]
	t.cur = r.from
	w := 0
	g := uniseg.NewGraphemes(string(t.text[r.from:r.to]))
	for g.Next() {
		if w+g.Width() > col {
			break
		}
		w += g.Width()
		t.cur += len(g.Runes())
	}
}

// home and end are the current visual row's ends.
func (t *textArea) home(width int) {
	i, _ := t.cursorAt(width)
	t.cur = t.rows(width)[i].from
}

func (t *textArea) end(width int) {
	i, _ := t.cursorAt(width)
	r := t.rows(width)[i]
	t.cur = r.to
	if r.to > r.from && r.to < len(t.text) && t.text[r.to-1] == ' ' && t.text[r.to] != '\n' {
		t.cur = r.to - 1 // a wrapped row ends before its space
	}
}

// keepCursor scrolls so the cursor's row is among the height rows shown.
func (t *textArea) keepCursor(width, height int) {
	i, _ := t.cursorAt(width)
	switch {
	case i < t.top:
		t.top = i
	case height > 0 && i >= t.top+height:
		t.top = i - height + 1
	}
	t.top = clamp(t.top, 0, max(0, len(t.rows(width))-height))
}

// clickAt puts the cursor at the cell (col, row) of the shown rows.
func (t *textArea) clickAt(width, col, rowIdx int) {
	rs := t.rows(width)
	j := clamp(t.top+rowIdx, 0, len(rs)-1)
	r := rs[j]
	t.cur = r.from
	w := 0
	g := uniseg.NewGraphemes(string(t.text[r.from:r.to]))
	for g.Next() {
		if w+g.Width() > col {
			break
		}
		w += g.Width()
		t.cur += len(g.Runes())
	}
}

// lines are the height rows shown from top, the cursor cell reversed when
// focused.
func (t *textArea) lines(width, height int, focused bool, placeholder string) []Line {
	out := make([]Line, height)
	if len(t.text) == 0 {
		if height > 0 {
			l := Line{}
			if focused {
				l = append(l, Seg{Text: " ", Role: RoleText, Reverse: true})
			}
			out[0] = append(l, Seg{Text: placeholder, Role: RoleMuted})
		}
		return out
	}
	rs := t.rows(width)
	ci, _ := t.cursorAt(width)
	for i := 0; i < height && t.top+i < len(rs); i++ {
		r := rs[t.top+i]
		s := string(t.text[r.from:r.to])
		if !focused || t.top+i != ci {
			out[i] = Line{{Text: s, Role: RoleText}}
			continue
		}
		// The cursor's cluster reversed (a space past the row's end).
		before := string(t.text[r.from:t.cur])
		rest := string(t.text[t.cur:r.to])
		at, size := " ", 0
		if rest != "" {
			at, size = firstCluster(rest)
			if at == "\n" {
				at = " "
			}
		}
		out[i] = Line{{Text: before, Role: RoleText}, {Text: at, Role: RoleText, Reverse: true}, {Text: rest[size:], Role: RoleText}}
	}
	return out
}

// firstCluster is s's first grapheme cluster and its size in bytes.
func firstCluster(s string) (string, int) {
	c, _, _, _ := uniseg.FirstGraphemeClusterInString(s, -1)
	return c, len(c)
}

// composeState is the open window.
type composeState struct {
	kind               composeKind
	to                 textArea // the recipient field: "@agent", "@all", or empty for a broadcast
	onTo               bool     // the recipient field has the keyboard
	area               textArea
	pick               int    // the highlighted recipient completion
	agent              string // composeAnswer: whose question
	askID, qid, header string
}

// composeDraft is the message window's draft, kept while it is closed or
// shows an answer.
type composeDraft struct{ to, body string }

func newField(s string, limit int) textArea {
	t := textArea{limit: limit}
	t.set(s)
	return t
}

// recipient is the field read: "" (a broadcast), "all", or a name.
func (c *composeState) recipient() string {
	return strings.TrimPrefix(strings.TrimSpace(c.to.value()), "@")
}

// openMessageCompose opens the message window on its draft; with no draft,
// on the line's text (Ctrl+O), else empty with to as recipient.
func (m *Model) openMessageCompose(lineText, to string) {
	d := m.ui.msgCompose
	switch {
	case lineText != "":
		d.to, d.body = splitRecipient(lineText)
	case d.body == "" && d.to == "" && to != "":
		d.to = "@" + to
	}
	m.ui.compose = &composeState{kind: composeMessage, to: newField(d.to, 64), area: newField(d.body, bus.CmdMaxRunes),
		onTo: d.to == "" && d.body == "" && to == "" && lineText == ""}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	if m.ui.line != lineNone {
		m.leaveLine(true)
	}
	m.layoutCompose()
}

// openAnswerCompose opens the window for a free answer to q, on its draft
// (or text when given).
func (m *Model) openAnswerCompose(a bus.Ask, q bus.AskQuestionState, text string) {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		m.saveMessageCompose()
	}
	key := a.ID + "|" + q.ID
	if text == "" {
		text = m.ui.askDrafts[key]
	}
	m.ui.compose = &composeState{kind: composeAnswer, area: newField(cutRunes(text, answerMaxRunes), answerMaxRunes),
		agent: a.From, askID: a.ID, qid: q.ID, header: q.Header}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	m.layoutCompose()
}

func (m *Model) saveMessageCompose() {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		m.ui.msgCompose = composeDraft{to: c.to.value(), body: c.area.value()}
	}
}

// closeCompose keeps the draft: a message's for the window, an answer's for
// its question.
func (m *Model) closeCompose() {
	c := m.ui.compose
	if c == nil {
		return
	}
	if c.kind == composeMessage {
		m.saveMessageCompose()
	} else if t := c.area.value(); strings.TrimSpace(t) != "" {
		m.ui.askDrafts[c.askID+"|"+c.qid] = t
	} else {
		delete(m.ui.askDrafts, c.askID+"|"+c.qid)
	}
	m.ui.compose = nil
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
}

// sentCompose closes the window after its send, its draft spent.
func (m *Model) sentCompose() {
	if c := m.ui.compose; c != nil {
		if c.kind == composeAnswer {
			delete(m.ui.askDrafts, c.askID+"|"+c.qid)
		} else {
			m.ui.msgCompose = composeDraft{}
		}
	}
	m.ui.compose = nil
}

// composeHasDraft: the message window holds a draft (shown or not).
func (m *Model) composeHasDraft() bool {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		return strings.TrimSpace(c.area.value()) != ""
	}
	return strings.TrimSpace(m.ui.msgCompose.body) != ""
}

// composeRects are the window's frame and its text area on this screen.
func (m *Model) composeRects() (frame, text Rect) {
	f := composeRect(m.width, m.height, m.layout.Header.H)
	return f, Rect{f.X + 2, f.Y + 2, max(1, f.W-4), max(1, f.H-4)}
}

// layoutCompose keeps the cursor in view after a change or a resize.
func (m *Model) layoutCompose() {
	if c := m.ui.compose; c != nil {
		_, tr := m.composeRects()
		c.area.keepCursor(tr.W, tr.H)
	}
}

// toCompletionsOf are the recipient field's completions while it is focused.
func (m *Model) toCompletionsOf(c *composeState) []string {
	if c.kind != composeMessage || !c.onTo {
		return nil
	}
	return toCompletions(c.to.value(), m.names(m.ui.showAll))
}

// composeKey is a key in the window (ModeCompose).
func (m *Model) composeKey(act Action, k Key) tea.Cmd {
	c := m.ui.compose
	_, tr := m.composeRects()
	field := &c.area
	width := tr.W
	if c.onTo && c.kind == composeMessage {
		field, width = &c.to, 1<<20
	}
	// The recipient's completions are listed: Tab and Enter take one, the
	// arrows move along them; none leaves the field.
	if cands := m.toCompletionsOf(c); len(cands) > 0 {
		switch {
		case k.Code == keyTab || k.Code == keyEnter:
			c.to.set(strings.TrimSpace(cands[min(c.pick, len(cands)-1)]))
			c.pick, c.onTo = 0, false
			return nil
		case k.Code == keyUp || k.Code == keyDown:
			step := 1
			if k.Code == keyUp {
				step = len(cands) - 1
			}
			c.pick = (c.pick + step) % len(cands)
			return nil
		}
	}
	switch act {
	case ActSend:
		return m.composeSend()
	case ActComposeField:
		switch {
		case c.kind != composeMessage:
		case !c.onTo && m.prefixToRecipient(c):
		default:
			c.onTo = !c.onTo
		}
		return nil
	case ActComposeAsk:
		tgt, body := c.recipient(), normalizeCompose(c.area.value())
		if c.kind == composeMessage && m.ui.askDraft != "" && m.ui.askDraft == draftKey(tgt, body) {
			m.saveMessageCompose()
			m.ui.compose = nil
			cmd := m.answerInPanel(tgt, body, tgt != "")
			if m.ui.askChoose == nil {
				m.ui.msgCompose = composeDraft{}
			}
			return cmd
		}
		return nil
	case ActClose:
		m.closeCompose()
		return nil
	}
	switch {
	case k.Code == keyEnter && field == &c.to: // the recipient is done: to the text
		c.onTo = false
	case k.Code == keyEnter, k.Code == keyRune && !k.Alt:
		s := "\n"
		if k.Code == keyRune {
			s = string(k.Rune)
		}
		if field.insert(s) > 0 {
			m.ui.flash, m.ui.flashErr = fmt.Sprintf("limit %d characters", field.limit), true
		}
		c.pick = 0
	case k.Code == keyBackspace:
		field.backspace()
		c.pick = 0
	case k.Code == keyDelete:
		field.del()
	case k.Code == keyLeft:
		field.left()
	case k.Code == keyRight:
		field.right()
	case k.Code == keyUp:
		field.vertical(width, -1)
	case k.Code == keyDown:
		field.vertical(width, 1)
	case k.Code == keyHome:
		field.home(width)
	case k.Code == keyEnd:
		field.end(width)
	}
	m.layoutCompose()
	return nil
}

// pasteCompose cuts a paste to what the limit leaves and gives it to the
// focused field; the recipient field takes one line only.
func (m *Model) pasteCompose(s string) {
	c := m.ui.compose
	if c.onTo && c.kind == composeMessage {
		s, _, _ = strings.Cut(s, "\n")
		c.to.insert(s)
		return
	}
	if n := c.area.insert(s); n > 0 {
		m.ui.flash, m.ui.flashErr = fmt.Sprintf("limit %d characters: %d dropped from the paste", c.area.limit, n), true
	}
	m.layoutCompose()
}

// prefixToRecipient: a text that is only "@prefix" becomes the recipient on
// Tab, completed when one name matches; the text itself is never read as a
// recipient when sending.
func (m *Model) prefixToRecipient(c *composeState) bool {
	t := c.area.value()
	cands := recipientCompletions(t, m.names(m.ui.showAll))
	if len(cands) == 0 {
		return false
	}
	c.area.set("")
	if len(cands) == 1 {
		c.to.set(strings.TrimSpace(cands[0]))
		return true
	}
	c.to.set(t)
	c.onTo, c.pick = true, 0
	return true
}

// composeSend sends the window's text through the one send path, or answers
// its question.
func (m *Model) composeSend() tea.Cmd {
	c := m.ui.compose
	body := normalizeCompose(c.area.value())
	if c.kind == composeAnswer {
		if body == "" {
			m.ui.flash, m.ui.flashErr = "nothing to send", true
			return nil
		}
		a, ok := findAsk(m.snap.asks, c.askID)
		if ok {
			a = m.withLocalAnswers(a)
		}
		for _, q := range a.Questions {
			if ok && q.ID == c.qid && q.Answer == nil && a.State == "open" {
				if m.pauseGate("answer|"+c.askID+"|"+c.qid+"|"+body, pauseRetry{kind: retryCompose}) {
					return nil
				}
				key := a.ID + "|" + q.ID
				if m.rd.sending[key] {
					return nil
				}
				m.rd.sending[key] = true
				return m.answerCmd(a, q, "", body)
			}
		}
		m.ui.flash, m.ui.flashErr = "this question is no longer open", true
		return nil
	}
	tgt := c.recipient()
	if tgt != "" && body != "" && m.pauseGate(draftKey(tgt, body), pauseRetry{kind: retryCompose}) {
		return nil
	}
	return m.send(tgt, body, tgt != "", true)
}

// composeClick: on the recipient row it focuses the field, in the text it
// focuses it and moves the cursor there; a click outside the window closes it
// (the draft kept).
func (m *Model) composeClick(x, y int) {
	c := m.ui.compose
	f, tr := m.composeRects()
	switch {
	case !f.contains(x, y):
		m.closeCompose()
	case y == f.Y+1 && c.kind == composeMessage:
		c.onTo = true
	case tr.contains(x, y):
		c.onTo = false
		c.area.clickAt(tr.W, x-tr.X, y-tr.Y)
	}
}

// kindWord is the message's kind after its recipient.
func (m *Model) kindWord(c *composeState) (string, Role) {
	switch r := c.recipient(); {
	case r == "":
		return "broadcast", RoleAttention
	case r == "all" || m.snap.agents[r] != nil:
		return "directive", RoleMuted
	default:
		return "unknown recipient", RoleAttention
	}
}

// drawCompose draws the window over the screen: its frame, the recipient row
// (a message) or the question (an answer), the text, and the flash or the
// hints with the counter.
func (m Model) drawCompose(g *grid) {
	c := m.ui.compose
	f, tr := m.composeRects()
	if f.W < 12 || f.H < 5 {
		return
	}
	for y := f.Y; y < f.Y+f.H; y++ { // the window covers what is under it
		drawLine(g, f.X, y, f.W, nil, false)
	}
	title := "Compose"
	if c.kind == composeAnswer {
		title = "Answer " + GlyphNone + " " + Preview(c.agent)
	}
	rows := c.area.rows(tr.W)
	drawFrame(g, f.X, f.Y, f.W, f.H, Frame{Title: Line{{Text: title, Role: RoleText}}, Focused: true, Hue: panelHue[PanelActivity],
		Scroll: ScrollState{Total: len(rows), Top: c.area.top, Visible: tr.H}})
	iw := f.W - 4
	if c.kind == composeMessage {
		word, role := m.kindWord(c)
		tail := Line{{Text: " " + GlyphNone + " ", Role: RoleMuted}, {Text: word, Role: role}}
		fw := max(10, iw-tail.Width()-4)
		to := Line{{Text: "to: ", Role: RoleMuted}}
		switch {
		case c.to.value() == "" && !c.onTo:
			to = append(to, Seg{Text: "@agent, @all, or empty for a broadcast", Role: RoleMuted})
		case c.onTo:
			to = append(to, Seg{Text: Preview(c.to.value()), Role: RoleText}, Seg{Text: " ", Role: RoleText, Reverse: true})
		default:
			to = append(to, Seg{Text: Preview(c.to.value()), Role: RoleText})
		}
		drawLine(g, f.X+2, f.Y+1, fw+4, to, false)
		drawLine(g, f.X+2+fw+4, f.Y+1, iw-fw-4, tail, false)
	} else {
		drawLine(g, f.X+2, f.Y+1, iw, Line{{Text: "Answer to ", Role: RoleMuted}, {Text: Preview(c.header), Role: RoleAccent}}, false)
	}
	placeholder := "Enter: new line " + GlyphNone + " Ctrl+S: send"
	if c.kind == composeAnswer {
		placeholder = "your answer " + GlyphNone + " Ctrl+S: send"
	}
	for i, l := range c.area.lines(tr.W, tr.H, !c.onTo || c.kind != composeMessage, placeholder) {
		drawLine(g, tr.X, tr.Y+i, tr.W, l, false)
	}
	if cands := m.toCompletionsOf(c); len(cands) > 0 { // under the recipient row, over the text
		l := Line{}
		for i, s := range cands {
			l = append(l, Seg{Text: " " + strings.TrimSpace(s) + " ", Role: RoleAccent, Pill: i == c.pick}, Seg{Text: " "})
		}
		drawLine(g, tr.X, tr.Y, tr.W, l, false)
	}
	n := utf8.RuneCountInString(c.area.value())
	counter := fmt.Sprintf("%d / %d", n, c.area.limit)
	crole := RoleMuted
	if n >= c.area.limit {
		crole = RoleAttention
	}
	var foot Line
	if m.ui.flash != "" {
		role := RoleOK
		if m.ui.flashErr {
			role = RoleError
		}
		foot = Line{{Text: m.ui.flash, Role: role}}
	} else {
		for _, b := range bindings(ModeCompose) {
			if b.Hint == "" || (b.Act == ActComposeField && c.kind != composeMessage) {
				continue
			}
			if len(foot) > 0 {
				foot = append(foot, Seg{Text: "   "})
			}
			foot = append(foot, Seg{Text: b.Keys, Role: RoleAccent, Bold: true}, Seg{Text: " " + b.Hint, Role: RoleMuted})
		}
	}
	foot = FitLine(foot, max(0, iw-Width(counter)-1))
	foot = append(foot, Seg{Text: strings.Repeat(" ", max(1, iw-foot.Width()-Width(counter)))}, Seg{Text: counter, Role: crole})
	drawLine(g, f.X+2, f.Y+f.H-2, iw, foot, false)
}
