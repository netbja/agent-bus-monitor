package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The reader (busmon Charm spec, "Reading"): every overlay that shows text is
// a page on a stack; the top page replaces the lower zone (the tabbed panel
// and the input), the header, For you and Agents stay. Esc returns to the
// page that opened it, then to the panels with their selection as it was.
// Bodies are built on the update path (refresh), at the zone's width, and
// rebuilt only when their data, width, palette or state changed.

// page is one reader page.
type page struct {
	kind        overlayKind
	serial      int    // its identity on the stack: a thread read finds its page by it
	key         string // its subject: an event id, an agent, a task, a request key, an ask id
	title       string
	body        []Line
	plain       string // y
	threadPlain string // Y
	top         int    // the first body line shown
	built       pageStamp
	dirty       bool // its own state changed since the build (cursor, thread)
	fixed       bool // its body was given at open (services, a verdict): never rebuilt

	ev     bus.Event // ovMessage
	at     time.Time
	thread threadState

	ask   askPanel   // ovAsk: the question and option under the cursor
	spans []lineSpan // ovAsk: each option's lines, "Other" last; nil once answered

	sel      int        // ovPlan: the selected item
	expanded bool       // ovPlan: the done group is open
	items    []planItem // ovPlan
}

// pageStamp is what a body was built for.
type pageStamp struct {
	gen, width int
	pal        Palette
}

// reader is the page stack, behind a pointer like the lists.
type reader struct {
	pages    []page
	serial   int                      // the last page's serial
	answered map[string]bus.AskAnswer // answers sent from here the poll has not brought yet (ask|question)
	sending  map[string]bool          // ask|question being sent: a second pick sends nothing
}

func newReader() *reader {
	return &reader{answered: map[string]bus.AskAnswer{}, sending: map[string]bool{}}
}

func (r *reader) open() bool { return len(r.pages) > 0 }

func (r *reader) top() *page {
	if len(r.pages) == 0 {
		return nil
	}
	return &r.pages[len(r.pages)-1]
}

// threadMsg is a message page's thread, read by threadCmd for the page of
// that serial.
type threadMsg struct {
	serial  int
	id      string
	entries []bus.Event
	err     error
}

// servicesMsg is the Services page's body, read by servicesCmd (it reads the
// local logs from disk).
type servicesMsg struct{ body []Line }

// answerMsg is the result of one answer sent by answerCmd.
type answerMsg struct {
	ask          bus.Ask
	qid, header  string
	choice, text string
	id           string // the ask the answer landed on (a replacement's id)
	left         int
	err          error
	key          string // ask|question as sent
}

// readerRect is the reader's zone: the tabbed panel and the input, or at the
// narrow tier the view, down to the footer.
func (m *Model) readerRect() Rect {
	l := m.layout
	switch l.Tier {
	case TierWide, TierMedium:
		return Rect{0, l.Lower.Y, m.width, l.Footer.Y - l.Lower.Y}
	case TierNarrow:
		r := l.rect(l.Active)
		return Rect{0, r.Y, m.width, l.Footer.Y - r.Y}
	}
	return Rect{}
}

func (r Rect) contains(x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }

// readerWidth is the body's width: inside the frame, one space each side.
func (m *Model) readerWidth() int { return max(20, m.readerRect().W-4) }

func (m *Model) readerRows() int { return max(0, m.readerRect().H-2) }

// push opens a page over the current one; a message page also reads its thread.
func (m *Model) push(p page) tea.Cmd {
	if p.kind == ovMessage {
		m.markRead(p.ev.ID) // opening a line reads it
	}
	m.rd.serial++
	p.serial = m.rd.serial
	m.rd.pages = append(m.rd.pages, p)
	m.refresh()
	if p.kind == ovMessage && p.thread.ID != "" && !p.thread.Loaded {
		return m.threadCmd(p.serial, p.thread.ID)
	}
	return nil
}

