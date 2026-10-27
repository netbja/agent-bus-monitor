package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The signal inventory (busmon Charm spec, "Signals"): every one driven by
// the snapshot and the tick, none by a draw. The bell rings once per new
// waiting declaration, new ask id and new observed-prompt episode, unless
// bell = false; the waiting fade is a fadeMsg scheduled only while an agent
// waits, with fade on and a palette that has the shades; the recap opens by
// itself after AutoAfter away; the reminder after PopupAfter, never over the
// compose window or another dialog.

// signalState is what the signals remember, behind a pointer like the lists.
type signalState struct {
	rung          map[string]string // agent -> the waiting declaration rung for
	rungAsks      map[string]bool   // ask ids rung for
	bells         int               // bells rung (what the tests count)
	fading        bool              // a fadeMsg is scheduled
	recap         recapModel        // the last recap built
	recapLoading  bool
	recapPending  bool // built, opens once the person is not busy
	recapAutoDone bool // the first poll's automatic check is done
	cache         *busmonCache
}

func newSignals(c *busmonCache) *signalState {
	return &signalState{rung: map[string]string{}, rungAsks: map[string]bool{}, cache: c}
}

// recapMsg is a recap built by recapCmd.
type recapMsg struct{ model recapModel }

// ring decides the bell after a poll, as tview's onTick does: one ring per
// new ask id (its waiting declaration covered), per new waiting declaration
// without an ask, per observed-prompt episode (its since).
func (m *Model) ring() bool {
	s, ring := m.sig, false
	seen := map[string]bool{}
	for _, n := range asksFor(m.snap.agents) {
		seen[n] = true
		a := m.snap.agents[n]
		key := "status:" + a.stateAt.String()
		if promptWaiting(a) { // one ring per blocked episode of its tab
			key = "prompt:" + a.obsSince.String()
		}
		switch {
		case a.askID != "":
			if !s.rungAsks[a.askID] {
				s.rungAsks[a.askID], ring = true, true
			}
			s.rung[n] = key
		case s.rung[n] != key:
			s.rung[n], ring = key, true
		}
	}
	for n := range s.rung {
		if !seen[n] {
			delete(s.rung, n)
		}
	}
	return ring && m.cfg.settings.Bell
}

// bell is the terminal bell, written raw (herdr marks the tab even when it
// is hidden).
func (m *Model) bell() tea.Cmd {
	m.sig.bells++
	return tea.Raw("\a")
}

// animated: the waiting word fades now.
func (m *Model) animated() bool {
	return len(asksFor(m.snap.agents)) > 0 && m.cfg.settings.Fade && th.Animated()
}

// scheduleFade schedules the next fade frame while one is due and none is.
func (m *Model) scheduleFade() tea.Cmd {
	if !m.animated() || m.sig.fading {
		return nil
	}
	m.sig.fading = true
	return tea.Tick(fadeFrame, func(t time.Time) tea.Msg { return fadeMsg(t) })
}

// afterPoll are the signals of a new snapshot.
func (m *Model) afterPoll() tea.Cmd {
	var cmds []tea.Cmd
	if m.ring() {
		cmds = append(cmds, m.bell())
	}
	cmds = append(cmds, m.scheduleFade(), m.autoRecap())
	return tea.Batch(cmds...)
}

// afterUpdate opens what waited for the person to be free: the recap, then
// the reminder.
func (m *Model) afterUpdate() tea.Cmd {
	if m.width == 0 {
		return nil
	}
	m.maybeOpenRecap()
	return m.maybeReminder()
}

// autoRecap, once, at the first poll: it records this visit and builds the
// recap when the last one is AutoAfter old or more.
func (m *Model) autoRecap() tea.Cmd {
	s := m.sig
	if s.recapAutoDone {
		return nil
	}
	s.recapAutoDone = true
	prior, after := m.cfg.priorVisit, m.cfg.settings.AutoAfter
	m.cache_().LastVisit = m.now.UnixMilli()
	save := m.saveCache("last visit not saved")
	if prior.IsZero() || after <= 0 || m.now.Sub(prior) < after {
		return save
	}
	return tea.Batch(save, m.requestRecap(true))
}

