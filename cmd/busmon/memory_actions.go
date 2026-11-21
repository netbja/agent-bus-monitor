package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/personmem"
)

// The Memory page's actions (spec 2026-11-21-person-memory-design r2, §4.4,
// step 4 part b), each the person's own act on the memory's host:
//   - a: the person's words in the compose window, remembered with busmon as
//     their source (§4.3.1);
//   - x: on a topic's page, forget the selected observation, after y;
//   - e: the topic's file in $VISUAL or $EDITOR; on save the whole memory is
//     checked, and a clean one commits the file, else it stays as saved and
//     the page says what is wrong.
// busmon in an agent's environment, or off the host, does none of them.

// memoryWriteBudget bounds one write: the store's lock, then git.
const memoryWriteBudget = 10 * time.Second

// personMemoryMsg is a write's outcome: what it says, or why it failed;
// add marks a (its text sent, so a failure opens it again).
type personMemoryMsg struct {
	add        bool
	sent, said string
	err        error
}

// memoryEditMsg is the editor's return, on the topic's file.
type memoryEditMsg struct {
	topic string
	err   error
}

// parseMemoryAdd reads a's window: its first line topic type importance,
// then keep-both or supersedes=<id> when a second decision is meant; the
// rest the person's words, whitespace collapsed (a quote is one line).
func parseMemoryAdd(text string) (personmem.Request, error) {
	head, rest, _ := strings.Cut(strings.TrimLeft(text, "\n "), "\n")
	f := strings.Fields(head)
	if len(f) < 3 {
		return personmem.Request{}, errors.New("the first line is topic type importance, as in: notifications preference standard")
	}
	r := personmem.Request{Topic: f[0], Type: f[1], Importance: f[2]}
	for _, x := range f[3:] {
		switch id, ok := strings.CutPrefix(x, "supersedes="); {
		case x == "keep-both":
			r.KeepBoth = true
		case ok && id != "":
			r.Supersedes = id
		default:
			return personmem.Request{}, errors.New("after the importance, only keep-both or supersedes=<id> (a second decision on the topic)")
		}
	}
	if r.Quote = strings.Join(strings.Fields(rest), " "); r.Quote == "" {
		return personmem.Request{}, errors.New("your words go on the lines after the first")
	}
	return r, nil
}

// editorArgv is the editor on path: $VISUAL, else $EDITOR (with its own
// words), else vi.
func editorArgv(getenv func(string) string, path string) []string {
	ed := strings.Fields(cmp.Or(getenv("VISUAL"), getenv("EDITOR")))
	if len(ed) == 0 {
		ed = []string{"vi"}
	}
	return append(ed, path)
}

// memoryKey is the Memory pages' keys: a pending x's answer first, then a,
// e, x, enter and the selection. done: the key is the page's.
func (m *Model) memoryKey(p *page, act Action, k Key) (tea.Cmd, bool) {
	if p.kind != ovMemory && p.kind != ovMemoryTopic {
		return nil, false
	}
	if id := p.forget; id != "" { // x asked: y forgets, any other key keeps
		p.forget, p.dirty = "", true
		if k.Code == keyRune && k.Rune == 'y' {
			return m.memoryForget(p, id), true
		}
		m.ui.flash, m.ui.flashErr = id+" kept", false
		m.refresh()
		return nil, true
	}
	switch {
	case act == ActMemoryAdd:
		m.openMemoryCompose(p)
		return nil, true
	case act == ActMemoryEdit:
		return m.memoryEdit(p), true
	case act == ActMemoryForget:
		m.askMemoryForget(p)
		return nil, true
	case p.kind == ovMemory && k.Code == keyEnter:
		if p.sel < len(p.mitems) {
			return m.push(page{kind: ovMemoryTopic, key: p.mitems[p.sel].topic, mem: p.mem}), true
		}
		return nil, true
	case act == ActUp || act == ActDown || act == ActPageUp || act == ActPageDown || act == ActFirst || act == ActLast:
		items := p.mitems
		if p.kind == ovMemoryTopic {
			items = p.oitems
		}
		m.moveSel(p, act, m.readerRows(), func() []int {
			lines := make([]int, len(items))
			for i, it := range items {
				lines[i] = it.line
			}
			return lines
		})
		p.dirty = true
		m.refresh()
		return nil, true
	}
	return nil, false
}

