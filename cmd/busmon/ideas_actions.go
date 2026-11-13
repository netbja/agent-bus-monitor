package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ideaMsg is an idea write's outcome: what to say, the refusal, and the
// list read right after it (nil when that read failed).
type ideaMsg struct {
	said  string
	err   error
	ideas []bus.Idea
	add   bool   // an add: its draft is spent only once written
	sent  string // an add's text as sent: the draft is spent only if it is still that
}

// ideasKey is a key on F6 or an idea's page; done when it was the page's.
// While the picker is open it takes every key: the arrows choose, y sets,
// esc (or q) cancels.
func (m *Model) ideasKey(p *page, act Action, k Key) (tea.Cmd, bool) {
	if pk := p.ideas.pick; pk != nil {
		n := len(bus.IdeaStates)
		switch act {
		case ActUp:
			pk.sel = (pk.sel + n - 1) % n
		case ActDown:
			pk.sel = (pk.sel + 1) % n
		case ActCopy:
			return m.setIdeaState(p), true
		case ActClose:
			p.ideas.pick = nil
		}
		p.dirty = true
		m.refresh()
		return nil, true
	}
	switch {
	case act == ActIdeaState:
		id := p.key
		if p.kind == ovIdeas {
			if p.sel >= len(p.iitems) {
				return nil, true
			}
			id = p.iitems[p.sel].id
		}
		for _, it := range m.snap.ideas {
			if it.ID == id {
				sel := 0
				for i, st := range bus.IdeaStates {
					if st == it.State {
						sel = i
					}
				}
				p.ideas.pick, p.dirty = &ideaPick{id: id, from: it.State, sel: sel}, true
				m.refresh()
			}
		}
		return nil, true
	case p.kind != ovIdeas:
		return nil, false
	case act == ActIdeaFilter:
		p.ideas.filter, p.ideas.selID, p.sel, p.dirty = nextIdeaFilter(p.ideas.filter), "", 0, true
		m.refresh()
		return nil, true
	case act == ActIdeaAdd:
		m.openIdeaCompose()
		return nil, true
	case k.Code == keyEnter:
		if p.sel < len(p.iitems) {
			return m.pushIdea(p.iitems[p.sel].id), true
		}
		return nil, true
	case act == ActUp || act == ActDown || act == ActPageUp || act == ActPageDown || act == ActFirst || act == ActLast:
		p.ideas.selID = "" // the keys move by index; the rebuild records the new idea
		m.moveSel(p, act, m.readerRows(), ideaLines(p))
		return nil, true
	}
	return nil, false
}

// ideaLines are F6's rows' body lines, read after a rebuild.
func ideaLines(p *page) func() []int {
	return func() []int {
		lines := make([]int, len(p.iitems))
		for i, it := range p.iitems {
			lines[i] = it.line
		}
		return lines
	}
}

// setIdeaState is y in the picker: the state chosen, set as the person,
// the state the picker opened on as the expected one (a change made
// meanwhile elsewhere is refused, never overwritten).
func (m *Model) setIdeaState(p *page) tea.Cmd {
	pk := *p.ideas.pick
	to := bus.IdeaStates[pk.sel]
	p.ideas.pick, p.dirty = nil, true
	defer m.refresh()
	if to == pk.from {
		m.ui.flash, m.ui.flashErr = pk.id+": already "+to, false
		return nil
	}
	if a := m.env("AGENT_BUS_AGENT"); a != "" { // an agent's busmon inherits the person's name (reviewer-codex)
		m.ui.flash, m.ui.flashErr = "this busmon runs in "+Preview(a)+"'s agent environment (AGENT_BUS_AGENT): only the person sets an idea's state", true
		return nil
	}
	who := m.cfg.self
	if who == "" || who == "pilot" {
		m.ui.flash, m.ui.flashErr = "name yourself first (agentbus identity set <name>): only a person sets an idea's state", true
		return nil
	}
	return m.ideaWrite(pk.id+": "+pk.from+" → "+to, func(ctx context.Context, w busWriter) error {
		return w.SetIdeaState(ctx, pk.id, pk.from, to, who)
	})
}

// env reads busmon's environment (the shell config's, else the process's).
func (m *Model) env(k string) string {
	if m.cfg.getenv != nil {
		return m.cfg.getenv(k)
	}
	return os.Getenv(k)
}

