package main

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The triage walk (busmon-queue-triage §4): T opens what waits for the
// person one item at a time, in the live For you order minus what was
// visited, and moves on by itself after a terminal act.

// walk is the walk in progress (m.ui.walk, nil when none; never persisted).
type walk struct {
	visited []string          // row keys, in the order visited
	pos     int               // the item on screen: an index into visited
	outcome map[string]string // key → "answered", "sent" or "read"
	serial  int               // the walk's page on the reader stack (its bottom)
	end     bool              // that page is the end page
	moving  bool              // between two pages: the walk replaces its page
}

// walkable is a For you row the walk opens: every row but merge:unknown.
func walkable(it attentionItem) bool { return it.Key != mergeUnknownKey }

// startWalk is T: the first walkable row's page, or the end page at once.
func (m *Model) startWalk() tea.Cmd {
	m.ui.walk = &walk{outcome: map[string]string{}}
	return m.walkNext()
}

// walkNext opens the first live row not visited yet, or the end page.
func (m *Model) walkNext() tea.Cmd {
	w := m.ui.walk
	for _, it := range m.forYou {
		if !walkable(it) || slices.Contains(w.visited, it.Key) {
			continue
		}
		w.visited = append(w.visited, it.Key)
		w.pos = len(w.visited) - 1
		if cmd, ok := m.walkOpen(it); ok {
			return cmd
		}
	}
	return m.walkEnd()
}

// walkOpen replaces the walk's page with item it's page (§4.1's table);
// false when the item opens nothing (gone from the snapshot).
func (m *Model) walkOpen(it attentionItem) (tea.Cmd, bool) {
	w := m.ui.walk
	w.moving = true
	m.closeReader()
	cmd := m.openForYouItem(it)
	w.moving = false
	top := m.rd.top()
	if top == nil {
		return nil, false
	}
	if it.Rank == rankUnread {
		w.outcome[it.Key] = "read" // opening it is the read
	}
	w.serial, w.end = top.serial, false
	m.refresh()
	return cmd, true
}

// walkOpenKey reopens a visited item (p, or n back over the visited ones):
// its live row, or a line read under the walk, which stays reachable.
func (m *Model) walkOpenKey(key string) (tea.Cmd, bool) {
	if i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Key == key }); i >= 0 {
		return m.walkOpen(m.forYou[i])
	}
	if id, ok := strings.CutPrefix(key, "unread:"); ok {
		w := m.ui.walk
		w.moving = true
		m.closeReader()
		cmd := m.openFeedLine(id)
		w.moving = false
		if top := m.rd.top(); top != nil {
			w.serial, w.end = top.serial, false
			m.refresh()
			return cmd, true
		}
	}
	return nil, false
}

// walkStep is n (next) or p (back) on the walk's page: back through the
// visited items, skipping one gone (a read line stays); forward through
// them, then the live list.
func (m *Model) walkStep(next bool) tea.Cmd {
	w := m.ui.walk
	if !next {
		for j := w.pos - 1; j >= 0; j-- {
			if cmd, ok := m.walkOpenKey(w.visited[j]); ok {
				w.pos = j
				return cmd
			}
		}
		m.ui.flash, m.ui.flashErr = "no previous item", false
		return nil
	}
	if w.end {
		m.ui.flash, m.ui.flashErr = "no next item", false
		return nil
	}
	for j := w.pos + 1; j < len(w.visited); j++ {
		if cmd, ok := m.walkOpenKey(w.visited[j]); ok {
			w.pos = j
			return cmd
		}
	}
	return m.walkNext()
}

// walkDone is a terminal act's success on the walk's item (§4.3): the walk
// moves to the next page at once and the flash names it.
func (m *Model) walkDone(outcome string) tea.Cmd {
	w := m.ui.walk
	if w.pos < len(w.visited) {
		w.outcome[w.visited[w.pos]] = outcome
	}
	w.pos = len(w.visited) - 1 // the next item is the live list's
	cmd := m.walkNext()
	next := "the end"
	if !w.end && w.pos < len(w.visited) {
		if it, ok := m.forYouItem(w.visited[w.pos]); ok {
			next = it.Agent + " " + forYouKinds[it.Rank].word
			if it.Rank == rankMerge {
				next = "merge"
			}
		}
	}
	m.ui.flash, m.ui.flashErr = "sent "+GlyphDash+" next: "+next, false
	return cmd
}