// pop returns to the page below, or to the panels.
func (m *Model) pop() {
	if n := len(m.rd.pages); n > 0 {
		m.rd.pages = m.rd.pages[:n-1]
		if p := m.rd.top(); p != nil {
			p.dirty = true // built for another width or generation maybe: rebuild
		}
	}
	m.refresh()
}

// closeReader leaves every page.
func (m *Model) closeReader() {
	m.rd.pages = nil
	m.refresh()
}

func (m *Model) threadCmd(serial int, id string) tea.Cmd {
	w, ctx := m.cfg.w, m.cfg.ctx
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		entries, err := w.Thread(c, id)
		return threadMsg{serial: serial, id: id, entries: entries, err: err}
	}
}

// gotThread puts a thread read in its page, wherever it is on the stack
// (another page may have been pushed over it), unless the page has gone.
func (m *Model) gotThread(msg threadMsg) {
	i := slices.IndexFunc(m.rd.pages, func(p page) bool { return p.serial == msg.serial })
	if i < 0 || m.rd.pages[i].kind != ovMessage || m.rd.pages[i].thread.ID != msg.id {
		return
	}
	p := &m.rd.pages[i]
	p.thread = threadState{ID: msg.id, Entries: msg.entries, Loaded: true, Err: msg.err}
	p.dirty = true
	m.refresh()
}

func (m *Model) servicesCmd() tea.Cmd {
	inst, recs, now, width := m.snap.svcInst, m.snap.svcRecs, m.now, m.readerWidth()
	return func() tea.Msg {
		host, _ := os.Hostname()
		return servicesMsg{body: servicesPanel(inst, recs, host, now, width)}
	}
}

// The pages a key or a row opens.

func messagePage(e bus.Event, at time.Time) page {
	p := page{kind: ovMessage, key: e.ID, ev: e, at: at, thread: threadState{ID: threadID(e)}}
	if p.thread.ID == "" {
		p.thread.Loaded = true
	}
	return p
}

func (m *Model) pushMessage(e bus.Event, at time.Time) tea.Cmd { return m.push(messagePage(e, at)) }

func (m *Model) pushAgent(name string) tea.Cmd {
	if m.snap.agents[name] == nil {
		return nil
	}
	return m.push(page{kind: ovAgent, key: name})
}

// pushAsk opens the ask in the reader's ask mode, on its first open question.
func (m *Model) pushAsk(id string) tea.Cmd {
	a, ok := findAsk(m.snap.asks, id)
	if !ok || len(a.Questions) == 0 {
		return nil
	}
	a = m.withLocalAnswers(a)
	q := max(firstOpenQuestion(a, 0), 0)
	return m.push(page{kind: ovAsk, key: id, ask: askPanel{id: id, q: q, opt: recommendedIndex(a.Questions[q].AskQuestion)}})
}

// pushAskOf opens the agent's open ask, or with "" the oldest one.
func (m *Model) pushAskOf(agent string) tea.Cmd {
	for _, a := range openAsks(m.snap.asks) {
		if agent == "" || a.From == agent {
			return m.pushAsk(a.ID)
		}
	}
	m.ui.flash, m.ui.flashErr = "no open question", false
	return nil
}

func (m *Model) pushPlan() tea.Cmd {
	if !m.snap.planState.loaded {
		m.ui.flash, m.ui.flashErr = "no plan loaded (master: agentbus plan load <path>@<commit>)", false
		return nil
	}
	return m.push(page{kind: ovPlan})
}

