package main

import (
	"fmt"
	"maps"
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
	text   []rune
	cur    int
	top    int
	limit  int
	anchor int  // the selection's other end, while marking
	mark   bool // a selection is being made (Shift with a move)

	rev                int   // bumped by every change of text
	cached             []row // the rows of revision cachedRev at width cachedW
	cachedRev, cachedW int

	// The edit history (compose-undo): the states before each step, newest
	// last, at most undoMax; what undo took back, for redo; run is the kind of
	// the last step while it can still grow (a run of typed characters, or of
	// deletions), "" once anything else happened.
	undo, redo []areaState
	run        string

	// A multi-line paste is shown as a placeholder, "[Pasted text #n +k
	// lines]", and kept here whole: value gives it back in its place. Numbered
	// per field from 1; set starts again. An entry is never removed, so undo
	// brings a removed placeholder back with its text.
	pasted map[string]string
	pastes int
}

// undoMax bounds the edit history: the oldest steps go first.
const undoMax = 100

// areaState is what undo restores: the text, the cursor and the selection.
type areaState struct {
	text        []rune
	cur, anchor int
	mark        bool
}

func (t *textArea) state() areaState {
	return areaState{text: append([]rune(nil), t.text...), cur: t.cur, anchor: t.anchor, mark: t.mark}
}

func (t *textArea) restore(st areaState) {
	t.text, t.cur, t.anchor, t.mark = st.text, st.cur, st.anchor, st.mark
	t.changed()
}

// edit runs one edit and keeps the state before it as a step when the text
// changed. A step of the same run (kind "type" or "delete") as the last one
// is joined to it; any other kind, "" included, ends the run.
func (t *textArea) edit(kind string, f func()) {
	before, rev := t.state(), t.rev
	f()
	if t.rev == rev {
		return
	}
	if kind == "" || kind != t.run {
		t.undo = append(t.undo, before)
		if len(t.undo) > undoMax {
			t.undo = append(t.undo[:0:0], t.undo[len(t.undo)-undoMax:]...)
		}
	}
	t.redo, t.run = nil, kind
}

// endRun keeps the next edit from joining the last step (a move, a click).
func (t *textArea) endRun() { t.run = "" }

// undoStep and redoStep move one step through the history; at its end they
// do nothing and report false.
func (t *textArea) undoStep() bool {
	if len(t.undo) == 0 {
		return false
	}
	t.redo = append(t.redo, t.state())
	t.restore(t.undo[len(t.undo)-1])
	t.undo, t.run = t.undo[:len(t.undo)-1], ""
	return true
}

func (t *textArea) redoStep() bool {
	if len(t.redo) == 0 {
		return false
	}
	t.undo = append(t.undo, t.state())
	t.restore(t.redo[len(t.redo)-1])
	t.redo, t.run = t.redo[:len(t.redo)-1], ""
	return true
}

// wrapCalls counts the wraps: a key wraps the text once (the cost test).
var wrapCalls int

// value is the text with each paste in its placeholder's place.
func (t *textArea) value() string {
	s := string(t.text)
	for ph, text := range t.pasted {
		s = strings.ReplaceAll(s, ph, text)
	}
	return s
}

// size is the text's length in runes, pastes counted whole: what the limit
// bounds.
func (t *textArea) size() int {
	n := len(t.text)
	for ph, text := range t.pasted {
		if c := strings.Count(string(t.text), ph); c > 0 {
			n += c * (utf8.RuneCountInString(text) - utf8.RuneCountInString(ph))
		}
	}
	return n
}

// placeholderAt is the length in runes of the placeholder that ends at the
// cursor (before) or starts at it (!before), 0 when none does.
func (t *textArea) placeholderAt(before bool) int {
	for ph := range t.pasted {
		r := []rune(ph)
		if before && t.cur >= len(r) && string(t.text[t.cur-len(r):t.cur]) == ph ||
			!before && t.cur+len(r) <= len(t.text) && string(t.text[t.cur:t.cur+len(r)]) == ph {
			return len(r)
		}
	}
	return 0
}

