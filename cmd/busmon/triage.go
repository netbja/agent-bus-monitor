package main

import (
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The triage walk (busmon-queue-triage §4; triage-dedupe §4): T opens a
// summary of what waits for the person, one line per item; Enter opens an
// item's page above it, the walk at that item: n and p move through the
// items, a terminal act moves on by itself, Esc returns to the summary, and
// the summary comes back after the last item. Esc on the summary ends it.

// walk is the walk in progress (m.ui.walk, nil when none; never persisted).
type walk struct {
	visited []string          // row keys, in the order visited
	pos     int               // the item on screen: an index into visited
	outcome map[string]string // key → "answered", "sent" or "read"
	summary int               // the summary's page on the reader stack (its bottom)
	serial  int               // the walk's page on top: an item's, or the summary's
	end     bool              // that page is the summary
	moving  bool              // between two pages: the walk replaces its page
	lost    bool              // its page dropped by a refresh (an ask closed elsewhere): move on
}

// walkable is a For you row the walk opens: every row but merge:unknown.
func walkable(it attentionItem) bool { return it.Key != mergeUnknownKey }

// startWalk is T: the summary.
func (m *Model) startWalk() tea.Cmd {
	w := &walk{outcome: map[string]string{}}
	m.ui.walk = w
	w.moving = true
	m.closeReader()
	cmd := m.push(page{kind: ovTriageSummary})
	w.moving = false
	w.summary = m.rd.top().serial
	w.serial, w.end = w.summary, true
	m.refresh()
	return cmd
}

// toSummary drops the pages above the summary, which is drawn again.
func (m *Model) toSummary() {
	w := m.ui.walk
	if i := slices.IndexFunc(m.rd.pages, func(p page) bool { return p.serial == w.summary }); i >= 0 {
		m.rd.pages = m.rd.pages[:i+1]
		m.rd.pages[i].dirty = true
	}
}

// walkNext opens the first live row not visited yet, or the summary.
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
	m.toSummary()
	cmd := m.openForYouItem(it)
	w.moving = false
	top := m.rd.top()
	if top == nil || top.serial == w.summary { // nothing opened above it
		return nil, false
	}
	if it.Rank == rankUnread || it.Rank == rankReport && it.Needs == bus.NeedsRead {
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
	if id, ok := lineKeyID(key); ok {
		w := m.ui.walk
		w.moving = true
		m.toSummary()
		cmd := m.openFeedLine(id)
		w.moving = false
		if top := m.rd.top(); top != nil && top.serial != w.summary {
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
	next := "the summary"
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

// walkCheck ends the walk once its summary has left the stack (Esc on it, a
// click outside the reader), and returns it to the summary once its item's
// page has (Esc on the item), except while it replaces that page.
func (m *Model) walkCheck() {
	w := m.ui.walk
	if w == nil || w.moving {
		return
	}
	if w.lost {
		return // afterUpdate moves it on
	}
	in := func(serial int) bool {
		return slices.ContainsFunc(m.rd.pages, func(p page) bool { return p.serial == serial })
	}
	switch {
	case !in(w.summary):
		m.ui.walk = nil
	case !in(w.serial):
		w.serial, w.end, w.pos = w.summary, true, len(w.visited)
		m.toSummary()
	}
}

// walkLost returns the walk to the summary when a refresh dropped its item's
// page (its ask answered or closed elsewhere: triage-dedupe §4), rather than
// ending it (foureyes).
func (m *Model) walkLost() tea.Cmd {
	w := m.ui.walk
	if w == nil || !w.lost {
		return nil
	}
	w.lost = false
	return m.walkEnd()
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

// walkEnd returns the walk to its summary (triage-dedupe §4: it replaces
// the end page).
func (m *Model) walkEnd() tea.Cmd {
	w := m.ui.walk
	w.moving = true
	m.toSummary()
	w.moving = false
	w.serial, w.end = w.summary, true
	w.pos = len(w.visited)
	m.refresh()
	return nil
}

// triageRow is a summary row: an item's key, or the next plan task's.
type triageRow struct {
	key, plan string
	line      int
}

// triageSummary builds the summary (triage-dedupe §4): the actions' count,
// what the walk did, one line per walkable item in For you's order, the
// next plan task; the selected row marked.
func (m *Model) triageSummary(p *page, width int) {
	var answered, sent, read int
	if w := m.ui.walk; w != nil {
		for _, o := range w.outcome {
			switch o {
			case "answered":
				answered++
			case "sent":
				sent++
			case "read":
				read++
			}
		}
	}
	var items []attentionItem
	for _, it := range m.forYou {
		if walkable(it) {
			items = append(items, it)
		}
	}
	p.title = fmt.Sprintf("Triage: %d actions", len(items))
	if len(items) == 1 {
		p.title = "Triage: 1 action"
	}
	if len(m.forYou) == 0 { // only an empty list: merge:unknown alone still stands (reviewer-codex)
		p.title = "Nothing waits for you"
	}
	p.body = []Line{{{Text: fmt.Sprintf("answered %d %s sent %d %s read %d", answered, GlyphNone, sent, GlyphNone, read), Role: RoleMuted}}, nil}
	p.titems = nil
	mark := func() Seg {
		if len(p.titems) == p.sel {
			return Seg{Text: GlyphSelect + " ", Role: RoleAccent}
		}
		return Seg{Text: "  "}
	}
	for _, it := range items {
		l := append(Line{mark()}, forYouRow(it, max(0, width-2), m.now)...)
		p.titems = append(p.titems, triageRow{key: it.Key, line: len(p.body)})
		p.body = append(p.body, l)
	}
	if slices.ContainsFunc(m.forYou, func(it attentionItem) bool { return it.Key == mergeUnknownKey }) {
		p.body = append(p.body, Line{{Text: "merge queue unreadable", Role: RoleMuted}})
	}
	for _, r := range forYouEmptyRows(m.snap, m.lists.reqView.count(secOwed), m.work.decideCount()) {
		if task, ok := strings.CutPrefix(r.key, planNextPrefix); ok {
			p.body = append(p.body, nil)
			l := append(Line{mark(), {Text: "next plan task: ", Role: RoleMuted}}, r.line...)
			p.titems = append(p.titems, triageRow{plan: task, line: len(p.body)})
			p.body = append(p.body, FitLine(l, width))
		}
	}
	if p.sel >= len(p.titems) && len(p.titems) > 0 { // a row gone: the mark on the last one
		p.sel = len(p.titems) - 1
		m.triageSummary(p, width)
	}
}

// summaryOpen is Enter on the summary: the selected item's page, the walk at
// that item; the plan line its task.
func (m *Model) summaryOpen(p *page) tea.Cmd {
	if p.sel >= len(p.titems) {
		return nil
	}
	r := p.titems[p.sel]
	if r.plan != "" {
		return m.push(page{kind: ovPlanTask, key: r.plan})
	}
	it, ok := m.forYouItem(r.key)
	if !ok {
		return nil
	}
	w := m.ui.walk
	j := slices.Index(w.visited, it.Key)
	if j < 0 {
		w.visited = append(w.visited, it.Key)
		j = len(w.visited) - 1
	}
	w.pos = j
	cmd, _ := m.walkOpen(it)
	return cmd
}
