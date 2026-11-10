package main

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
)

// The queue strip (busmon-queue-triage §3): the shell's header chips, a
// partition of For you, so the strip and the panel never disagree.

// queueStrip is what waits, by chip: decide (asks, stall rows, waits,
// gates; plan tasks are no rows, home-inbox-drawer §3.3), merge (merge rows that are a PR, never merge:unknown)
// and unread (unreadItems, as the old badge).
type queueStrip struct{ decide, merge, unread int }

func stripOf(items []attentionItem) queueStrip {
	var q queueStrip
	for _, it := range items {
		switch {
		case isDecide(it):
			q.decide++
		case isMergePR(it):
			q.merge++
		}
	}
	q.unread = unreadItems(items)
	return q
}

// isDecide and isMergePR are a decide or merge chip's rows: what it counts
// and what its click selects.
func isDecide(it attentionItem) bool  { return it.Rank < rankMerge }
func isMergePR(it attentionItem) bool { return it.Rank == rankMerge && it.Key != mergeUnknownKey }

// headerHit is what a click on a header item does (§3.2).
type headerHit int

const (
	hitNone headerHit = iota
	hitDecide
	hitMerge
	hitUnread
	hitHealth // the health pill: F5
)

// headerSpan is an admitted header item's place, for the click hit-test.
type headerSpan struct {
	row, x, w int
	hit       headerHit
}

type stripChip struct {
	line Line
	hit  headerHit
}

// stripChips are the strip's chips in their fixed order, a chip at 0 left
// out; below the wide tier a chip loses its word ("♙2").
func stripChips(q queueStrip, wide bool) []stripChip {
	var out []stripChip
	for _, c := range []struct {
		n     int
		glyph string
		word  string
		role  Role
		hit   headerHit
	}{
		{q.decide, GlyphHuman, "decide", RoleAttention, hitDecide},
		{q.merge, GlyphGate, "merge", RoleDispatch, hitMerge},
		{q.unread, GlyphDotFull, "unread", RoleAttention, hitUnread},
	} {
		if c.n <= 0 {
			continue
		}
		text := fmt.Sprintf("%s%d", c.glyph, c.n)
		if wide {
			text = fmt.Sprintf("%s %d %s", c.glyph, c.n, c.word)
		}
		out = append(out, stripChip{Line{{Text: text, Role: c.role}}, c.hit})
	}
	return out
}

// stripLine is the strip drawn alone (the tour's header page).
func stripLine(q queueStrip, wide bool) Line {
	var l Line
	for i, c := range stripChips(q, wide) {
		if i > 0 {
			l = append(l, Seg{Text: "  "})
		}
		l = append(l, c.line...)
	}
	return l
}

// headerClick runs the action of the header item at (x, row), if any (§3.2).
func (m *Model) headerClick(x, row int) tea.Cmd {
	l := m.layout
	_, spans := headerLayout(m.headerData(), m.width, l.Tier, l.Header.H)
	for _, s := range spans {
		if s.row != row || x < s.x || x >= s.x+s.w {
			continue
		}
		switch s.hit {
		case hitDecide: // For you's row, else Work's: the chip counts both (foureyes)
			if !m.forYouHas(isDecide) && m.selectWork(func(it workItem) bool { return it.Verb != verbMerge }) {
				return nil
			}
			m.selectForYou(isDecide)
		case hitMerge:
			m.selectForYou(isMergePR)
		case hitUnread:
			m.goNextUnread()
		case hitHealth:
			return m.push(page{kind: ovHealth})
		}
		return nil
	}
	return nil
}

// forYouHas says For you lists a row ok accepts.
func (m *Model) forYouHas(ok func(attentionItem) bool) bool {
	return slices.ContainsFunc(m.forYou, ok)
}

// selectForYou focuses For you and selects its first row that ok accepts.
func (m *Model) selectForYou(ok func(attentionItem) bool) {
	m.focusPanel(PanelForYou)
	m.refresh() // the focused list holds every row (medium tier)
	for _, it := range m.forYou {
		if ok(it) {
			m.lists.forYou.Select(it.Key)
			break
		}
	}
	m.refresh()
}
