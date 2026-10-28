package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The shell's small dialogs (busmon Charm spec, "Reading": they stay dialogs
// over the screen): the pause popup before a waking send, the reminder, the
// recap summary's confirmation. The removal (d then y) keeps its form: a
// question in the flash, the next key answers it.

// retryKind is the send a pause dialog replays once the person chooses.
type retryKind int

const (
	retryLine    retryKind = iota // the input line's Enter
	retryCompose                  // the compose window's Ctrl+S (a message or a free answer)
	retryAnswer                   // an ask's option, by key or click
)

type pauseRetry struct {
	kind retryKind
	opt  int // retryAnswer: the option
}

// pauseDialog is the open pause popup: Resume now, Send anyway, Cancel.
type pauseDialog struct {
	rec   bus.PauseRecord
	key   string
	retry pauseRetry
	sel   int // 0 Resume now, 1 Send anyway, 2 Cancel (the default)
}

// resumeMsg is the result of "Resume now".
type resumeMsg struct {
	err   error
	key   string
	retry pauseRetry
}

// pauseGate opens the popup when a waking send meets a running pause, and
// reports whether it did (the caller stops; the popup replays the send). A
// failed pause read, a pause already over, or a send the person already
// chose to make (key) passes.
func (m *Model) pauseGate(key string, retry pauseRetry) bool {
	if m.ui.pauseAck == key {
		return false
	}
	p := m.snap.pause
	if m.snap.pauseFailed || p == nil || !m.now.Before(p.UntilTime()) {
		return false
	}
	m.ui.pausePopup = &pauseDialog{rec: *p, key: key, retry: retry, sel: 2}
	return true
}

// pauseRect is the popup's place on this screen.
func (m *Model) pauseDialogRect() Rect { return pauseRect(m.width, m.height, m.layout.Header.H) }

// pressPause performs a button.
func (m *Model) pressPause(i int) tea.Cmd {
	d := m.ui.pausePopup
	m.ui.pausePopup = nil
	switch i {
	case 0:
		w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
		if w == nil {
			return nil
		}
		if ctx == nil {
			ctx = context.Background()
		}
		return func() tea.Msg {
			c, cancel := context.WithTimeout(ctx, pollBudget)
			defer cancel()
			_, err := w.Resume(c, self, "Resumed from busmon by "+self+".")
			return resumeMsg{err: err, key: d.key, retry: d.retry}
		}
	case 1:
		m.ui.pauseAck = d.key
		return m.replay(d.retry)
	}
	m.ui.flash, m.ui.flashErr = "not sent: the project is paused", false
	return nil
}

// gotResume: the pause is over, the send goes; a failed resume sends nothing.
func (m *Model) gotResume(msg resumeMsg) tea.Cmd {
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = "resume failed, nothing sent: "+Preview(msg.err.Error()), true
		return nil
	}
	m.snap.pause = nil
	m.ui.pauseAck = msg.key
	return m.replay(msg.retry)
}

// replay sends again what the pause stopped, through its own path.
func (m *Model) replay(r pauseRetry) tea.Cmd {
	switch r.kind {
	case retryLine:
		if m.ui.line == lineMessage {
			return m.sendLine()
		}
	case retryCompose:
		if m.ui.compose != nil {
			return m.composeSend()
		}
	case retryAnswer:
		return m.answer(r.opt)
	}
	return nil
}

// pauseKey is a key while the popup is open; Ctrl-C still quits (above).
func (m *Model) pauseKey(k Key) tea.Cmd {
	d := m.ui.pausePopup
	switch {
	case k.Code == keyEscape:
		return m.pressPause(2)
	case k.Code == keyEnter:
		return m.pressPause(d.sel)
	case k.Code == keyLeft || k.Code == keyBacktab:
		d.sel = (d.sel + 2) % 3
	case k.Code == keyRight || k.Code == keyTab:
		d.sel = (d.sel + 1) % 3
	case k.Code == keyRune:
		switch k.Rune {
		case 'r', 'R':
			return m.pressPause(0)
		case 's', 'S':
			return m.pressPause(1)
		case 'c', 'C':
			return m.pressPause(2)
		}
	}
	return nil
}