// buildPage (re)builds the top page's body when it is stale; it reports
// false when the page has nothing left to show (its ask closed).
func (m *Model) buildPage() bool {
	p := m.rd.top()
	if p == nil || m.width == 0 {
		return true
	}
	width := m.readerWidth()
	stamp := pageStamp{gen: m.gen, width: width, pal: th.Palette()}
	if p.fixed || (!p.dirty && p.built == stamp && p.body != nil) {
		m.clampPage(p)
		return true
	}
	m.cache.builds++
	// The bodies read the overlay width global (detail.go); the shell sets it
	// for the build, on the update path. Step c removes the global.
	old := mdWidth
	mdWidth = width
	defer func() { mdWidth = old }()
	s, now := m.snap, m.now
	p.plain, p.threadPlain = "", ""
	switch p.kind {
	case ovMessage:
		p.title = detailTitle(p.ev)
		p.body = detailText(p.ev, p.at, now, p.thread)
		p.plain, p.threadPlain = detailPlain(p.ev), threadPlain(p.ev, p.thread)
	case ovAgent:
		a := s.agents[p.key]
		if a == nil {
			break // gone from the snapshot: the last body stays
		}
		title, detail := agentDetail(p.key, a, p.key == s.pilot, s.board, m.feed.feed, now)
		var b body
		var asks [][]Line // each open ask's summary leads, the newest first
		for _, ask := range openAsks(s.asks) {
			if ask.From == p.key {
				asks = append([][]Line{askSummary(ask)}, asks...)
			}
		}
		for _, l := range asks {
			b.join(l)
		}
		snap, has := s.budgets[a.provider]
		until, isHeld := held(s.holds, a.provider, now)
		b.join(detail).join(quotaDetail(a.provider, snap, has, until, isHeld, now))
		p.title, p.body, p.plain = title, b.Lines(), agentDetailPlain(a)
	case ovTask:
		if e, ok := s.board[p.key]; ok {
			p.title, p.body, p.plain = taskDetail(e, p.key, now)
		}
	case ovRequest:
		task := strings.TrimPrefix(p.key, "req:")
		for _, r := range s.requests {
			if r.Task == task {
				p.title, p.body, p.plain = requestDetail(r, now)
			}
		}
	case ovRequests:
		label := requestsLabel(s.requests, s.threads, m.cfg.self, openQuestionCount(s.asks)).text()
		p.title, p.body = fmt.Sprintf("Requests %s %s", GlyphNone, label), requestsPanel(s.requests, s.threads, now, width)
	case ovHealth:
		gnow := s.guardianNow
		if gnow.IsZero() {
			gnow = now
		}
		p.title, p.body = "Project health", healthOverlayText(s.guardianHealth, s.guardianNotes, gnow)
	case ovHelp:
		p.title, p.body = helpTitle(), helpText()
	case ovPlan:
		body, plain, items := planOverlay(s.planState, p.sel, p.expanded, now, width)
		if p.sel >= len(items) {
			p.sel = max(0, len(items)-1)
			body, plain, items = planOverlay(s.planState, p.sel, p.expanded, now, width)
		}
		p.body, p.plain, p.items = body, plain, items
		p.title = "Plan"
		if s.plan != nil {
			p.title += " " + GlyphNone + " " + strings.TrimPrefix(planSeg(*s.plan, now).text(), "plan ")
		}
	case ovPlanTask:
		p.title, p.body, p.plain = planTaskDetail(s.planState, p.key, now)
	case ovAsk:
		a, ok := m.pageAsk(p)
		if !ok || (a.Open() == 0 && a.State != "open") {
			return false
		}
		if p.ask.q >= len(a.Questions) {
			p.ask.q = 0
		}
		p.title = askTitle(a)
		p.body, p.spans = askPanelLayout(a, p.ask, width)
		p.plain = askPlain(a.Questions[p.ask.q])
	}
	p.built, p.dirty = stamp, false
	m.clampPage(p)
	return true
}

// clampPage keeps the page's scroll inside its body.
func (m *Model) clampPage(p *page) {
	p.top = clamp(p.top, 0, max(0, len(p.body)-m.readerRows()))
}

// askPlain is the question as y copies it.
func askPlain(q bus.AskQuestionState) string {
	plain := q.Question
	if q.Context != "" {
		plain += "\n\n" + q.Context
	}
	for i, o := range q.Options {
		plain += fmt.Sprintf("\n%d. %s", i+1, o.Label)
		if o.Description != "" {
			plain += " - " + o.Description
		}
	}
	return CopyText(plain)
}

