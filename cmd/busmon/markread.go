package main

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
)

// Marking read in Activity (triage-unread §4, spec
// docs/superpowers/specs/2026-11-26-triage-unread-design.md): N marks read
// every unread line no triage item holds (asked first, the lines unread at
// the press), m toggles the selected line, z puts back the last act's marks
// once. Read marks are this busmon's (the cache file, as opening a line);
// nothing is written on the bus.

// markAct is the last N or m: the entries it marked read, and those it
// marked unread.
type markAct struct {
	read, unread []string
}

// heldLines are the lines the triage items hold (their Held), by id.
func heldLines(items []attentionItem) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		for _, id := range it.Held {
			out[id] = true
		}
	}
	return out
}

// markAllSet is what N marks now: every unread line, of agent when not ""
// (the filter's @agent match), that no triage item holds.
func (m *Model) markAllSet(agent string) []string {
	held := heldLines(m.forYou)
	match := parseFilter("@" + agent)
	var out []string
	for _, fl := range m.feed.feed {
		if fl.sep || fl.mark != markUnread || held[fl.ev.ID] || agent != "" && !match.match(fl) {
			continue
		}
		out = append(out, fl.ev.ID)
	}
	return out
}

// askMarkAll is N: the question, over the lines unread now.
func (m *Model) askMarkAll(agent string) {
	m.refresh()
	set := m.markAllSet(agent)
	if len(set) == 0 {
		m.ui.flash, m.ui.flashErr = "nothing to mark: no unread line outside triage", false
		return
	}
	m.ui.markAsk = set
	title := fmt.Sprintf("mark %d lines read?", len(set))
	if agent != "" {
		title = fmt.Sprintf("mark %d lines of %s read?", len(set), agent)
	}
	m.openConfirm(&confirmDialog{Title: title, Body: []string{"none a triage item holds; z in Activity undoes it"}, Yes: "Mark read", No: "Keep",
		OnYes: func(m *Model) tea.Cmd {
			set := m.ui.markAsk
			m.ui.markAsk = nil
			act := &markAct{}
			for _, id := range set {
				act.read = append(act.read, m.markLineReadIDs(id)...)
			}
			m.ui.markUndo = act
			m.ui.flash, m.ui.flashErr = fmt.Sprintf("%d marked read %s z undo", len(set), GlyphNone), false
			return nil
		},
		OnNo: func(m *Model) tea.Cmd {
			m.ui.markAsk = nil
			m.ui.flash, m.ui.flashErr = "nothing done", false
			return nil
		}})
}

// markLineReadIDs reads the line that holds id, and is the entries it
// marked (those not read before).
func (m *Model) markLineReadIDs(id string) []string {
	fl, ok := m.feed.lineOf(id)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range lineIDs(fl) {
		if m.feed.unread.forPerson[x] && !m.feed.unread.read[x] {
			m.markRead(x)
			out = append(out, x)
		}
	}
	return out
}

// markUnreadID takes id's read mark away, in the cache too.
func (m *Model) markUnreadID(id string) {
	if !m.feed.unread.read[id] {
		return
	}
	delete(m.feed.unread.read, id)
	c := m.cache_()
	c.Read = slices.DeleteFunc(slices.Clone(c.Read), func(r string) bool { return r == id })
	m.sig.readDirty = true
}

// toggleMark is m on the selected Activity line: unread becomes read, read
// becomes unread; a line a triage item holds, or one before the last
// visit, is never marked.
func (m *Model) toggleMark() {
	sel := m.lists.activity.Selected()
	var fl feedLine
	found := false
	for _, x := range m.feed.feed {
		if !x.sep && x.id == sel {
			fl, found = x, true
		}
	}
	if !found {
		m.ui.flash, m.ui.flashErr = "m: select a line in Activity", true
		return
	}
	if heldLines(m.forYou)[fl.ev.ID] {
		m.ui.flash, m.ui.flashErr = "this line is a triage item: open it from T", true
		return
	}
	if fl.at.UnixMilli() < m.feed.unread.floor {
		m.ui.flash, m.ui.flashErr = "a line from before your last visit is never marked", false
		return
	}
	// The row's own mark decides the direction, for all its entries: a
	// report row stands for its report and its notices, some read, some not
	// (reviewer-codex).
	act := &markAct{}
	if fl.mark == markUnread || fl.mark == markSuperseded {
		act.read = m.markLineReadIDs(fl.ev.ID)
		m.ui.markUndo = act
		m.ui.flash, m.ui.flashErr = "marked read "+GlyphNone+" z undo", false
		return
	}
	for _, x := range lineIDs(fl) {
		if m.feed.unread.read[x] {
			m.markUnreadID(x)
			act.unread = append(act.unread, x)
		}
	}
	if len(act.unread) == 0 {
		m.ui.flash, m.ui.flashErr = "not a line for you: it carries no unread mark", false
		return
	}
	m.ui.markUndo = act
	m.ui.flash, m.ui.flashErr = "marked unread "+GlyphNone+" z undo", false
}

// undoMarks is z: the last act's marks put back, once.
func (m *Model) undoMarks() {
	a := m.ui.markUndo
	if a == nil {
		m.ui.flash, m.ui.flashErr = "nothing to undo: no line marked from this busmon", false
		return
	}
	m.ui.markUndo = nil
	for _, id := range a.read {
		m.markUnreadID(id)
	}
	for _, id := range a.unread {
		m.markRead(id)
	}
	m.ui.flash, m.ui.flashErr = "undone", false
}
