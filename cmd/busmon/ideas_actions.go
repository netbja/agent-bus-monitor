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
	add   bool        // an add: its draft is spent only once written
	sent  string      // an add's text as sent: the draft is spent only if it is still that
	set   *ideaChange // a state change written: u's to undo (nil for an add)
	undo  bool        // an undo: once written, nothing is left to undo
}

// ideaChange is one state change this busmon made: u puts from back, as a
// compare-and-set from to (ideas-triage).
type ideaChange struct{ id, from, to string }

// ideaKeyStates are the direct keys' states (ideas-triage, the person's
// y / later / no triage, master's y = decided on these pages).
var ideaKeyStates = map[rune]string{'y': bus.IdeaDecided, 'l': bus.IdeaLater, 'd': bus.IdeaDoing, 'x': bus.IdeaDropped}

// ideaCopyText is what Y copies of an idea: its title, then its text.
func ideaCopyText(it bus.Idea) string {
	if strings.TrimSpace(it.Text) == "" {
		return it.Title
	}
	return it.Title + "\n\n" + it.Text
}

// ideasKey is a key on F6 or an idea's page; done when it was the page's.
// While the picker is open it takes every key: ← → choose (the states are
// laid across), enter sets, esc (or q) cancels (the person,
// idea-state-keys: no y to type).
func (m *Model) ideasKey(p *page, act Action, k Key) (tea.Cmd, bool) {
	if pk := p.ideas.pick; pk != nil {
		n := len(bus.IdeaStates)
		switch {
		case k.Code == keyLeft:
			pk.sel = (pk.sel + n - 1) % n
		case k.Code == keyRight:
			pk.sel = (pk.sel + 1) % n
		case k.Code == keyEnter:
			return m.setIdeaState(p), true
		case act == ActClose:
			p.ideas.pick = nil
		}
		p.dirty = true
		m.refresh()
		return nil, true
	}
	switch {
	case act == ActIdeaUndo:
		return m.undoIdeaState(p), true
	case act == ActIdeaSet:
		id, cur := m.selectedIdea(p)
		to, ok := ideaKeyStates[k.Rune]
		if id == "" || !ok {
			return nil, true
		}
		return m.writeIdeaState(p, id, cur, to, false), true
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

// selectedIdea is the page's idea and its state as shown: the selected row
// on F6, the page's own idea on its page ("" when none).
func (m *Model) selectedIdea(p *page) (string, string) {
	id := p.key
	if p.kind == ovIdeas {
		if p.sel >= len(p.iitems) {
			return "", ""
		}
		id = p.iitems[p.sel].id
	}
	for _, it := range m.snap.ideas {
		if it.ID == id {
			return id, it.State
		}
	}
	return "", ""
}

// setIdeaState is enter in the picker: the state chosen, set as the person,
// the state the picker opened on as the expected one (a change made
// meanwhile elsewhere is refused, never overwritten).
func (m *Model) setIdeaState(p *page) tea.Cmd {
	pk := *p.ideas.pick
	p.ideas.pick = nil
	return m.writeIdeaState(p, pk.id, pk.from, bus.IdeaStates[pk.sel], false)
}

// undoIdeaState is u: this busmon's last change put back, from the state it
// set (a change made since, here or elsewhere, is refused).
func (m *Model) undoIdeaState(p *page) tea.Cmd {
	last := m.ui.ideaLast
	if last == nil {
		m.ui.flash, m.ui.flashErr = "nothing to undo: no state changed from this busmon", false
		return nil
	}
	return m.writeIdeaState(p, last.id, last.to, last.from, true)
}

// writeIdeaState sets id from from to to, as the person: the picker, the
// direct keys and u all come here, with the same refusals.
func (m *Model) writeIdeaState(p *page, id, from, to string, undo bool) tea.Cmd {
	p.dirty = true
	defer m.refresh()
	if to == from {
		m.ui.flash, m.ui.flashErr = id+": already "+to, false
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
	cmd := m.ideaWrite(id+": "+from+" → "+to, func(ctx context.Context, w busWriter) error {
		return w.SetIdeaState(ctx, id, from, to, who)
	})
	if cmd == nil {
		return nil
	}
	change := &ideaChange{id: id, from: from, to: to}
	return func() tea.Msg {
		msg := cmd().(ideaMsg)
		msg.set, msg.undo = change, undo
		return msg
	}
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
	switch {
	case msg.undo: // once: u again has nothing left
		m.ui.ideaLast = nil
		m.ui.flash += " (undone)"
	case msg.set != nil:
		m.ui.ideaLast = msg.set
	}
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