// pageAsk is the page's ask, following replaced_by to the open version, with
// the answers sent from here the poll has not brought yet.
func (m *Model) pageAsk(p *page) (bus.Ask, bool) {
	id := p.ask.id
	for hops := 0; hops < 8; hops++ {
		a, ok := findAsk(m.snap.asks, id)
		if !ok {
			return bus.Ask{}, false
		}
		if a.State == "replaced" && a.ReplacedBy != "" {
			id = a.ReplacedBy
			continue
		}
		a = m.withLocalAnswers(a)
		if id != p.ask.id {
			p.ask.id, p.key = id, id
			p.ask.q = max(firstOpenQuestion(a, 0), 0)
			p.ask.opt = recommendedIndex(a.Questions[p.ask.q].AskQuestion)
		}
		return a, true
	}
	return bus.Ask{}, false
}

// withLocalAnswers is a with the answers sent from here, on a copy of its
// questions: the snapshot's asks are shared with the poller and never written.
func (m *Model) withLocalAnswers(a bus.Ask) bus.Ask {
	qs := make([]bus.AskQuestionState, len(a.Questions))
	copy(qs, a.Questions)
	for i, q := range qs {
		if ans, ok := m.rd.answered[a.ID+"|"+q.ID]; ok && q.Answer == nil {
			qs[i].Answer = &ans
		}
	}
	a.Questions = qs
	return a
}

// forgetAnswers drops the local answers the snapshot now holds, or whose ask
// is no longer open.
func (m *Model) forgetAnswers() {
	for k := range m.rd.answered {
		id, qid, _ := strings.Cut(k, "|")
		a, ok := findAsk(m.snap.asks, id)
		if !ok || a.State != "open" {
			delete(m.rd.answered, k)
			continue
		}
		for _, q := range a.Questions {
			if q.ID == qid && q.Answer != nil {
				delete(m.rd.answered, k)
			}
		}
	}
}

// answer answers the ask page's current question with option n; n equal to
// the number of options is "Other", a free answer in the compose window. An
// answered question, or one being sent, takes nothing.
func (m *Model) answer(n int) tea.Cmd {
	p := m.rd.top()
	if p == nil || p.kind != ovAsk {
		return nil
	}
	a, ok := m.pageAsk(p)
	if !ok || p.ask.q >= len(a.Questions) {
		return nil
	}
	q := a.Questions[p.ask.q]
	key := a.ID + "|" + q.ID
	if q.Answer != nil || m.rd.sending[key] || n < 0 || n > len(q.Options) {
		return nil
	}
	p.ask.opt, p.dirty = n, true
	if n == len(q.Options) {
		m.openAnswerCompose(a, q)
		m.refresh()
		return nil
	}
	if m.paused() {
		// tview asks through the pause popup; the shell's dialogs come with
		// step b4, so until then a paused project's answer is not sent.
		m.ui.flash, m.ui.flashErr = "project paused: answer not sent", true
		m.refresh()
		return nil
	}
	if m.cfg.w == nil {
		return nil
	}
	m.rd.sending[key] = true
	m.refresh()
	return m.answerCmd(a, q, q.Options[n].Label, "")
}

// paused: the project's pause is running (a failed read is not a pause).
func (m *Model) paused() bool {
	p := m.snap.pause
	return p != nil && m.now.Before(p.UntilTime())
}

func (m *Model) answerCmd(a bus.Ask, q bus.AskQuestionState, choice, text string) tea.Cmd {
	w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		id, left, err := w.Answer(c, a.ID, q.ID, self, choice, text)
		return answerMsg{ask: a, qid: q.ID, header: q.Header, choice: choice, text: text, id: id, left: left, err: err,
			key: a.ID + "|" + q.ID}
	}
}