// insertPaste is a paste: one with a line break goes in as a placeholder
// (the person: Claude Code's "[Pasted text #1 +9 lines]"), any other as
// typed text. It reports the runes the limit dropped.
func (t *textArea) insertPaste(s string) (dropped int) {
	s = agreeWidths(DetailText(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")))
	if !strings.Contains(s, "\n") {
		return t.insert(s)
	}
	t.cutSelection()
	rs := []rune(s)
	if room := max(t.limit-t.size(), 0); len(rs) > room {
		dropped, rs = len(rs)-room, rs[:room]
	}
	s = string(rs)
	if !strings.Contains(s, "\n") {
		return dropped + t.insert(s)
	}
	t.pastes++
	ph := fmt.Sprintf("[Pasted text #%d +%d lines]", t.pastes, strings.Count(s, "\n"))
	if t.pasted == nil {
		t.pasted = map[string]string{}
	}
	t.pasted[ph] = s
	t.text = append(t.text[:t.cur:t.cur], append([]rune(ph), t.text[t.cur:]...)...)
	t.cur += len([]rune(ph))
	t.changed()
	return dropped
}

func (t *textArea) changed() { t.rev++ }

// selection is the selected runes [from, to); ok is false when none is.
func (t *textArea) selection() (from, to int, ok bool) {
	if !t.mark || t.anchor == t.cur {
		return 0, 0, false
	}
	return min(t.anchor, t.cur), max(t.anchor, t.cur), true
}

// unmark ends the selection, the text kept.
func (t *textArea) unmark() { t.mark = false }

// cutSelection removes the selected runes and puts the cursor where they
// were; it reports whether there was a selection.
func (t *textArea) cutSelection() bool {
	from, to, ok := t.selection()
	t.mark = false
	if !ok {
		return false
	}
	t.text = append(t.text[:from:from], t.text[to:]...)
	t.cur = from
	t.changed()
	return true
}

// insert puts s at the cursor, cut to what the limit leaves; it reports the
// runes dropped; a selection is replaced. s is made displayable first: the
// measure is one method.
func (t *textArea) insert(s string) (dropped int) {
	t.cutSelection()
	s = agreeWidths(DetailText(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    ")))
	rs := []rune(s)
	if room := t.limit - t.size(); len(rs) > room {
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

// set replaces the text whole and starts a new history: what it replaces
// (the window opened, sent, a completion) is no step to go back to.
func (t *textArea) set(s string) {
	t.text, t.cur, t.top, t.mark = nil, 0, 0, false
	t.undo, t.redo, t.run = nil, nil, ""
	t.pasted, t.pastes = nil, 0
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
	if n := t.placeholderAt(true); n > 0 { // a paste's placeholder is one step
		t.cur -= n
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
	if n := t.placeholderAt(false); n > 0 {
		t.cur += n
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
	if t.cutSelection() {
		return
	}
	end := t.cur
	t.left(rs)
	if t.cur < end {
		t.text = append(t.text[:t.cur:t.cur], t.text[end:]...)
		t.changed()
	}
}

func (t *textArea) del(rs []row) {
	if t.cutSelection() {
		return
	}
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
	t.mark, t.run = false, "" // a click ends a run, as a move does
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
	if from, to, ok := t.selection(); ok && focused { // the selection reversed, the cursor its edge
		for i := 0; i < height && t.top+i < len(rs); i++ {
			r := rs[t.top+i]
			a, b := clamp(from, r.from, r.to), clamp(to, r.from, r.to)
			out[i] = Line{{Text: string(t.text[r.from:a]), Role: RoleText}, {Text: string(t.text[a:b]), Role: RoleText, Reverse: true},
				{Text: string(t.text[b:r.to]), Role: RoleText}}
		}
		return out
	}
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
	orphan             bool // composeAnswer: its question changed or closed; the next send makes it a message
	// at is the @ popup over the text, when open (compose_at_popup.go);
	// atLists the listings this window read, dropped with it (§2.1).
	at      *atPopup
	atLists map[string]*atList
}

// composeDraft is the message window's draft, kept while it is closed or
// shows an answer.
type composeDraft struct{ to, body string }

func newField(s string, limit int) textArea {
	t := textArea{limit: limit}
	t.set(s)
	return t
}

// switchedField ends both fields' edit runs: typing after the keyboard went
// to the other field and back is a new step (reviewer-codex).
func (c *composeState) switchedField() {
	c.area.endRun()
	c.to.endRun()
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
	if text == "" {
		text = m.answerDraft(a.ID, q.ID)
	}
	m.ui.compose = &composeState{kind: composeAnswer, area: newField(cutRunes(text, answerMaxRunes), answerMaxRunes),
		agent: a.From, askID: a.ID, qid: q.ID, header: q.Header}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	m.layoutCompose()
}

// answerDraft is the draft of question qid of ask id, or of the same question
// in an ask it replaced, unchanged (a draft left before an ask --replace); the
// draft found moves to id.
func (m *Model) answerDraft(id, qid string) string {
	if d, ok := m.ui.askDrafts[id+"|"+qid]; ok {
		return d
	}
	for _, a := range m.snap.asks {
		if a.State != "replaced" || a.ReplacedBy == "" {
			continue
		}
		d, ok := m.ui.askDrafts[a.ID+"|"+qid]
		if !ok {
			continue
		}
		if to, _, open := followAsk(m.snap.asks, a.ID, qid); open && to.ID == id {
			delete(m.ui.askDrafts, a.ID+"|"+qid)
			m.ui.askDrafts[id+"|"+qid] = d
			return d
		}
	}
	return ""
}

func (m *Model) saveMessageCompose() {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		m.ui.msgCompose = composeDraft{to: c.to.value(), body: c.area.value()}
		if strings.TrimSpace(m.ui.msgCompose.body) == "" {
			m.nextHeldDraft()
		}
	}
}

// keepMessageDraft keeps d as the message draft, or, while another is kept,
// after it: a draft is never overwritten.
func (m *Model) keepMessageDraft(d composeDraft) {
	if strings.TrimSpace(m.ui.msgCompose.body) == "" {
		m.ui.msgCompose = d
		return
	}
	m.ui.heldDrafts = append(m.ui.heldDrafts, d)
}

// nextHeldDraft makes the next held draft the message draft, once the
// current one is spent or emptied.
func (m *Model) nextHeldDraft() {
	if len(m.ui.heldDrafts) == 0 {
		return
	}
	m.ui.msgCompose = m.ui.heldDrafts[0]
	m.ui.heldDrafts = m.ui.heldDrafts[1:]
}

// closeCompose keeps the draft: a message's for the window, an answer's for
// its question.
func (m *Model) closeCompose() {
	c := m.ui.compose
	if c == nil {
		return
	}
	if c.kind == composeIdea {
		m.ui.ideaDraft = c.area.value()
	} else if c.kind == composeMessage {
		m.saveMessageCompose()
	} else if t := c.area.value(); c.orphan && strings.TrimSpace(t) != "" {
		// Its question is gone, so no ask opens its draft again: it is kept
		// as a message to the asking agent.
		delete(m.ui.askDrafts, c.askID+"|"+c.qid)
		m.keepMessageDraft(composeDraft{to: "@" + c.agent, body: t})
		m.ui.flash, m.ui.flashErr = "your text is kept as a message draft to "+c.agent, false
	} else if strings.TrimSpace(t) != "" {
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
			m.nextHeldDraft()
		}
	}
	m.ui.compose = nil
}

// composeHasDraft: the message window holds a draft (shown or not).
func (m *Model) composeHasDraft() bool {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		return strings.TrimSpace(c.area.value()) != "" || len(m.ui.heldDrafts) > 0
	}
	return strings.TrimSpace(m.ui.msgCompose.body) != "" || len(m.ui.heldDrafts) > 0
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
			c.switchedField()
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
	case ActPasteImage:
		m.ui.flash, m.ui.flashErr = "reading the clipboard"+GlyphEllipsis, false
		return pasteImageCmd(m.cfg.clip, m.cfg.attachDir, maps.Clone(m.ui.pastedImages))
	case ActUndo, ActRedo:
		moved := field.undoStep
		if act == ActRedo {
			moved = field.redoStep
		}
		if !moved() {
			m.ui.flash, m.ui.flashErr = "nothing to "+map[Action]string{ActUndo: "undo", ActRedo: "redo"}[act], false
		}
		c.pick = 0
		m.layoutCompose()
		return nil
	case ActComposeField:
		c.area.unmark() // a selection belongs to the field it was made in: leaving it ends it
		c.to.unmark()
		c.switchedField()
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
		_, _, inText := c.area.selection()
		_, _, inTo := c.to.selection()
		if inText || inTo { // Esc first clears a selection, in either field (the person, reviewer-codex)
			c.area.unmark()
			c.to.unmark()
			return nil
		}
		m.closeCompose()
		return nil
	}
	rs := field.prepare(width) // the rows the cursor moves on: wrapped once per change
	switch k.Code {            // a move with Shift extends the selection from where it began; without, it ends it
	case keyLeft, keyRight, keyUp, keyDown, keyHome, keyEnd, keyPgUp, keyPgDn:
		if k.Shift && !field.mark {
			field.anchor, field.mark = field.cur, true
		} else if !k.Shift {
			field.unmark()
		}
		field.endRun() // a move ends a run of typing or deleting
	}
	switch {
	case k.Code == keyEnter && field == &c.to: // the recipient is done: to the text
		c.onTo = false
		c.switchedField()
	case k.Code == keyEnter, k.Code == keyRune: // a rune marked Alt too: a dead key's or AltGr's character (compose-dead-keys)
		s, kind := "\n", "" // a line break is a step of its own
		if k.Code == keyRune {
			s, kind = string(k.Rune), "type"
		}
		if _, _, sel := field.selection(); sel {
			kind = "" // typing over a selection is a step of its own
		}
		dropped := 0
		field.edit(kind, func() { dropped = field.insert(s) })
		if dropped > 0 {
			m.ui.flash, m.ui.flashErr = fmt.Sprintf("limit %d characters", field.limit), true
		}
		c.pick = 0
	case k.Code == keyBackspace:
		field.edit(deleteKind(field), func() { field.backspace(rs) })
		c.pick = 0
	case k.Code == keyDelete:
		field.edit(deleteKind(field), func() { field.del(rs) })
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
	case k.Code == keyPgUp: // a page of the window's rows
		_, tr := m.composeRects()
		field.vertical(rs, -max(1, tr.H))
	case k.Code == keyPgDn:
		_, tr := m.composeRects()
		field.vertical(rs, max(1, tr.H))
	}
	m.layoutCompose()
	return nil
}

// deleteKind is a Backspace or Del's step: one of a run of deletions, or
// its own when it removes a selection.
func deleteKind(t *textArea) string {
	if _, _, sel := t.selection(); sel {
		return ""
	}
	return "delete"
}

// pasteCompose cuts a paste to what the limit leaves and gives it to the
// focused field; the recipient field takes one line only. A paste is one
// step of the history, whatever its length (compose-undo).
func (m *Model) pasteCompose(s string) {
	c := m.ui.compose
	c.at = nil // a paste never opens the popup, nor types into its query
	if c.onTo && c.kind == composeMessage {
		s, _, _ = strings.Cut(s, "\n")
		c.to.edit("", func() { c.to.insert(s) })
		return
	}
	n := 0
	c.area.edit("", func() { n = c.area.insertPaste(s) })
	if n > 0 {
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
	if c.kind == composeIdea {
		return m.sendIdeaCompose(c)
	}
	if c.kind == composeAnswer {
		if body == "" {
			m.ui.flash, m.ui.flashErr = "nothing to send", true
			return nil
		}
		if c.orphan {
			m.answerToMessage(c)
			return nil
		}
		// The question may have moved to a replacing ask (ask --replace):
		// follow it as the bus does, while it is unchanged.
		a, _, ok := followAsk(m.snap.asks, c.askID, c.qid)
		if ok {
			a = m.withLocalAnswers(a)
		}
		for _, q := range a.Questions {
			if ok && q.ID == c.qid && q.Answer == nil {
				if a.ID != c.askID { // the text now answers the new ask: its draft follows
					delete(m.ui.askDrafts, c.askID+"|"+c.qid)
					c.askID = a.ID
				}
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
		c.orphan = true
		m.ui.flash, m.ui.flashErr = "this question changed or closed: your text is kept; send again to make it a message to "+c.agent, true
		return nil
	}
	tgt := c.recipient()
	if tgt != "" && body != "" && m.pauseGate(draftKey(tgt, body), pauseRetry{kind: retryCompose}) {
		return nil
	}
	return m.send(tgt, body, tgt != "", true)
}

// answerToMessage turns an answer whose question changed or closed into a
// message to the agent who asked, the text kept whole, for the person to read
// and send: a typed answer is never lost.
func (m *Model) answerToMessage(c *composeState) {
	delete(m.ui.askDrafts, c.askID+"|"+c.qid)
	earlier := strings.TrimSpace(m.ui.msgCompose.body) != ""
	if earlier { // the window now owns the message draft: the earlier one opens next
		m.ui.heldDrafts = append([]composeDraft{m.ui.msgCompose}, m.ui.heldDrafts...)
		m.ui.msgCompose = composeDraft{}
	}
	m.ui.compose = &composeState{kind: composeMessage, to: newField("@"+c.agent, 64), area: newField(c.area.value(), bus.CmdMaxRunes)}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	m.layoutCompose()
	m.ui.flash, m.ui.flashErr = "now a message to "+c.agent+": send it when ready", false
	if earlier {
		m.ui.flash += "; your earlier draft is kept and opens next"
	}
}

// composeClick: on the recipient row it focuses the field, in the text it
// focuses it and moves the cursor there; a click outside the window closes it
// (the draft kept).
func (m *Model) composeClick(x, y int) tea.Cmd {
	c := m.ui.compose
	f, tr := m.composeRects()
	switch {
	case !f.contains(x, y):
		m.closeCompose()
	case y == f.Y+f.H-2: // the hint row: a hint is a button
		counter := fmt.Sprintf("%d / %d", utf8.RuneCountInString(c.area.value()), c.area.limit)
		if c.at != nil {
			counter, _ = c.at.atFootRight()
			counter = atFit(counter, (f.W-4)/2)
		}
		_, hits := m.composeFoot(max(0, f.W-4-Width(counter)-1))
		for _, h := range hits {
			if x >= f.X+2+h.x && x < f.X+2+h.x+h.w {
				if c.at != nil { // the popup's hints are its keys
					return m.composeFileKey(h.act, Key{})
				}
				return m.composeKey(h.act, Key{})
			}
		}
	case y == f.Y+1 && c.kind == composeMessage:
		c.onTo = true
		c.area.unmark() // a focus-changing click ends a selection in either field (reviewer-codex)
		c.to.unmark()
	case tr.contains(x, y):
		c.onTo = false
		c.to.unmark()
		c.area.clickAt(tr.W, x-tr.X, y-tr.Y)
	}
	return nil
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
	switch c.kind {
	case composeAnswer:
		title = "Answer " + GlyphNone + " " + Preview(c.agent)
	case composeIdea:
		title = "New idea " + GlyphNone + " to discuss"
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
	} else if c.kind == composeIdea {
		drawLine(g, f.X+2, f.Y+1, iw, Line{{Text: "the first line is its title, the rest what it is", Role: RoleMuted}}, false)
	} else {
		drawLine(g, f.X+2, f.Y+1, iw, Line{{Text: "Answer to ", Role: RoleMuted}, {Text: Preview(c.header), Role: RoleAccent}}, false)
	}
	placeholder := "Enter: new line " + GlyphNone + " Ctrl+S: send"
	switch c.kind {
	case composeAnswer:
		placeholder = "your answer " + GlyphNone + " Ctrl+S: send"
	case composeIdea:
		placeholder = "its title, then what it is " + GlyphNone + " Ctrl+S: add"
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
	if c.at != nil {
		m.drawAtPopup(g)
	}
	n := utf8.RuneCountInString(c.area.value())
	counter := fmt.Sprintf("%d / %d", n, c.area.limit)
	crole := RoleMuted
	if n >= c.area.limit {
		crole = RoleAttention
	}
	if c.at != nil { // the popup's footer: its state or its count (§4.3)
		counter, crole = c.at.atFootRight()
		counter = atFit(counter, iw/2)
	}
	foot, _ := m.composeFoot(max(0, iw-Width(counter)-1))
	foot = append(foot, Seg{Text: strings.Repeat(" ", max(1, iw-foot.Width()-Width(counter)))}, Seg{Text: counter, Role: crole})
	drawLine(g, f.X+2, f.Y+f.H-2, iw, foot, false)
}

// footHit is a hint's cells on the compose window's last row: a click there
// does its key (the person: Ctrl+S send is a button).
type footHit struct {
	x, w int // from the row's first cell
	act  Action
}

// composeFoot is the window's hint row in w cells (or the flash), and where
// each hint that fits sits.
func (m Model) composeFoot(w int) (Line, []footHit) {
	c := m.ui.compose
	if m.ui.flash != "" {
		role := RoleOK
		if m.ui.flashErr {
			role = RoleError
		}
		return FitLine(Line{{Text: m.ui.flash, Role: role}}, w), nil
	}
	var foot Line
	var hits []footHit
	mode := ModeCompose
	if c.at != nil {
		mode = ModeComposeFile
	}
	for _, b := range bindings(mode) {
		if b.Hint == "" || (b.Act == ActComposeField && c.kind != composeMessage) {
			continue
		}
		if len(foot) > 0 {
			foot = append(foot, Seg{Text: "   "})
		}
		x := foot.Width()
		word := b.Hint
		if w := m.atHint(b.Act); w != "" {
			word = w
		}
		foot = append(foot, Seg{Text: b.Keys, Role: RoleAccent, Bold: true}, Seg{Text: " " + word, Role: RoleMuted})
		if foot.Width() <= w {
			hits = append(hits, footHit{x: x, w: foot.Width() - x, act: b.Act})
		}
	}
	return FitLine(foot, w), hits
}