// memoryTopic is the topic a and e act on: the selected row, or the page's.
func memoryTopic(p *page) string {
	if p.kind == ovMemoryTopic {
		return p.key
	}
	if p.sel < len(p.mitems) {
		return p.mitems[p.sel].topic
	}
	return ""
}

// memoryRefusal is why busmon may not do what here: off the memory's host,
// or in an agent's environment; "" when it may.
func (m *Model) memoryRefusal(p *page, what, command string) string {
	if p.mem == nil || !p.mem.local {
		where := "the bus's host"
		if p.mem != nil && p.mem.host != "" {
			where = p.mem.host
		}
		return "the person's memory lives on " + where + ": " + what + " is done there"
	}
	return agentcaller.Refuse(context.Background(), m.env, m.cfg.ancestor, what, command)
}

// memoryGetenv is the store's environment: the shell's, else the process's.
func (m *Model) memoryGetenv() func(string) string {
	if m.cfg.getenv != nil {
		return m.cfg.getenv
	}
	return os.Getenv
}

// memoryWrite runs one store write off the update loop.
func (m *Model) memoryWrite(add bool, sent string, write func(context.Context, *personmem.Store) (string, error)) tea.Cmd {
	getenv, ctx := m.memoryGetenv(), cmp.Or(m.cfg.ctx, context.Background())
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, memoryWriteBudget)
		defer cancel()
		s, err := personmem.Open(getenv)
		if err != nil {
			return personMemoryMsg{add: add, sent: sent, err: err}
		}
		said, err := write(cc, s)
		return personMemoryMsg{add: add, sent: sent, said: said, err: err}
	}
}

// openMemoryCompose is a: the window, with the draft kept, else the
// selected topic and a filing to change.
func (m *Model) openMemoryCompose(p *page) {
	if why := m.memoryRefusal(p, "remembering", "agentbus mem person remember"); why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return
	}
	if c := m.ui.compose; c != nil && c.kind == composeMessage {
		m.saveMessageCompose()
	}
	text := m.ui.memoryDraft
	if t := memoryTopic(p); text == "" && t != "" {
		text = t + " preference standard\n"
	}
	m.ui.compose = &composeState{kind: composeMemory, area: newField(text, 2*personmem.MaxQuote)}
	m.ui.askDraft, m.ui.broadcastDraft = "", ""
	if m.ui.line != lineNone {
		m.leaveLine(true)
	}
	m.layoutCompose()
}

// sendMemoryCompose is Ctrl+S in a's window: a filing or words missing keep
// the window open and say why; else the window closes, its text kept until
// the store says it is written (a refusal opens it again).
func (m *Model) sendMemoryCompose(c *composeState) tea.Cmd {
	r, err := parseMemoryAdd(c.area.value())
	if err != nil {
		m.ui.flash, m.ui.flashErr = err.Error(), true
		return nil
	}
	if p := m.rd.top(); p != nil {
		if why := m.memoryRefusal(p, "remembering", "agentbus mem person remember"); why != "" {
			m.ui.flash, m.ui.flashErr = why, true
			return nil
		}
	}
	who := m.cfg.self
	if who == "" || who == "pilot" {
		m.ui.flash, m.ui.flashErr = "name yourself first (agentbus identity set <name>)", true
		return nil
	}
	r.Busmon, r.By, r.At = true, who, cmp.Or(m.now, time.Now())
	sent := c.area.value()
	m.ui.compose, m.ui.memoryDraft = nil, sent
	return m.memoryWrite(true, sent, func(ctx context.Context, s *personmem.Store) (string, error) {
		o, err := s.Remember(ctx, nil, r)
		return "remembered " + o.ID + " in " + r.Topic, err
	})
}

