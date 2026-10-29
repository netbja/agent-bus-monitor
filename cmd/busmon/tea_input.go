package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The input line (busmon Charm spec, "Input"): it keeps its place and its
// behaviour. i, : or @ open it for a message (Esc keeps the draft), / for the
// filter; "@prefix" lists its completions above it (Tab or Enter takes the
// highlighted one, the arrows move it); Enter sends through the one send
// path (send.go), whose bus calls run in a command (sendCmd).

// lineKind is what the input line is editing.
type lineKind int

const (
	lineNone    lineKind = iota
	lineMessage          // a message: "@agent body", "@all body" or a broadcast
	lineFilter           // Activity's filter
)

// sendResultMsg is one send's outcome: the plan and its calls' errors.
type sendResultMsg struct {
	plan   sendPlan
	errs   []error
	window bool // sent from the compose window
}

// openLine gives the input line the keyboard on text.
func (m *Model) openLine(kind lineKind, text string) {
	m.ui.line, m.ui.edit, m.ui.tail, m.ui.pick = kind, text, 0, 0
	if kind == lineFilter {
		m.focusPanel(PanelActivity)
	}
}

// leaveLine returns the keyboard to the panels; keep saves a message draft.
func (m *Model) leaveLine(keep bool) {
	m.ui.broadcastDraft = "" // a warning holds for the next Enter only, never across an Esc
	if m.ui.line == lineMessage && keep {
		m.ui.msgDraft = m.ui.edit
	}
	m.ui.line, m.ui.edit, m.ui.tail = lineNone, "", 0
}

// lineCur is the line's cursor, a byte offset into its text.
func (m *Model) lineCur() int { return lineCursor(m.ui.edit, m.ui.tail) }

// setLine puts the line's text and its cursor.
func (m *Model) setLine(s string, cur int) { m.ui.edit, m.ui.tail = s, len(s)-cur }

// editLine is a key that edits the line at its cursor (both lines: a message
// and the filter); false when it is not one.
func (m *Model) editLine(k Key) bool {
	s, cur := m.ui.edit, m.lineCur()
	switch {
	case k.Code == keyLeft:
		cur = lineLeft(s, cur)
	case k.Code == keyRight:
		cur = lineRight(s, cur)
	case k.Code == keyHome, k.Code == keyCtrlA:
		cur = 0
	case k.Code == keyEnd, k.Code == keyCtrlE:
		cur = len(s)
	case k.Code == keyBackspace:
		s, cur = lineBackspace(s, cur)
	case k.Code == keyDelete:
		s, cur = lineDelete(s, cur)
	case k.Code == keyCtrlW:
		s, cur = lineDeleteWord(s, cur)
	case k.Code == keyCtrlU:
		s, cur = s[cur:], 0
	case k.Code == keyRune && !k.Alt:
		s, cur = lineInsert(s, cur, string(k.Rune))
	default:
		return false
	}
	m.setLine(s, cur)
	return true
}

// completions are the line's "@" completions (recipientCompletions: the @all
// targets, plus the dormant agents while a shows them).
func (m *Model) completions() []string {
	if m.ui.line != lineMessage {
		return nil
	}
	return recipientCompletions(m.ui.edit, m.names(m.ui.showAll))
}

// names are the registered agents the panel shows, the person excepted.
func (m *Model) names(showAll bool) []string {
	return agentNamesOf(m.snap.agents, m.snap.pilot, m.cfg.self, showAll, m.now)
}

// lineKey is a key while the line has the keyboard (ModeInput, ModeFilter).
func (m *Model) lineKey(act Action, k Key) tea.Cmd {
	if m.ui.line == lineFilter {
		m.editFilter(act, k)
		return nil
	}
	cands := m.completions()
	switch {
	case act == ActComposeOpen: // Ctrl+O: the line's text goes to the window
		text := strings.TrimSpace(m.ui.edit)
		m.leaveLine(false)
		m.ui.msgDraft = ""
		m.openMessageCompose(text, "")
	case len(cands) > 0 && (act == ActSend || k.Code == keyTab):
		m.ui.edit, m.ui.tail, m.ui.pick = cands[min(m.ui.pick, len(cands)-1)], 0, 0
	case len(cands) > 0 && (k.Code == keyUp || k.Code == keyDown):
		step := 1
		if k.Code == keyUp {
			step = len(cands) - 1
		}
		m.ui.pick = (m.ui.pick + step) % len(cands)
	case act == ActSend:
		return m.sendLine()
	case k.Code == keyTab: // after the open-question hint: answer in its panel
		tgt, body, directed := parseDirected(strings.TrimSpace(m.ui.edit))
		if m.ui.askDraft != "" && m.ui.askDraft == draftKey(tgt, body) {
			return m.answerInPanel(tgt, body, directed)
		}
	case act == ActEscape:
		m.ui.askDraft = ""
		m.leaveLine(true)
	case m.editLine(k):
		m.ui.pick = 0
	}
	return nil
}

