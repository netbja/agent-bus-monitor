package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The compose window (busmon Charm spec, "Input"; v0.16's window): a message
// with its recipient, or a free answer to one question. Its text area is our
// own, over Line: bubbles' textarea counts its limit in cells, so a cap of N
// runes stopped at N/2 wide runes typed one by one (the step-b cap test, the person's
// decision 4). The cap is enforced in Update, in runes, never mid-rune.

// textArea is the window's text and cursor, in runes; top is the first
// visual row shown. Its rows are wrapped once per change, in one pass, by
// prepare on the update path; View reads them (rowsAt).
type textArea struct {
	text  []rune
	cur   int
	top   int
	limit int

	rev                int   // bumped by every change of text
	cached             []row // the rows of revision cachedRev at width cachedW
	cachedRev, cachedW int
}

// wrapCalls counts the wraps: a key wraps the text once (the cost test).
var wrapCalls int

func (t *textArea) value() string { return string(t.text) }

func (t *textArea) changed() { t.rev++ }

// insert puts s at the cursor, cut to what the limit leaves; it reports the
// runes dropped. s is made displayable first: the measure is one method.
func (t *textArea) insert(s string) (dropped int) {
	s = agreeWidths(DetailText(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")))
	rs := []rune(s)
	if room := t.limit - len(t.text); len(rs) > room {
		dropped, rs = len(rs)-max(room, 0), rs[:max(room, 0)]
	}
	if len(rs) == 0 {
		return dropped
	}
	t.text = append(t.text[:t.cur:t.cur], append(rs, t.text[t.cur:]...)...)
	t.cur += len(rs)
	t.changed()
	return dropped
}

func (t *textArea) set(s string) {
	t.text, t.cur, t.top = nil, 0, 0
	t.changed()
	t.insert(s)
}

// row is one visual row of the text: runes [from, to), wrapped after its
// last space when it has one, else at the width.
type row struct{ from, to int }

// wrap is the text's rows at width, in one grapheme pass.
func wrap(text []rune, width int) []row {
	wrapCalls++
	width = max(1, width)
	var out []row
	from, pos, w := 0, 0, 0
	space, afterSpace := -1, 0 // the rune after the row's last space, and the width since
	g := uniseg.NewGraphemes(string(text))
	for g.Next() {
		rs := g.Runes()
		if len(rs) == 1 && rs[0] == '\n' {
			out = append(out, row{from, pos})
			pos++
			from, w, space, afterSpace = pos, 0, -1, 0
			continue
		}
		cw := g.Width()
		if w+cw > width && pos > from {
			if space > from {
				out = append(out, row{from, space})
				from, w = space, afterSpace
			} else {
				out = append(out, row{from, pos})
				from, w = pos, 0
			}
			space, afterSpace = -1, w
			if w+cw > width && pos > from { // what followed the space still leaves no room
				out = append(out, row{from, pos})
				from, w, afterSpace = pos, 0, 0
			}
		}
		w += cw
		afterSpace += cw
		pos += len(rs)
		if len(rs) == 1 && rs[0] == ' ' {
			space, afterSpace = pos, 0
		}
	}
	return append(out, row{from, pos})
}

// prepare wraps the text at width when it changed (the update path).
func (t *textArea) prepare(width int) []row {
	if t.cached == nil || t.cachedRev != t.rev || t.cachedW != width {
		t.cached, t.cachedRev, t.cachedW = wrap(t.text, width), t.rev, width
	}
	return t.cached
}

// rowsAt is the rows at width without storing them: View's read.
func (t *textArea) rowsAt(width int) []row {
	if t.cached != nil && t.cachedRev == t.rev && t.cachedW == width {
		return t.cached
	}
	return wrap(t.text, width)
}

// rowOf is the index of the row holding the cursor.
func rowOf(rs []row, cur int) int {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].from > cur }) - 1
	return clamp(i, 0, len(rs)-1)
}

// cursorAt is the cursor's visual row and its cell column.
func (t *textArea) cursorAt(rs []row) (int, int) {
	i := rowOf(rs, t.cur)
	return i, Width(string(t.text[rs[i].from:t.cur]))
}

// clusterStarts are the rune offsets where the clusters of runes [from, to)
// start, and to.
func (t *textArea) clusterStarts(from, to int) []int {
	out := []int{from}
	n := from
	g := uniseg.NewGraphemes(string(t.text[from:to]))
	for g.Next() {
		n += len(g.Runes())
		out = append(out, n)
	}
	return out
}

