package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// dropCells drops a line's first n cells, a wide cluster cut in half
// becoming a space, and keeps the styles.
func TestDropCells(t *testing.T) {
	l := Line{{Text: "ab", Role: RoleMuted}, {Text: "cdé", Role: RoleText}}
	for n, want := range map[int]string{0: "abcdé", 1: "bcdé", 3: "dé", 5: "", 9: ""} {
		if got := dropCells(l, n).text(); got != want {
			t.Errorf("drop %d: %q; want %q", n, got, want)
		}
	}
	if got := dropCells(l, 3); len(got) != 1 || got[0].Role != RoleText {
		t.Errorf("styles lost: %+v", got)
	}
	if got := dropCells(Line{{Text: "日本x"}}, 1).text(); got != " 本x" {
		t.Errorf("half a wide cluster: %q", got)
	}
}

const longTitle = "herdr exit 2: agentstate out of dispatch, client profiles as data, blocked and background facts, hooks"

// The Kanban's selected card scrolls each cut line by the offset, never past
// its own end; the other cards and the uncut lines do not move.
func TestKanbanMarqueeDraws(t *testing.T) {
	v := kanbanView{cols: []kanbanCol{{title: "Next  2", cards: []kanbanCard{
		{key: "a", lines: []Line{{{Text: "○ coder"}}, {{Text: "  " + longTitle}}}},
		{key: "b", lines: []Line{{{Text: "○ coder"}}, {{Text: "  " + longTitle}}}},
	}}}}
	l := listState{kanban: &v, rect: Rect{0, 0, 32, 12}, sel: "a"}
	l.items = v.items()
	rows := func() []string {
		g := newGrid(32, 12)
		l.draw(g)
		return strings.Split(g.plain(), "\n")
	}
	before := rows()
	if !strings.Contains(before[3], "  herdr exit 2: agentstate ou…") {
		t.Fatalf("cut title %q", before[3])
	}
	if got := l.marqueeOver("a"); got != len("  "+longTitle)-30 {
		t.Fatalf("over %d", got)
	}
	l.mq = marquee{key: "a", off: 5}
	after := rows()
	if !strings.Contains(after[3], "dr exit 2: agentstate out of …") {
		t.Errorf("scrolled title %q", after[3])
	}
	if after[2] != before[2] || after[5] != before[5] {
		t.Errorf("an uncut line or another card moved:\n%q\n%q", after[2], after[5])
	}
	l.mq = marquee{key: "b", off: 5} // an offset of another card moves nothing
	if got := rows(); got[3] != before[3] || got[5] != before[5] {
		t.Errorf("another card's offset scrolled:\n%q\n%q", got[3], got[5])
	}
	l.mq = marquee{key: "a", off: 999} // clamped: the end shows, no ellipsis
	if end := rows()[3]; !strings.Contains(end, "background facts, hooks") || strings.Contains(end, GlyphEllipsis) {
		t.Errorf("end %q", end)
	}
	if l.marqueeOver("b") <= 0 || l.marqueeOver("nope") != 0 {
		t.Error("over of another card")
	}
}

// The list's rows record their cut title (YOU, TEAM, NEXT, IDEAS); the
// selected row scrolls that field alone, in place.
func TestWorkRowsMarquee(t *testing.T) {
	w := workModel{Next: []workItem{
		{Key: "plan:long", Text: longTitle, Who: "coder", Plan: "herdr-2"},
		{Key: "plan:short", Text: "short", Who: "coder", Plan: "x"},
	}}
	items := workRows(w, workDraw{now: shellNow, width: 100})
	var long, short *ListItem
	for i := range items {
		switch items[i].Key {
		case "plan:long":
			long = &items[i]
		case "plan:short":
			short = &items[i]
		}
	}
	if long == nil || long.Marquee == nil || short == nil || short.Marquee != nil {
		t.Fatalf("marquee fields: long %+v short %+v", long, short)
	}
	l := listState{items: items, rect: Rect{0, 0, 104, len(items) + 2}, sel: "plan:long"}
	row := func() string {
		g := newGrid(104, len(items)+2)
		l.draw(g)
		for _, r := range strings.Split(g.plain(), "\n") {
			if strings.Contains(r, "coder") && strings.Contains(r, "herdr-2") {
				return r
			}
		}
		return ""
	}
	before := row()
	l.mq = marquee{key: "plan:long", off: 6}
	after := row()
	if !strings.Contains(before, "herdr exit 2: agentstate") || !strings.Contains(after, "xit 2: agentstate out of") {
		t.Fatalf("before %q after %q", before, after)
	}
	l.mq.off = 999 // the end: col's space before the owner stays
	if end := row(); !strings.Contains(end, "hooks coder") {
		t.Errorf("end of the title %q", end)
	}
	l.mq.off = 6
	cut := func(s string) string { i := strings.Index(s, "coder"); return s[i:] }
	if cut(before) != cut(after) {
		t.Errorf("the columns after the title moved: %q vs %q", cut(before), cut(after))
	}
}