// pasteLine is a paste into the line: one line, its newlines as spaces.
func (m *Model) pasteLine(s string) {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	m.setLine(lineInsert(m.ui.edit, m.lineCur(), Preview(s)))
}

// sendLine sends the line's text: "@agent body" is a directive, anything
// else a broadcast (which needs a second Enter).
func (m *Model) sendLine() tea.Cmd {
	text := strings.TrimSpace(m.ui.edit)
	if text == "" {
		m.ui.broadcastDraft, m.ui.edit, m.ui.tail, m.ui.msgDraft = "", "", 0, ""
		return nil
	}
	tgt, body, directed := parseDirected(text)
	if directed && m.pauseGate(draftKey(tgt, body), pauseRetry{kind: retryLine}) {
		return nil
	}
	return m.send(tgt, body, directed, false)
}

// send is the shell's side of the one send path: the plan is decided here,
// from the snapshot, and its calls run in a command. Nothing is sent twice:
// a send in flight takes no other.
func (m *Model) send(tgt, body string, directed, window bool) tea.Cmd {
	if m.ui.sending {
		m.ui.flash, m.ui.flashErr = "a send is in progress", true
		return nil
	}
	in := sendInput{tgt: tgt, body: body, directed: directed, window: window, known: m.snap.agents[tgt] != nil,
		holders: m.askHolders(tgt, directed), askDraft: m.ui.askDraft, broadcastDraft: m.ui.broadcastDraft}
	if directed && tgt == "all" {
		in.targets = m.names(false)
	}
	p := planSend(in)
	m.ui.askDraft, m.ui.broadcastDraft = p.askDraft, p.broadcastDraft
	if p.result != sendSent {
		m.ui.flash, m.ui.flashErr = p.flash, p.flashErr
		return nil
	}
	if m.cfg.w == nil {
		m.ui.flash, m.ui.flashErr = "send failed: no bus", true
		return nil
	}
	m.ui.sending = true
	w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		return sendResultMsg{plan: p, errs: p.exec(c, w, self), window: window}
	}
}

// gotSend reads a send's outcome: sent (or partly sent) clears the draft it
// came from, failed keeps it for a retry.
func (m *Model) gotSend(msg sendResultMsg) {
	m.ui.sending = false
	note := ""
	if msg.plan.directed && m.snap.pauseFailed {
		note = " " + GlyphNone + " pause ?"
	}
	res, flash, isErr := msg.plan.outcome(msg.errs, note)
	m.ui.flash, m.ui.flashErr = flash, isErr
	summary := m.ui.summarySent
	m.ui.summarySent = false
	if res == sendFailed {
		return
	}
	m.ui.pauseAck = "" // the acknowledgement was for this one send
	if summary {       // the summary request went: the recap it came from closes
		if p := m.rd.top(); p != nil && p.kind == ovRecap {
			m.pop()
		}
		return
	}
	if msg.window {
		m.sentCompose()
		return
	}
	m.ui.msgDraft = ""
	if m.ui.line == lineMessage {
		m.ui.edit, m.ui.tail = "", 0
	}
}

// askHolders are the recipients of a directed text that have an open ask:
// the named agent, or for @all the agents it reaches.
func (m *Model) askHolders(tgt string, directed bool) []string {
	if !directed {
		return nil
	}
	names := []string{tgt}
	if tgt == "all" {
		names = m.names(false)
	}
	var out []string
	for _, n := range names {
		if a := m.snap.agents[n]; a != nil && a.openAsk > 0 {
			out = append(out, n)
		}
	}
	return out
}