// left and right move by cluster, scanning the cursor's row only.
func (t *textArea) left(rs []row) {
	if t.cur == 0 {
		return
	}
	r := rs[rowOf(rs, t.cur)]
	if t.cur == r.from { // the end of the row above (its newline or last cluster)
		r = rs[rowOf(rs, t.cur-1)]
		if t.cur-1 >= r.to {
			t.cur--
			return
		}
	}
	cs := t.clusterStarts(r.from, min(t.cur, r.to))
	t.cur = cs[max(0, len(cs)-2)]
}

func (t *textArea) right(rs []row) {
	if t.cur >= len(t.text) {
		return
	}
	r := rs[rowOf(rs, t.cur)]
	if t.cur >= r.to { // a newline
		t.cur++
		return
	}
	t.cur = t.clusterStarts(t.cur, r.to)[1]
}

func (t *textArea) backspace(rs []row) {
	end := t.cur
	t.left(rs)
	if t.cur < end {
		t.text = append(t.text[:t.cur:t.cur], t.text[end:]...)
		t.changed()
	}
}

func (t *textArea) del(rs []row) {
	start := t.cur
	t.right(rs)
	if t.cur > start {
		t.text = append(t.text[:start:start], t.text[t.cur:]...)
		t.changed()
	}
	t.cur = start
}

// colIn is the rune offset of cell column col in row r.
func (t *textArea) colIn(r row, col int) int {
	cur, w := r.from, 0
	g := uniseg.NewGraphemes(string(t.text[r.from:r.to]))
	for g.Next() {
		if w+g.Width() > col {
			break
		}
		w += g.Width()
		cur += len(g.Runes())
	}
	return cur
}

// vertical moves the cursor a row up or down, to the nearest column.
func (t *textArea) vertical(rs []row, delta int) {
	i, col := t.cursorAt(rs)
	if j := clamp(i+delta, 0, len(rs)-1); j != i {
		t.cur = t.colIn(rs[j], col)
	}
}

// home and end are the current visual row's ends.
func (t *textArea) home(rs []row) { t.cur = rs[rowOf(rs, t.cur)].from }

func (t *textArea) end(rs []row) {
	r := rs[rowOf(rs, t.cur)]
	t.cur = r.to
	if r.to > r.from && r.to < len(t.text) && t.text[r.to-1] == ' ' && t.text[r.to] != '\n' {
		t.cur = r.to - 1 // a wrapped row ends before its space
	}
}

// keepCursor scrolls so the cursor's row is among the height rows shown.
func (t *textArea) keepCursor(width, height int) {
	rs := t.prepare(width)
	i, _ := t.cursorAt(rs)
	switch {
	case i < t.top:
		t.top = i
	case height > 0 && i >= t.top+height:
		t.top = i - height + 1
	}
	t.top = clamp(t.top, 0, max(0, len(rs)-height))
}

// clickAt puts the cursor at the cell (col, row) of the shown rows.
func (t *textArea) clickAt(width, col, rowIdx int) {
	rs := t.prepare(width)
	t.cur = t.colIn(rs[clamp(t.top+rowIdx, 0, len(rs)-1)], col)
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
	rs := t.rowsAt(width)
	ci, _ := t.cursorAt(rs)
	for i := 0; i < height && t.top+i < len(rs); i++ {
		r := rs[t.top+i]
		if !focused || t.top+i != ci {
			out[i] = Line{{Text: string(t.text[r.from:r.to]), Role: RoleText}}
			continue
		}
		// The cursor's cluster reversed (a space past the row's end).
		before, rest := string(t.text[r.from:t.cur]), string(t.text[t.cur:r.to])
		at, size := " ", 0
		if rest != "" {
			at, size = firstCluster(rest)
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
	rs := field.prepare(width) // the rows the cursor moves on: wrapped once per change
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
		field.backspace(rs)
		c.pick = 0
	case k.Code == keyDelete:
		field.del(rs)
	case k.Code == keyLeft:
		field.left(rs)
	case k.Code == keyRight:
		field.right(rs)
	case k.Code == keyUp:
		field.vertical(rs, -1)
	case k.Code == keyDown:
		field.vertical(rs, 1)
	case k.Code == keyHome:
		field.home(rs)
	case k.Code == keyEnd:
		field.end(rs)
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
	rows := c.area.rowsAt(tr.W)
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