// pauseClick presses a clicked button; every click is the popup's while it
// is open (it is modal).
func (m *Model) pauseClick(x, y int) tea.Cmd {
	btns := drawPausePopup(newGrid(m.width, m.height), m.pauseDialogRect(), m.ui.pausePopup.rec, m.ui.pausePopup.sel, m.now)
	for i, b := range btns {
		if b.W > 0 && y == b.Y && x >= b.X && x < b.X+b.W {
			return m.pressPause(i)
		}
	}
	return nil
}

// askRemove is d on the selected row: it names what would go and waits for
// y. Agents: forgotten (agentbus agents forget); untracked threads: closed
// by a reply from the person; a tracked request is only explained.
func (m *Model) askRemove() {
	key := m.lists.of(m.ui.focus).Selected()
	switch {
	case m.ui.focus == PanelAgents && key != "":
		a := m.snap.agents[key]
		if a == nil {
			return
		}
		if why := liveReason(a, m.now); why != "" {
			m.ui.flash, m.ui.flashErr = key+" "+why+": not removed", true
			return
		}
		m.ui.confirm = &pendingRemoval{key: "agent:" + key, label: key}
		m.ui.flash, m.ui.flashErr = "forget agent "+key+" (state, usage, queue)? y confirms, any other key cancels", true
	case m.ui.focus == PanelRequests && reqRowKey(key):
		ths := m.reqThreadsOf(key)
		var closable []reqThread
		for _, t := range ths {
			if t.Kind != kindChallenge && t.Request == nil {
				closable = append(closable, t)
			}
		}
		if len(closable) == 0 {
			if len(ths) > 0 {
				m.ui.flash, m.ui.flashErr = "a review ends with a verdict or agentbus gate close", true
			}
			return
		}
		t := closable[0]
		m.ui.confirm = &pendingRemoval{key: key, label: t.Target, threads: closable}
		if len(closable) == 1 {
			m.ui.flash = fmt.Sprintf("close the %s to %s (history kept)? y confirms, any other key cancels", t.Kind, t.Target)
		} else {
			m.ui.flash = fmt.Sprintf("close %d threads %s%s%s (history kept)? y closes %d, any other key cancels", len(closable), t.From, GlyphArrowR, t.Target, len(closable))
		}
		m.ui.flashErr = true
	case strings.HasPrefix(key, "req:"):
		m.ui.flash, m.ui.flashErr = "a tracked request is closed by its owner (agentbus request done or block)", true
	}
}

// removeMsg is the result of a confirmed removal: the threads closed (ids),
// the first error.
type removeMsg struct {
	key string
	ids []string
	err error
}

// confirmKey answers the removal's question: y removes, any other key keeps.
func (m *Model) confirmKey(k Key) tea.Cmd {
	c := m.ui.confirm
	m.ui.confirm = nil
	if k.Code != keyRune || k.Rune != 'y' {
		m.ui.flash, m.ui.flashErr = "not removed", false
		return nil
	}
	w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	threads := c.threads
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		if name, ok := strings.CutPrefix(c.key, "agent:"); ok {
			_, err := w.ForgetAgent(cc, name)
			return removeMsg{key: c.key, err: err}
		}
		out := removeMsg{key: c.key}
		for _, t := range threads { // each thread once
			to := t.From
			if !bus.ValidName(to) {
				to = t.Target
			}
			if _, err := w.HumanClose(cc, self, to, t.ID, "closed from busmon by "+self); err != nil {
				out.err = err
				break
			}
			out.ids = append(out.ids, t.ID)
		}
		return out
	}
}