// forYouItem is the live For you item of key.
func (m *Model) forYouItem(key string) (attentionItem, bool) {
	i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Key == key })
	if i < 0 {
		return attentionItem{}, false
	}
	return m.forYou[i], true
}

// walkPage reports the reader's top page as the walk's own (not a page
// opened above it).
func (m *Model) walkPage() bool {
	top := m.rd.top()
	return m.ui.walk != nil && top != nil && top.serial == m.ui.walk.serial
}

// walkItem is the walk's item on screen, when its page is on top.
func (m *Model) walkItem() (attentionItem, bool) {
	w := m.ui.walk
	if !m.walkPage() || w.end || w.pos >= len(w.visited) {
		return attentionItem{}, false
	}
	return m.forYouItem(w.visited[w.pos])
}

// walkCheck ends the walk once its page has left the stack (Esc, a click
// outside the reader), except while it replaces that page.
func (m *Model) walkCheck() {
	w := m.ui.walk
	if w == nil || w.moving {
		return
	}
	if !slices.ContainsFunc(m.rd.pages, func(p page) bool { return p.serial == w.serial }) {
		m.ui.walk = nil
	}
}

// walkTitle is the walk's place, "triage 2/7": the rows visited plus the
// walkable rows still in the list not visited.
func (m *Model) walkTitle() string {
	w := m.ui.walk
	n := len(w.visited)
	for _, it := range m.forYou {
		if walkable(it) && !slices.Contains(w.visited, it.Key) {
			n++
		}
	}
	return fmt.Sprintf("triage %d/%d", w.pos+1, n)
}

// walkEnd opens the end page (§4.4): what the walk went through, what still
// waits, and the next decision, whose row Enter opens.
func (m *Model) walkEnd() tea.Cmd {
	w := m.ui.walk
	var answered, sent, read, skipped int
	var skippedNames []string
	for _, key := range w.visited {
		switch w.outcome[key] {
		case "answered":
			answered++
		case "sent":
			sent++
		case "read":
			read++
		default:
			if it, ok := m.forYouItem(key); ok { // still waits: skipped
				skipped++
				if isDecide(it) {
					skippedNames = append(skippedNames, it.Agent+" "+forYouKinds[it.Rank].word)
				}
			}
		}
	}
	q := stripOf(m.forYou)
	title := "Triage done: everything was seen once"
	if !slices.ContainsFunc(m.forYou, walkable) {
		title = "Nothing waits for you"
	}
	var lines []Line
	lines = append(lines, Line{{Text: fmt.Sprintf("answered %d %s sent %d %s read %d %s skipped %d", answered, GlyphNone, sent, GlyphNone, read, GlyphNone, skipped), Role: RoleText}})
	var waits Line
	sep := func() {
		if len(waits) > 0 {
			waits = append(waits, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
		}
	}
	for _, c := range stripChips(q, true) {
		sep()
		waits = append(waits, c.line...)
		if c.hit == hitDecide && len(skippedNames) > 0 {
			waits = append(waits, Seg{Text: " (skipped: " + strings.Join(skippedNames, ", ") + ")", Role: RoleMuted})
		}
	}
	if len(waits) > 0 {
		lines = append(lines, append(Line{{Text: "still waits: ", Role: RoleMuted}}, waits...))
	}
	if slices.ContainsFunc(m.forYou, func(it attentionItem) bool { return it.Key == mergeUnknownKey }) {
		lines = append(lines, Line{{Text: "merge queue unreadable", Role: RoleMuted}})
	}
	next := ""
	for _, r := range forYouEmptyRows(m.snap, m.lists.reqView.count(secOwed)) {
		if task, ok := strings.CutPrefix(r.key, "empty:plan:"); ok {
			lines = append(lines, nil)
			lines = append(lines, append(Line{{Text: GlyphSelect + " ", Role: RoleAccent}}, r.line...))
			next = task
		}
	}
	w.moving = true
	m.closeReader()
	cmd := m.push(page{kind: ovTriageEnd, title: title, key: next, body: lines, fixed: true})
	w.moving = false
	w.serial, w.end = m.rd.top().serial, true
	w.pos = len(w.visited)
	m.refresh()
	return cmd
}