// openIdeaCompose is a on F6: the compose window for a new idea, on its
// draft. A message being written is kept as the message draft.
func (m *Model) openIdeaCompose() {
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		m.saveMessageCompose()
	}
	m.ui.compose = &composeState{kind: composeIdea, area: newField(m.ui.ideaDraft, bus.IdeaMaxTitle+1+bus.IdeaMaxText)}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	if m.ui.line != lineNone {
		m.leaveLine(true)
	}
	m.layoutCompose()
}

// sendIdeaCompose is Ctrl+S in the idea window: its first line the title,
// the rest the text, added by the person at "to discuss". A title too long
// or none keeps the window open and says why.
func (m *Model) sendIdeaCompose(c *composeState) tea.Cmd {
	if a := m.env("AGENT_BUS_AGENT"); a != "" { // it would credit the person with the agent's idea (reviewer-codex)
		m.ui.flash, m.ui.flashErr = "this busmon runs in "+Preview(a)+"'s agent environment (AGENT_BUS_AGENT): an agent adds its idea with agentbus idea add, under its own name", true
		return nil
	}
	if m.ui.ideaAdding { // one add at a time: the kept text sent twice would be two ideas (reviewer-codex)
		m.ui.flash, m.ui.flashErr = "an add is on its way: wait for its result", true
		return nil
	}
	title, text, _ := strings.Cut(strings.TrimLeft(c.area.value(), "\n "), "\n")
	title, text = strings.TrimSpace(title), strings.TrimSpace(text)
	switch {
	case title == "":
		m.ui.flash, m.ui.flashErr = "an idea needs a title: its first line", true
		return nil
	case len([]rune(title)) > bus.IdeaMaxTitle:
		m.ui.flash, m.ui.flashErr = fmt.Sprintf("the title (the first line) is %d characters, at most %d: the rest goes on the next lines", len([]rune(title)), bus.IdeaMaxTitle), true
		return nil
	}
	who := m.cfg.self
	if who == "" || who == "pilot" {
		m.ui.flash, m.ui.flashErr = "name yourself first (agentbus identity set <name>)", true
		return nil
	}
	// The draft stays until the idea is written (reviewer-codex): a failed
	// write keeps it, and a reopens it.
	sent := c.area.value()
	m.ui.compose, m.ui.ideaDraft = nil, sent
	// One request id per text sent (reviewer-codex): a retry after a failed
	// reply, the script maybe committed, finds the idea it made.
	if m.ui.ideaReq == "" || m.ui.ideaReqText != sent {
		m.ui.ideaReq, m.ui.ideaReqText = bus.NewIntentNonce(), sent
	}
	req := m.ui.ideaReq
	cmd := m.ideaWrite("added: "+Preview(title), func(ctx context.Context, w busWriter) error {
		_, err := w.AddIdea(ctx, bus.IdeaDef{Title: title, Text: text, By: who, Request: req})
		return err
	})
	if cmd == nil {
		return nil
	}
	m.ui.ideaAdding = true
	return func() tea.Msg {
		msg := cmd().(ideaMsg)
		msg.add, msg.sent = true, sent
		return msg
	}
}

// ideaWrite runs one idea write off the update loop, then reads the list
// again so the page shows it at once.
func (m *Model) ideaWrite(said string, write func(context.Context, busWriter) error) tea.Cmd {
	w, ctx := m.cfg.w, m.cfg.ctx
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		if err := write(cc, w); err != nil {
			return ideaMsg{err: err}
		}
		ideas, err := w.Ideas(cc)
		if err != nil {
			ideas = nil
		}
		return ideaMsg{said: said, ideas: ideas}
	}
}

// gotIdea says the outcome and keeps the list read after it, in the shared
// state too, so the next snapshot does not bring the old one back.
func (m *Model) gotIdea(msg ideaMsg) {
	if msg.add {
		m.ui.ideaAdding = false
	}
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = "idea: "+msg.err.Error(), true
		if msg.add {
			m.ui.flash += " (your text is kept: a reopens it)"
		}
		return
	}
	if msg.add && m.ui.ideaDraft == msg.sent { // an edit made meanwhile is kept
		m.ui.ideaDraft = ""
	}
	if msg.add && m.ui.ideaReqText == msg.sent { // written: the next text is a new request
		m.ui.ideaReq, m.ui.ideaReqText = "", ""
	}
	m.ui.flash, m.ui.flashErr = msg.said, false
	if msg.ideas != nil {
		m.snap.ideas, m.snap.ideasErr = msg.ideas, false
		if st := m.cfg.st; st != nil {
			st.mu.Lock()
			st.ideas, st.ideasErr = msg.ideas, false
			st.mu.Unlock()
		}
		if p := m.rd.top(); p != nil {
			p.dirty = true
		}
	}
}