func marqueeShell(t *testing.T, w int) Model {
	t.Helper()
	st := workShared()
	for i := range st.plan.rows {
		if st.plan.rows[i].Task.ID == "t8" {
			st.plan.rows[i].Task.Title = longTitle
		}
	}
	return keys(t, workShellOf(t, st, w, 40), '4') // Work, on NEXT's first row: plan:t8
}

// One pass: the focused cut row waits, scrolls a cell a frame to its end,
// holds, then shows the cut text again and stops; a stale frame does
// nothing; leaving the row or the panel resets it.
func TestMarqueeOnePass(t *testing.T) {
	pinLocal(t)
	for _, w := range []int{130, 100} { // the Kanban, the list
		m := marqueeShell(t, w)
		mq := m.lists.work.mq
		if mq.key != "plan:t8" || mq.off != 0 || mq.serial == 0 {
			t.Fatalf("%d: not armed: %+v", w, mq)
		}
		over := m.lists.work.marqueeOver("plan:t8")
		if over <= 0 {
			t.Fatalf("%d: title not cut", w)
		}
		if cmd := m.update(marqueeMsg{serial: mq.serial - 1}); cmd != nil || m.lists.work.mq.off != 0 {
			t.Fatalf("%d: a stale frame moved it", w)
		}
		frames := 0
		for m.lists.work.mq.off < over {
			if cmd := m.update(marqueeMsg{serial: mq.serial}); cmd == nil {
				t.Fatalf("%d: stopped at %d of %d", w, m.lists.work.mq.off, over)
			}
			frames++
		}
		if frames != over {
			t.Fatalf("%d: %d frames for %d cells", w, frames, over)
		}
		if cmd := m.update(marqueeMsg{serial: mq.serial}); cmd != nil || m.lists.work.mq.off != 0 || !m.lists.work.mq.done {
			t.Fatalf("%d: after the hold: %+v", w, m.lists.work.mq)
		}
		if cmd := m.update(marqueeMsg{serial: mq.serial}); cmd != nil {
			t.Fatalf("%d: a second pass", w)
		}
		m = step(t, m, marqueeMsg{serial: mq.serial}) // through afterUpdate: not re-armed
		if m.lists.work.mq.serial != mq.serial {
			t.Fatalf("%d: re-armed on the same row", w)
		}
		if w == 130 { // the focus on another panel, Work still shown: the pass stops
			f := keys(t, m, tea.Key{Code: tea.KeyTab})
			if f.ui.focus == PanelWork || f.layout.Active != PanelWork || f.lists.work.mq.key != "" {
				t.Fatalf("focus away: focus %v active %v %+v", f.ui.focus, f.layout.Active, f.lists.work.mq)
			}
		}
		m = keys(t, m, '1') // another panel: reset, nothing scheduled
		if m.lists.work.mq.key != "" || m.lists.work.mq.off != 0 {
			t.Fatalf("%d: kept off the panel: %+v", w, m.lists.work.mq)
		}
	}
}

// A row that fits arms nothing: no frame is ever scheduled.
func TestMarqueeIdleOnAFittingRow(t *testing.T) {
	pinLocal(t)
	m := keys(t, workShell(t, 130, 40), '4')
	if m.lists.work.Selected() != "plan:t8" || m.lists.work.mq.key != "" {
		t.Fatalf("armed on a fitting row: %q %+v", m.lists.work.Selected(), m.lists.work.mq)
	}
}

// A field wider than the frame leaves it: the scrolled text stops at the
// frame's edge, never on the border.
func TestListMarqueeStaysInTheFrame(t *testing.T) {
	text := Line{{Text: longTitle, Role: RoleText}}
	it := ListItem{Key: "k", Line: FitLine(Line{{Text: "> "}, {Text: longTitle}}, 60), Selectable: true, Marquee: fieldMarquee(2, 58, text)}
	l := listState{items: []ListItem{it}, rect: Rect{0, 0, 24, 3}, sel: "k", mq: marquee{key: "k", off: 4}}
	g := newGrid(40, 3)
	l.draw(g)
	row := []rune(strings.Split(g.plain(), "\n")[1])
	if len(row) < 24 || string(row[23]) != "│" || strings.TrimSpace(string(row[24:])) != "" {
		t.Fatalf("drawn past the frame: %q", string(row))
	}
}
