package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// busmon-queue-triage §3: the queue strip replaces the shell's two badges.

func TestStripCountsPartitionForYou(t *testing.T) {
	items := []attentionItem{
		{Rank: rankAsk, Key: "ask:1", Unread: true},
		{Rank: rankHealth, Key: "health:team-stall:1"},
		{Rank: rankWait, Key: "wait:coder:1"},
		{Rank: rankGate, Key: "gate:coder:1-0"},
		{Rank: rankMerge, Key: "merge:abc"},
		{Rank: rankMerge, Key: mergeUnknownKey},
		{Rank: rankUnread, Key: "unread:9"},
		{Rank: rankUnread, Key: "unread:10"},
	}
	q := stripOf(items)
	if q != (queueStrip{decide: 4, merge: 1, unread: 3}) {
		t.Fatalf("strip = %+v, want decide 4, merge 1 (never merge:unknown), unread 3", q)
	}
	if q.decide+q.merge != len(decisions(items)) {
		t.Fatalf("decide+merge = %d, decisions = %d", q.decide+q.merge, len(decisions(items)))
	}
	if q.unread != unreadItems(items) {
		t.Fatalf("unread %d != unreadItems %d", q.unread, unreadItems(items))
	}
	only := stripOf([]attentionItem{{Rank: rankMerge, Key: mergeUnknownKey}})
	if only != (queueStrip{}) {
		t.Fatalf("merge:unknown alone counts nothing: %+v", only)
	}
}

func stripHeader(q queueStrip) headerData {
	return headerData{project: "p", health: health{ok: true}, shell: true, strip: q, now: hdrNow}
}

// segOf is the segment of l whose text contains s.
func segOf(l Line, s string) (Seg, bool) {
	for _, g := range l {
		if strings.Contains(g.Text, s) {
			return g, true
		}
	}
	return Seg{}, false
}

func TestStripChipsOrderWordsAndRoles(t *testing.T) {
	q := queueStrip{decide: 2, merge: 1, unread: 3}
	wide := headerRows(stripHeader(q), 160, TierWide, 2)[0]
	text := wide.text()
	d, m, u := strings.Index(text, GlyphHuman+" 2 decide"), strings.Index(text, GlyphGate+" 1 merge"), strings.Index(text, GlyphDotFull+" 3 unread")
	if d < 0 || m < 0 || u < 0 || !(d < m && m < u) {
		t.Fatalf("wide chips missing or out of order (decide, merge, unread): %q", text)
	}
	for s, want := range map[string]Role{"2 decide": RoleAttention, "1 merge": RoleDispatch, "3 unread": RoleAttention} {
		if g, ok := segOf(wide, s); !ok || g.Role != want {
			t.Fatalf("chip %q role = %v, want %v", s, g.Role, want)
		}
	}
	if strings.Contains(text, "for you") {
		t.Fatalf("the old for-you badge is still drawn: %q", text)
	}
	for _, tier := range []Tier{TierMedium, TierNarrow} {
		w := map[Tier]int{TierMedium: 100, TierNarrow: 60}[tier]
		got := headerRows(stripHeader(q), w, tier, 2)[0].text()
		if !strings.Contains(got, GlyphHuman+"2  "+GlyphGate+"1  "+GlyphDotFull+"3") {
			t.Fatalf("tier %d: compact chips, gap 2: %q", tier, got)
		}
	}
	none := headerRows(stripHeader(queueStrip{decide: 2, unread: 3}), 160, TierWide, 2)[0].text()
	if strings.Contains(none, GlyphGate) || !strings.Contains(none, GlyphHuman+" 2 decide   "+GlyphDotFull+" 3 unread") {
		t.Fatalf("a chip at 0 is not drawn, the others keep their order: %q", none)
	}
}

// chipX is the column of the strip chip hit at header row 0.
func chipX(t *testing.T, m Model, hit headerHit) (int, int) {
	t.Helper()
	_, spans := headerLayout(m.headerData(), m.width, m.layout.Tier, m.layout.Header.H)
	for _, s := range spans {
		if s.hit == hit {
			return s.x + s.w/2, m.layout.Header.Y + s.row
		}
	}
	t.Fatalf("no header span for hit %d: %+v", hit, spans)
	return 0, 0
}

func stripFixture(t *testing.T) Model {
	t.Helper()
	m := rowsFixture(t, false, false)
	s := m.snap
	s.guardian = tourGuardian()
	return step(t, m, pollMsg{snap: s, at: shellNow})
}