// gotAnswer records a sent answer and moves the page to the next open
// question; the last one closes it. A refusal says why and keeps the page.
func (m *Model) gotAnswer(msg answerMsg) {
	delete(m.rd.sending, msg.key)
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = "answer not sent: "+Preview(msg.err.Error()), true
		m.refresh()
		return
	}
	ans := bus.AskAnswer{Choice: msg.choice, Text: msg.text, By: m.cfg.self, At: m.now.UnixMilli()}
	m.rd.answered[msg.key] = ans
	if msg.id != "" && msg.id != msg.ask.ID {
		m.rd.answered[msg.id+"|"+msg.qid] = ans
	}
	value := msg.choice
	if value == "" {
		value = msg.text
	}
	m.ui.flash, m.ui.flashErr = fmt.Sprintf("%s %s: %s sent to %s", GlyphDone, Preview(msg.header), Preview(value), Preview(msg.ask.From)), false
	p := m.rd.top()
	if p == nil || p.kind != ovAsk || (p.ask.id != msg.ask.ID && p.ask.id != msg.id) {
		m.refresh()
		return
	}
	if msg.left == 0 {
		m.pop()
		return
	}
	if msg.id != "" {
		p.ask.id, p.key = msg.id, msg.id
	}
	if a, ok := m.pageAsk(p); ok {
		if n := firstOpenQuestion(a, p.ask.q+1); n >= 0 {
			p.ask.q, p.ask.opt = n, recommendedIndex(a.Questions[n].AskQuestion)
		}
	}
	p.dirty = true
	m.refresh()
}

// composeIntent is the compose window the shell would open: a free answer
// to an ask's question, prefilled with its draft. The window itself (its
// text area, its send) is step b4's; b3 records what opens it.
type composeIntent struct {
	kind         composeKind
	to           string // the agent answered
	askID, qid   string
	header, text string
}

func (m *Model) openAnswerCompose(a bus.Ask, q bus.AskQuestionState) {
	m.ui.compose = &composeIntent{kind: composeAnswer, to: a.From, askID: a.ID, qid: q.ID, header: q.Header,
		text: m.ui.askDrafts[a.ID+"|"+q.ID]}
}

// readerKey is a key while the reader is open.
func (m *Model) readerKey(act Action, k Key) tea.Cmd {
	p := m.rd.top()
	if p.kind == ovAsk {
		return m.askKey(act, k)
	}
	rows := m.readerRows()
	switch act {
	case ActClose:
		m.pop()
		return nil
	case ActCopy:
		return m.copy(p.plain, "text")
	case ActCopyThread:
		return m.copy(p.threadPlain, "thread")
	case ActHealth:
		return m.push(page{kind: ovHealth})
	case ActAsk:
		switch {
		case p.kind == ovPlan && k.Code == keyEnter:
			return m.openPlanItem(p)
		case p.kind == ovAgent && m.snap.agents[p.key] != nil && m.snap.agents[p.key].openAsk > 0:
			return m.pushAskOf(p.key)
		case k.Code != keyEnter:
			return m.pushAskOf("")
		}
		return nil
	case ActUp, ActDown, ActPageUp, ActPageDown, ActFirst, ActLast:
		if p.kind == ovPlan {
			m.movePlan(p, act, rows)
			return nil
		}
		switch act {
		case ActUp:
			p.top--
		case ActDown:
			p.top++
		case ActPageUp:
			p.top -= max(1, rows)
		case ActPageDown:
			p.top += max(1, rows)
		case ActFirst:
			p.top = 0
		case ActLast:
			p.top = len(p.body)
		}
		m.clampPage(p)
	}
	return nil
}

// askKey is a key on an ask page (ModeShellAsk).
func (m *Model) askKey(act Action, k Key) tea.Cmd {
	p := m.rd.top()
	a, ok := m.pageAsk(p)
	if !ok {
		m.pop()
		return nil
	}
	q := a.Questions[min(p.ask.q, len(a.Questions)-1)]
	switch act {
	case ActClose:
		m.pop()
		return nil
	case ActHelp:
		return m.push(page{kind: ovHelp})
	case ActCopy:
		return m.copy(p.plain, "question")
	case ActUp:
		p.ask.opt = max(p.ask.opt-1, 0)
	case ActDown:
		p.ask.opt = min(p.ask.opt+1, len(q.Options))
	case ActAskNext, ActAskPrev:
		step := 1
		if act == ActAskPrev {
			step = len(a.Questions) - 1
		}
		p.ask.q = (p.ask.q + step) % len(a.Questions)
		p.ask.opt = recommendedIndex(a.Questions[p.ask.q].AskQuestion)
	case ActAskPick:
		return m.answer(int(k.Rune - '1'))
	case ActAskOther:
		return m.answer(len(q.Options))
	case ActSend:
		return m.answer(p.ask.opt)
	}
	p.dirty = true
	m.refresh()
	return nil
}