// askMemoryForget is x on a topic's page: the selected observation, asked first.
func (m *Model) askMemoryForget(p *page) {
	if p.kind != ovMemoryTopic {
		m.ui.flash, m.ui.flashErr = "x forgets one observation: open its topic (enter), then choose it", true
		return
	}
	if why := m.memoryRefusal(p, "forgetting", "agentbus mem person forget"); why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return
	}
	if p.sel >= len(p.oitems) {
		return
	}
	id, quote := p.oitems[p.sel].id, ""
	for _, t := range p.mem.topics {
		for _, o := range t.obs {
			if o.ID == id {
				quote = o.Quote
			}
		}
	}
	p.forget, p.dirty = id, true
	m.ui.flash, m.ui.flashErr = "forget "+id+" (\""+Preview(quote)+"\")? y forgets (git history keeps it until agentbus mem person purge), any other key keeps it", true
	m.refresh()
}

// memoryForget forgets id, the person's yes given.
func (m *Model) memoryForget(p *page, id string) tea.Cmd {
	if why := m.memoryRefusal(p, "forgetting", "agentbus mem person forget"); why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	return m.memoryWrite(false, "", func(ctx context.Context, s *personmem.Store) (string, error) {
		return s.Forget(ctx, id)
	})
}

// memoryEdit is e: the terminal handed to the editor on the topic's file.
func (m *Model) memoryEdit(p *page) tea.Cmd {
	if why := m.memoryRefusal(p, "editing the person's memory", "$EDITOR on the topic's file"); why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	topic := memoryTopic(p)
	if topic == "" {
		m.ui.flash, m.ui.flashErr = "no topic to edit: a remembers the first words", true
		return nil
	}
	s, err := personmem.Open(m.memoryGetenv())
	if err != nil {
		m.ui.flash, m.ui.flashErr = "the memory cannot be opened: "+Preview(err.Error()), true
		return nil
	}
	argv := editorArgv(m.memoryGetenv(), filepath.Join(s.Dir, topic+".md"))
	return tea.ExecProcess(exec.Command(argv[0], argv[1:]...), func(err error) tea.Msg {
		return memoryEditMsg{topic: topic, err: err}
	})
}

// gotMemoryEdit: the editor returned; the check, then the commit when clean.
func (m *Model) gotMemoryEdit(msg memoryEditMsg) tea.Cmd {
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = "the editor: "+Preview(msg.err.Error()), true
		m.rereadMemory()
		return nil
	}
	topic := msg.topic
	return m.memoryWrite(false, "", func(ctx context.Context, s *personmem.Store) (string, error) {
		fs, committed, err := s.CommitEdit(ctx, topic)
		switch {
		case err != nil:
			return "", err
		case len(fs) > 0:
			more := ""
			if len(fs) > 1 {
				more = fmt.Sprintf(" (and %d more)", len(fs)-1)
			}
			return "", fmt.Errorf("%s edited, not committed: %s%s; the file stays as saved (agentbus mem person check)", topic, fs[0], more)
		case !committed:
			return topic + " saved unchanged", nil
		}
		return topic + " edited, checked and committed", nil
	})
}

// gotPersonMemory says a write's outcome and reads the memory again; a
// refused a opens its window again, its text kept.
func (m *Model) gotPersonMemory(msg personMemoryMsg) {
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = Preview(msg.err.Error()), true
		if msg.add && m.ui.compose == nil {
			m.ui.compose = &composeState{kind: composeMemory, area: newField(m.ui.memoryDraft, 2*personmem.MaxQuote)}
			m.layoutCompose()
		}
	} else {
		if msg.add && m.ui.memoryDraft == msg.sent { // an edit made meanwhile is kept
			m.ui.memoryDraft = ""
		}
		m.ui.flash, m.ui.flashErr = msg.said, false
	}
	m.rereadMemory()
}

// rereadMemory drops every Memory page's read: each reads the store again
// when drawn.
func (m *Model) rereadMemory() {
	for i := range m.rd.pages {
		if p := &m.rd.pages[i]; p.kind == ovMemory || p.kind == ovMemoryTopic {
			p.mem, p.dirty = nil, true
		}
	}
	m.refresh()
}