func TestHeaderChipClicks(t *testing.T) {
	rank := func(m Model, key string) int {
		for _, it := range m.forYou {
			if it.Key == key {
				return it.Rank
			}
		}
		return -1
	}
	firstOf := func(m Model, ok func(attentionItem) bool) string {
		for _, it := range m.forYou {
			if ok(it) {
				return it.Key
			}
		}
		return ""
	}
	t.Run("decide", func(t *testing.T) {
		m := stripFixture(t)
		m.focusPanel(PanelAgents)
		m.refresh()
		x, y := chipX(t, m, hitDecide)
		m = clickAt(t, m, x, y)
		want := firstOf(m, func(it attentionItem) bool { return it.Rank <= rankGate })
		if m.ui.focus != PanelForYou || m.lists.forYou.Selected() != want || rank(m, want) > rankGate {
			t.Fatalf("decide: focus %v sel %q, want For you and %q", m.ui.focus, m.lists.forYou.Selected(), want)
		}
	})
	t.Run("merge", func(t *testing.T) {
		m := stripFixture(t)
		x, y := chipX(t, m, hitMerge)
		m = clickAt(t, m, x, y)
		sel := m.lists.forYou.Selected()
		if m.ui.focus != PanelForYou || !strings.HasPrefix(sel, "merge:") || sel == mergeUnknownKey {
			t.Fatalf("merge: focus %v sel %q", m.ui.focus, sel)
		}
	})
	t.Run("unread", func(t *testing.T) {
		m := stripFixture(t)
		x, y := chipX(t, m, hitUnread)
		m = clickAt(t, m, x, y)
		sel := m.lists.activity.Selected()
		found := false
		for _, fl := range m.feed.feed {
			if fl.id == sel && fl.mark == markUnread {
				found = true
			}
		}
		if m.ui.focus != PanelActivity || !found {
			t.Fatalf("unread: as u, focus %v sel %q", m.ui.focus, sel)
		}
	})
	t.Run("health", func(t *testing.T) {
		m := stripFixture(t)
		x, y := chipX(t, m, hitHealth)
		m = clickAt(t, m, x, y)
		if !m.rd.open() || m.rd.top().kind != ovHealth {
			t.Fatalf("health pill: F5's page, got open=%v", m.rd.open())
		}
	})
	t.Run("a chip closes the reader first", func(t *testing.T) {
		m := stripFixture(t)
		m = keys(t, m, tea.Key{Code: tea.KeyF5})
		if !m.rd.open() {
			t.Fatal("F5 did not open")
		}
		x, y := chipX(t, m, hitMerge)
		m = clickAt(t, m, x, y)
		if m.rd.open() || !strings.HasPrefix(m.lists.forYou.Selected(), "merge:") {
			t.Fatalf("reader open=%v sel %q", m.rd.open(), m.lists.forYou.Selected())
		}
	})
	t.Run("not a chip", func(t *testing.T) {
		m := stripFixture(t)
		focus := m.ui.focus
		m = clickAt(t, m, 1, m.layout.Header.Y) // the project name
		if m.rd.open() || m.ui.focus != focus {
			t.Fatalf("a click on the project did something: open=%v focus %v", m.rd.open(), m.ui.focus)
		}
		x, y := chipX(t, m, hitHealth)
		if m.layout.Header.H < 2 {
			t.Fatalf("header %d rows", m.layout.Header.H)
		}
		m = clickAt(t, m, x, y+1) // row 1, under the health pill
		if m.rd.open() || m.ui.focus != focus {
			t.Fatalf("a click under the pill did something: open=%v focus %v", m.rd.open(), m.ui.focus)
		}
	})
}

// Each span covers exactly its chip's drawn text, at every tier.
func TestHeaderSpansCoverTheirChips(t *testing.T) {
	for _, w := range []int{140, 100, 60} {
		m := rowsFixture(t, false, false)
		m = step(t, m, tea.WindowSizeMsg{Width: w, Height: 40})
		s := m.snap
		s.guardian = tourGuardian()
		m = step(t, m, pollMsg{snap: s, at: shellNow})
		d := m.headerData()
		chips := map[headerHit]string{}
		for _, c := range stripChips(d.strip, w >= wideMinWidth) {
			chips[c.hit] = c.line.text()
		}
		chips[hitHealth] = guardianSeg(d.guardian, w).text()
		_, spans := headerLayout(d, m.width, m.layout.Tier, m.layout.Header.H)
		if len(spans) != len(chips) {
			t.Fatalf("width %d: %d spans for %d chips: %+v", w, len(spans), len(chips), spans)
		}
		for _, sp := range spans {
			row := []rune(m.grid().rowText(m.layout.Header.Y + sp.row))
			if got := string(row[sp.x : sp.x+sp.w]); got != chips[sp.hit] {
				t.Fatalf("width %d: span %+v covers %q, want %q", w, sp, got, chips[sp.hit])
			}
		}
	}
}

// At the medium tier an unfocused For you shows four rows: the merge chip
// still reaches a merge row past them.
func TestMergeChipAtMediumTier(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.focusPanel(PanelAgents)
	m.refresh()
	if m.layout.Tier != TierMedium {
		t.Fatalf("tier %v", m.layout.Tier)
	}
	x, y := chipX(t, m, hitMerge)
	m = clickAt(t, m, x, y)
	if sel := m.lists.forYou.Selected(); !strings.HasPrefix(sel, "merge:") || sel == mergeUnknownKey {
		t.Fatalf("merge chip at medium: sel %q", sel)
	}
}

func TestStripMergeChipSkipsUnknown(t *testing.T) {
	m := rowsFixture(t, true, false)
	if q := stripOf(m.forYou); q.merge != 0 {
		t.Fatalf("merge:unknown counted: %+v", q)
	}
	_, spans := headerLayout(m.headerData(), m.width, m.layout.Tier, m.layout.Header.H)
	for _, s := range spans {
		if s.hit == hitMerge {
			t.Fatal("a merge chip is drawn with only merge:unknown")
		}
	}
}

func TestTourHeaderPageShowsTheStrip(t *testing.T) {
	if tourVersion < 3 {
		t.Fatalf("tourVersion %d: the header page changed, bump it", tourVersion)
	}
	for _, p := range tourBook {
		if p.title != "The header" {
			continue
		}
		if p.since != tourVersion {
			t.Fatalf("the header page's since = %d, want %d so it is seen again", p.since, tourVersion)
		}
		var text string
		for _, b := range p.blocks {
			for _, r := range b.rows {
				text += r.label("charlie").text() + " "
				for _, d := range r.desc {
					text += d.text + " "
				}
				text += "\n"
			}
		}
		for _, want := range []string{GlyphHuman + "2  " + GlyphGate + "1  " + GlyphDotFull + "3", GlyphHuman + " to decide", GlyphGate + " to merge", GlyphDotFull + " unread"} {
			if !strings.Contains(text, want) {
				t.Fatalf("the header page lacks %q:\n%s", want, text)
			}
		}
		return
	}
	t.Fatal("no header page")
}