// movePlan moves the plan's selection and keeps it in view.
func (m *Model) movePlan(p *page, act Action, rows int) {
	n := len(p.items)
	if n == 0 {
		return
	}
	switch act {
	case ActUp:
		p.sel--
	case ActDown:
		p.sel++
	case ActPageUp:
		p.sel -= 10
	case ActPageDown:
		p.sel += 10
	case ActFirst:
		p.sel = 0
	case ActLast:
		p.sel = n - 1
	}
	p.sel = clamp(p.sel, 0, n-1)
	p.dirty = true
	m.refresh()
	if p.sel < len(p.items) {
		if line := p.items[p.sel].line; line < p.top || line >= p.top+rows {
			p.top = max(0, line-3)
			m.clampPage(p)
		}
	}
}

// openPlanItem expands the done group or opens a task of the plan.
func (m *Model) openPlanItem(p *page) tea.Cmd {
	if p.sel >= len(p.items) {
		return nil
	}
	it := p.items[p.sel]
	if it.toggle {
		p.expanded, p.dirty = true, true
		m.refresh()
		return nil
	}
	return m.push(page{kind: ovPlanTask, key: it.task})
}

// copy puts s on the clipboard (OSC52: it reaches the local clipboard even
// over SSH) and says so.
func (m *Model) copy(s, what string) tea.Cmd {
	if s == "" {
		return nil
	}
	m.ui.flash, m.ui.flashErr = GlyphDone+" "+what+" copied", false
	m.refresh()
	return tea.SetClipboard(s)
}

// readerWheel scrolls the page three rows.
func (m *Model) readerWheel(up bool) {
	p := m.rd.top()
	if up {
		p.top -= 3
	} else {
		p.top += 3
	}
	m.clampPage(p)
}

// readerClick: on an ask page a click on an option answers with it, as its
// key does ("Other" opens the compose window); on the plan a click selects an
// item, and a click on the selected one opens it.
func (m *Model) readerClick(x, y int) tea.Cmd {
	p, r := m.rd.top(), m.readerRect()
	line := p.top + y - (r.Y + 1)
	if y <= r.Y || y >= r.Y+r.H-1 || x <= r.X || x >= r.X+r.W-1 || line >= len(p.body) {
		return nil
	}
	switch p.kind {
	case ovAsk:
		for i, s := range p.spans {
			if line >= s.from && line < s.to {
				return m.answer(i)
			}
		}
	case ovPlan:
		for i, it := range p.items {
			if it.line == line {
				if i == p.sel {
					return m.openPlanItem(p)
				}
				p.sel, p.dirty = i, true
				m.refresh()
			}
		}
	}
	return nil
}

// drawReader draws the top page in its zone: a frame titled with the page's
// name, its hints as the label, the body one space in from each side.
func (m Model) drawReader(g *grid) {
	p, r := m.rd.top(), m.readerRect()
	if p == nil || r.W < 6 || r.H < 3 {
		return
	}
	rows := r.H - 2
	hints := "esc back"
	if p.plain != "" {
		hints = "y copy " + GlyphNone + " " + hints
	}
	if p.threadPlain != "" {
		hints = "Y copy thread " + GlyphNone + " " + hints
	}
	drawFrame(g, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: Preview(p.title), Role: RoleText}},
		Label: Line{{Text: hints, Role: RoleMuted}}, Focused: true,
		Scroll: ScrollState{Total: len(p.body), Top: p.top, Visible: rows}, Hue: RoleAccent})
	for i := 0; i < rows; i++ {
		var l Line
		if j := p.top + i; j < len(p.body) {
			l = p.body[j]
		}
		drawLine(g, r.X+2, r.Y+1+i, r.W-4, l, false)
	}
}