// gotRemove: the agent or the thread leaves the shell's copy at once (the
// next poll confirms it); a failure says why.
func (m *Model) gotRemove(msg removeMsg) {
	name, isAgent := strings.CutPrefix(msg.key, "agent:")
	switch {
	case msg.err != nil && isAgent:
		m.ui.flash, m.ui.flashErr = "forget failed: "+Preview(msg.err.Error()), true
	case msg.err != nil:
		m.ui.flash, m.ui.flashErr = "close failed: "+Preview(msg.err.Error()), true
	case isAgent:
		agents := make(map[string]*agentState, len(m.snap.agents))
		for n, a := range m.snap.agents {
			if n != name {
				agents[n] = a
			}
		}
		m.snap.agents = agents // a copy: the snapshot's maps are never written
		m.ui.flash, m.ui.flashErr = GlyphDone+" "+name+" forgotten", false
	default:
		closed := map[string]bool{}
		for _, id := range msg.ids {
			closed[id] = true
		}
		var kept []openThread
		for _, t := range m.snap.threads {
			if !closed[t.ID] {
				kept = append(kept, t)
			}
		}
		m.snap.threads = kept
		// The replies the next poll will read, so the view folds them now
		// (a copy: the snapshot's slices are never written).
		hist := append([]bus.Event(nil), m.snap.cmdHistory...)
		for _, id := range msg.ids {
			hist = append(hist, bus.Event{Kind: "cmd", Type: bus.CmdReply, From: m.cfg.self, Ref: id, NoWake: true, Human: true})
		}
		m.snap.cmdHistory = hist
		m.ui.flash, m.ui.flashErr = GlyphDone+" thread closed", false
		if len(msg.ids) > 1 {
			m.ui.flash = fmt.Sprintf("%s %d threads closed", GlyphDone, len(msg.ids))
		}
	}
}

// reminderDialog is the open reminder: the decisions due, oldest first.
type reminderDialog struct {
	items []attentionItem
	sel   int
}

// busyForModal: something the person is in the middle of; a reminder or an
// automatic recap waits for it to end.
func (m *Model) busyForModal() bool {
	return m.ui.compose != nil || m.rd.open() || m.ui.line != lineNone || strings.TrimSpace(m.ui.msgDraft) != "" ||
		m.ui.askChoose != nil || m.ui.pausePopup != nil || m.ui.reminder != nil || m.ui.summary || m.ui.tour != nil
}

// maybeReminder opens the reminder when decisions are due (PopupAfter) and
// the person is not busy; it snoozes what it shows.
func (m *Model) maybeReminder() tea.Cmd {
	s := m.cfg.settings
	if s.PopupAfter <= 0 || m.sig.recapLoading || m.sig.recapPending || m.busyForModal() {
		return nil
	}
	items := dueAttention(decisions(m.forYou), m.cache_(), m.now, s.PopupAfter)
	if len(items) == 0 {
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		if items[i].Agent != items[j].Agent {
			return items[i].Agent < items[j].Agent
		}
		return items[i].Key < items[j].Key
	})
	m.ui.reminder = &reminderDialog{items: items}
	return m.snooze(items)
}

// cache_ is the local cache the shell keeps (snoozes, last visit).
func (m *Model) cache_() *busmonCache {
	if m.sig.cache == nil {
		m.sig.cache = &busmonCache{Project: m.cfg.project, Snoozes: map[string]int64{}}
	}
	return m.sig.cache
}

// snooze snoozes items for Snooze and saves the cache in a command.
func (m *Model) snooze(items []attentionItem) tea.Cmd {
	c := m.cache_()
	for _, it := range items {
		c.snooze(it.Key, m.now.Add(m.cfg.settings.Snooze))
	}
	return m.saveCache("reminder snooze not saved")
}

// cacheSavedMsg is a cache write's result: a failure becomes a header note.
type cacheSavedMsg struct {
	err  error
	note string
}

// saveCache writes a copy of the cache in a command (file I/O never runs in
// Update).
func (m *Model) saveCache(note string) tea.Cmd {
	path := m.cfg.cachePath
	if path == "" {
		return nil
	}
	c := *m.cache_()
	c.Snoozes = make(map[string]int64, len(m.sig.cache.Snoozes))
	for k, v := range m.sig.cache.Snoozes {
		c.Snoozes[k] = v
	}
	c.Read = append([]string(nil), m.sig.cache.Read...)
	return func() tea.Msg { return cacheSavedMsg{err: c.save(path), note: note} }
}