// answerInPanel opens the ask of the recipient holding an open question, with
// body as its free answer in the compose window; with several, the next digit
// picks one.
func (m *Model) answerInPanel(tgt, body string, directed bool) tea.Cmd {
	holders := m.askHolders(tgt, directed)
	if len(holders) == 0 {
		return nil
	}
	m.ui.askDraft = ""
	if len(holders) == 1 {
		return m.answerPrefilled(holders[0], body)
	}
	more := ""
	if len(holders) > 9 {
		more = fmt.Sprintf(" %s %d more: w on its row", GlyphNone, len(holders)-9)
		holders = holders[:9]
	}
	var opts []string
	for i, h := range holders {
		opts = append(opts, fmt.Sprintf("%d %s", i+1, h))
	}
	m.ui.askChoose = &askChoice{holders: holders, body: body}
	m.ui.flash, m.ui.flashErr = "answer whose question? "+strings.Join(opts, " "+GlyphNone+" ")+more+" (its number; Esc: back to editing)", false
	return nil
}

// chooseKey is the key after "answer whose question?": a digit picks, Esc
// goes back; every other key is swallowed until then.
func (m *Model) chooseKey(k Key) tea.Cmd {
	pick := m.ui.askChoose
	switch {
	case k.Code == keyRune && k.Rune >= '1' && int(k.Rune-'0') <= len(pick.holders):
		m.ui.askChoose = nil
		return m.answerPrefilled(pick.holders[k.Rune-'1'], pick.body)
	case k.Code == keyEscape:
		m.ui.askChoose = nil
		m.ui.flash, m.ui.flashErr = "", false
	}
	return nil
}

// answerPrefilled leaves the line (its draft kept), opens agent's ask on its
// first open question and the compose window on it with text as the answer.
func (m *Model) answerPrefilled(agent, text string) tea.Cmd {
	if m.ui.line != lineNone {
		m.leaveLine(true)
	}
	cmd := m.pushAskOf(agent)
	if p := m.rd.top(); p != nil && p.kind == ovAsk {
		if a, ok := m.pageAsk(p); ok && p.ask.q < len(a.Questions) && a.Questions[p.ask.q].Answer == nil {
			m.openAnswerCompose(a, a.Questions[p.ask.q], text)
		}
	}
	return cmd
}

// inputRow is the line as drawn: its label, then the text around the cursor
// cell (the cluster it stands on, reversed; a space past the end) while it
// has the keyboard. A text wider than the line keeps the cursor in view with
// a quarter of the room after it when there is more: cut at clusters, in
// cells.
func (m *Model) inputRow(width int) Line {
	label := GlyphPrompt + " "
	if m.ui.line == lineFilter {
		label = "filter" + GlyphPrompt + " "
	}
	l := Line{{Text: label, Role: RoleAccent}}
	if m.ui.line == lineNone {
		return l
	}
	room := max(1, width-Width(label))
	cur := m.lineCur()
	before, rest := Preview(m.ui.edit[:cur]), Preview(m.ui.edit[cur:])
	at, size := " ", 0
	if rest != "" {
		at, size = firstCluster(rest)
	}
	rest = rest[size:]
	after := min(Width(rest), room/4)
	for before != "" && Width(before)+Width(at)+after > room {
		_, size := firstCluster(before)
		before = before[size:]
	}
	shown, left := "", room-Width(before)-Width(at)
	for rest != "" {
		c, size := firstCluster(rest)
		if Width(c) > left {
			break
		}
		shown, left, rest = shown+c, left-Width(c), rest[size:]
	}
	return append(l, Seg{Text: before, Role: RoleText}, Seg{Text: at, Role: RoleText, Reverse: true}, Seg{Text: shown, Role: RoleText})
}

// completionRow lists the line's completions, the highlighted one as a pill.
func (m *Model) completionRow(width int) Line {
	cands := m.completions()
	if len(cands) == 0 {
		return nil
	}
	l := Line{{Text: " "}}
	for i, c := range cands {
		l = append(l, Seg{Text: " " + strings.TrimSpace(c) + " ", Role: RoleAccent, Pill: i == m.ui.pick}, Seg{Text: " "})
	}
	return FitLine(l, width)
}