// requestRecap builds the recap in a command (it reads the bus).
func (m *Model) requestRecap(auto bool) tea.Cmd {
	s := m.sig
	if s.recapLoading {
		return nil
	}
	r, ok := m.cfg.w.(recapReader)
	if !ok || m.cfg.st == nil {
		s.recap = buildRecap(recapInput{Now: m.now, Since: m.cfg.priorVisit, Human: m.cfg.self,
			Errors: map[string]error{"Bus": fmt.Errorf("recap reader unavailable")}})
		s.recapPending = true
		return nil
	}
	s.recapLoading = true
	if auto {
		m.ui.flash, m.ui.flashErr = "building project recap"+GlyphEllipsis, false
	}
	ctx, st, since, human, unread := m.cfg.ctx, m.cfg.st, m.cfg.priorVisit, m.cfg.self, m.unreadEvents()
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 7*time.Second)
		defer cancel()
		return recapMsg{model: readRecap(c, r, st, since, human, unread)}
	}
}

// unreadEvents are the Activity entries still unread for the person.
func (m *Model) unreadEvents() []bus.Event {
	var out []bus.Event
	for _, fl := range m.feed.feed {
		if !fl.sep && fl.mark == markUnread {
			out = append(out, fl.ev)
		}
	}
	return out
}

func (m *Model) gotRecap(msg recapMsg) {
	s := m.sig
	s.recap, s.recapLoading, s.recapPending = msg.model, false, true
	if m.ui.flash == "building project recap"+GlyphEllipsis {
		m.ui.flash = ""
	}
}

// maybeOpenRecap opens a built recap as a reader page once the person is free.
func (m *Model) maybeOpenRecap() {
	s := m.sig
	if !s.recapPending || m.busyForModal() {
		return
	}
	s.recapPending = false
	m.push(page{kind: ovRecap, title: "Where was I?", recap: s.recap, sel: firstRecapSelection(s.recap.Rows)})
}

// recapTarget is who the summary request goes to.
func (m *Model) recapTarget() string { return m.sig.recap.Target }

// recapKey: arrows (and Tab) move along the selectable rows, Enter opens one
// over the recap (Esc returns to it), Esc closes.
func (m *Model) recapKey(k Key) tea.Cmd {
	p := m.rd.top()
	switch {
	case k.Code == keyEscape:
		m.pop()
	case k.Code == keyUp || k.Code == keyBacktab || k.Code == keyRune && k.Rune == 'k':
		m.moveRecap(p, -1)
	case k.Code == keyDown || k.Code == keyTab || k.Code == keyRune && k.Rune == 'j':
		m.moveRecap(p, 1)
	case k.Code == keyEnter:
		return m.activateRecapRow(p)
	}
	return nil
}

func (m *Model) moveRecap(p *page, step int) {
	rows := p.recap.Rows
	i := p.sel
	for n := 0; n < len(rows); n++ {
		i = (i + step + len(rows)) % len(rows)
		if rows[i].Selectable {
			p.sel, p.dirty = i, true
			m.refresh()
			if line := p.recapLine; line < p.top || line >= p.top+m.readerRows() {
				p.top = max(0, line-3)
				m.clampPage(p)
			}
			return
		}
	}
}

// activateRecapRow opens what a recap row names, over the recap.
func (m *Model) activateRecapRow(p *page) tea.Cmd {
	if p.sel < 0 || p.sel >= len(p.recap.Rows) || !p.recap.Rows[p.sel].Selectable {
		return nil
	}
	r := p.recap.Rows[p.sel]
	switch r.Kind {
	case "summary":
		if p.recap.SummaryDisabled == "" {
			m.ui.summary = true
		}
	case "ask":
		return m.pushAskOf(r.Agent)
	case "agent":
		return m.pushAgent(r.Agent)
	case "board":
		if _, ok := m.snap.board[r.Task]; ok {
			return m.push(page{kind: ovTask, key: r.Task})
		}
	case "thread":
		return m.pushMessage(r.Event, eventTimestamp(r.Event.ID))
	case "verdict":
		if v := r.Verdict; v != nil {
			var b body
			b.seg(RoleAttention, strings.ToUpper(v.Decision)).text("\n").seg(RoleMuted, v.Reviewer+" reviewed "+v.Author).text("\n\n").text(Preview(v.Message))
			return m.push(page{kind: ovMessage, title: "Verdict " + GlyphNone + " " + v.Subject, body: b.Lines(), fixed: true})
		}
	case "swap":
		if r.Swap != nil {
			return m.push(page{kind: ovMessage, title: "Swap " + GlyphNone + " " + r.Swap.Role, body: []Line{{S(RoleText, Preview(swapDescription(*r.Swap)))}}, fixed: true})
		}
	case "services":
		return m.servicesCmd()
	case "pause":
		return m.push(page{kind: ovMessage, title: "Project pause", body: []Line{{S(RoleText, Preview(r.Detail))}}, fixed: true})
	}
	return nil
}