// reminderKey: arrows choose; Enter acts on the item (an ask answers, a wait
// writes to the agent, a gate opens its thread); i writes to a waiting
// agent; Esc snoozes them all.
func (m *Model) reminderKey(k Key) tea.Cmd {
	d := m.ui.reminder
	switch {
	case k.Code == keyEscape:
		items := d.items
		m.ui.reminder = nil
		return m.snooze(items)
	case k.Code == keyUp || k.Code == keyBacktab || k.Code == keyRune && k.Rune == 'k':
		d.sel = (d.sel + len(d.items) - 1) % len(d.items)
	case k.Code == keyDown || k.Code == keyTab || k.Code == keyRune && k.Rune == 'j':
		d.sel = (d.sel + 1) % len(d.items)
	case k.Code == keyEnter:
		it := d.items[d.sel]
		m.ui.reminder = nil
		switch it.Rank {
		case rankAsk:
			return m.pushAsk(it.AskID)
		case rankGate:
			ref := strings.TrimPrefix(it.Key, "gate:"+it.Agent+":")
			return m.pushMessage(bus.Event{ID: ref, Kind: "cmd", Type: bus.CmdChallenge, From: it.By, Target: it.Agent, Message: it.Text}, it.At)
		}
		m.openMessageCompose("", it.Agent) // a declared wait: Enter answers it like i
	case k.Code == keyRune && k.Rune == 'i':
		if it := d.items[d.sel]; it.Rank != rankAsk {
			m.ui.reminder = nil
			m.openMessageCompose("", it.Agent)
		}
	}
	return nil
}

// reminderRect is the reminder's place: centred, its items' height.
func (m *Model) reminderRect() Rect {
	d := m.ui.reminder
	h := min(m.height-m.layout.Header.H, 3*len(d.items)+2)
	w := min(80, m.width-2)
	return Rect{(m.width - w) / 2, max(m.layout.Header.H, (m.height-h)/2), w, h}
}

func (m Model) drawReminder(g *grid) {
	d, r := m.ui.reminder, m.reminderRect()
	if r.W < 20 || r.H < 3 {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		drawLine(g, r.X, y, r.W, nil, false)
	}
	drawFrame(g, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: fmt.Sprintf("Waiting for you %s %d", GlyphNone, len(d.items)), Role: RoleText}},
		Label: Line{{Text: "enter act " + GlyphNone + " i message " + GlyphNone + " esc later", Role: RoleMuted}}, Focused: true, Hue: RoleAttention})
	var b body
	for i, it := range d.items {
		marker := S(RoleText, "  ")
		if i == d.sel {
			marker = S(RoleAccent, GlyphSelect+" ")
		}
		k := forYouKinds[it.Rank]
		b.segs(marker).seg(RoleAttention, k.word+" "+Preview(it.Agent)+" "+GlyphNone+" "+humanAge(max(0, m.now.Sub(it.At)))).text("\n")
		b.text("    ").seg(RoleText, Preview(it.Text)).text("\n\n")
	}
	for i, l := range trimEnd(b.Lines()) {
		if i >= r.H-2 {
			break
		}
		drawLine(g, r.X+2, r.Y+1+i, r.W-4, l, false)
	}
}

// summaryRect is the summary confirmation's place.
func (m *Model) summaryRect() Rect {
	w := min(72, m.width-2)
	return Rect{(m.width - w) / 2, max(m.layout.Header.H, (m.height-5)/2), w, 5}
}

func (m Model) drawSummary(g *grid) {
	r := m.summaryRect()
	for y := r.Y; y < r.Y+r.H; y++ {
		drawLine(g, r.X, y, r.W, nil, false)
	}
	drawFrame(g, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: "Send summary request?", Role: RoleAttention}}, Focused: true, Hue: RoleAttention})
	drawLine(g, r.X+2, r.Y+1, r.W-4, Line{{Text: "This sends a message to " + Preview(m.recapTarget()) + " and uses its tokens.", Role: RoleText}}, false)
	drawLine(g, r.X+2, r.Y+3, r.W-4, Line{{Text: "y confirm " + GlyphNone + " n or esc cancel", Role: RoleMuted}}, false)
}

// summaryKey: y sends the summary request through the send path, n or Esc
// returns to the recap.
func (m *Model) summaryKey(k Key) tea.Cmd {
	switch {
	case k.Code == keyEscape || k.Code == keyRune && (k.Rune == 'n' || k.Rune == 'N'):
		m.ui.summary = false
	case k.Code == keyRune && (k.Rune == 'y' || k.Rune == 'Y'):
		m.ui.summary = false
		since := "the most recent available history"
		if !m.cfg.priorVisit.IsZero() {
			since = m.cfg.priorVisit.Local().Format(time.RFC3339)
		}
		body := "Summarise where the project stands for me: what was done since " + since +
			", what is in progress, what you expect from me. Answer on the bus with agentbus reply."
		m.ui.summarySent = true
		return m.send(m.recapTarget(), body, true, false)
	}
	return nil
}
