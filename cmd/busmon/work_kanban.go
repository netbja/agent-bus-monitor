package main

import (
	"fmt"
	"path"
	"strings"
)

// The Work tab's Kanban (requests-plan-redesign, layout B; the person's
// answer, Q1): the same model as the list (work_model.go), drawn as four
// columns, For you, In progress, Next and Ideas, when busmon's pane is
// kanbanMinWidth columns wide or more (display.work_layout auto), or always
// (kanban). Both read one workModel: only the drawing differs.
//
// A card is two lines. The first holds its mark, its verb or owner and its
// age; the second its title, indented two cells and cut with … at the
// column's width, never wrapped. At 130 columns a column is 31 cells: the
// title keeps 29. One card has a third line: an agent holding both a task and
// a review gate, whose gate (for whom, its age, the other gates, then the
// subject) cannot share the task's two lines at 31 cells (reviewer-codex).
//
// The folded lines close the Ideas column: between agents and done, two cards
// Enter opens as on the list. The keys depend on the focus level
// (work_focus.go): the tab, the column selector, a column's cards.

const (
	kanbanMinWidth = 130 // busmon's pane width from which auto draws the Kanban
	kanbanMinCol   = 24  // a column narrower than this: the columns stack (2×2, then 1)
)

// workKanbanOn says whether the Work tab draws the Kanban: the setting, and
// for auto busmon's own width (the herdr pane's, not the terminal's).
func workKanbanOn(layout string, width int) bool {
	switch layout {
	case "kanban":
		return true
	case "list":
		return false
	}
	return width >= kanbanMinWidth
}

// kanbanCard is one card: its row key (the list's) and its two lines.
type kanbanCard struct {
	key   string
	lines []Line // two; three for an agent holding a task and a gate
}

// kanbanCol is one column: its heading and its cards.
type kanbanCol struct {
	title string
	cards []kanbanCard
}

// kanbanView is the Kanban the Work list draws in place of its rows.
type kanbanView struct {
	cols []kanbanCol
	// Where the focus is (work_focus.go): at the column selector, column col's
	// heading is drawn selected; only at a column's cards is the selected card
	// drawn so (at the tab, the selection is kept but not shown).
	level workLevel
	col   int
}

// workKanban builds the four columns from the model. Every card reads the
// same way (reviewer-codex): its first line is the card's facts, the
// priority mark first (never cut), then its mark, verb or owner, and its age
// or place; its second line is its title, cut with … at the column.
func workKanban(w workModel, d workDraw) kanbanView {
	title := func(text string, role Role) Line { return Line{{Text: "  " + Preview(text), Role: role}} }
	head := func(prio Seg, mark string, role Role, parts ...string) Line {
		l := Line{prio, {Text: mark + " ", Role: role}}
		for i, p := range parts {
			if p == "" {
				continue
			}
			if i > 0 {
				l = append(l, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
			}
			l = append(l, Seg{Text: Preview(p), Role: role})
		}
		return l
	}
	reqMark := func(r *request) Seg {
		if r != nil {
			return priorityMark(r.Priority)
		}
		return noMark
	}

	you := kanbanCol{title: fmt.Sprintf("For you  %d", w.youCount())}
	for _, it := range w.You {
		g, role := workGlyph(it)
		a := ""
		if !it.At.IsZero() {
			a = humanAge(d.now.Sub(it.At))
		}
		you.cards = append(you.cards, kanbanCard{key: it.Key, lines: []Line{
			head(reqMark(it.Req), g, role, it.Verb, workWho(it.Who, false), a),
			title(strings.TrimPrefix(it.Text, "merge "), RoleText)}})
	}

	team := kanbanCol{title: fmt.Sprintf("In progress  %d", len(w.Team))}
	for _, r := range w.Team {
		glyph, role := GlyphWorking, RoleOK
		if r.Word != "working" {
			glyph, role = GlyphIdle, RoleMuted
		}
		what, where := r.Message, ""
		if r.Task != nil {
			what = r.Task.Task
			if r.Plan != "" {
				where = "#" + r.Plan
			}
		}
		var gateLine Line
		if len(r.Reviewing) > 0 { // the gate it holds: for whom, its age and count first, so a cut eats only the subject (reviewer-codex)
			g := r.Reviewing[0]
			facts := "for " + g.Who
			if a := gateAge(g.Since, d.now); a != "" {
				facts += " " + a
			}
			if n := len(r.Reviewing) - 1; n > 0 {
				facts += fmt.Sprintf(" (+%d)", n)
			}
			if r.Task == nil {
				what, where = "reviewing "+g.Subject, facts
			} else { // a third line: the task stays the title
				gateLine = Line{{Text: "  " + GlyphGate + " " + Preview(facts) + " " + GlyphNone + " " + Preview(g.Subject), Role: RoleDispatch}}
			}
		} else if len(r.Reviewers) > 0 {
			where = strings.TrimSpace(where + " " + strings.TrimSpace(strings.TrimPrefix(teamReviewers(r, d.now), "  review:")))
		}
		card := kanbanCard{key: workAgentPrefix + r.Agent, lines: []Line{head(reqMark(r.Task), glyph, role, r.Agent, where), title(what, RoleText)}}
		if gateLine != nil {
			card.lines = append(card.lines, gateLine)
		}
		team.cards = append(team.cards, card)
	}
	if len(w.Idle) > 0 { // the free agents: they can take a request (correction 6)
		team.cards = append(team.cards, kanbanCard{lines: []Line{
			head(noMark, GlyphIdle, RoleMuted, "idle, can take a request"), title(strings.Join(w.Idle, ", "), RoleMuted)}})
	}

	next := kanbanCol{title: fmt.Sprintf("Next  %d", len(w.Next))}
	for _, it := range w.Next {
		where := "not in the plan"
		switch {
		case it.Plan != "" && len(it.After) > 0:
			where = "#" + it.Plan + " waits on " + strings.Join(it.After, ", ")
		case it.Plan != "":
			where = "#" + it.Plan
		}
		next.cards = append(next.cards, kanbanCard{key: it.Key, lines: []Line{
			head(reqMark(it.Req), GlyphIdle, RoleMuted, workWho(it.Who, true), where), title(it.Text, RoleText)}})
	}

	ideas := kanbanCol{title: fmt.Sprintf("Ideas  %d", len(w.Ideas))}
	if w.IdeasErr {
		ideas.cards = append(ideas.cards, kanbanCard{lines: []Line{
			head(noMark, GlyphIdea, RoleAttention, "unread"), title("the open questions could not be read", RoleAttention)}})
	}
	for _, it := range w.Ideas {
		ideas.cards = append(ideas.cards, kanbanCard{key: workIdeaPrefix + it.ID, lines: []Line{
			head(noMark, GlyphIdea, RoleAccent, "idea", path.Base(it.Where)), title(it.Title, RoleText)}})
	}
	ideas.cards = append(ideas.cards, kanbanCard{key: workFoldBetween, lines: []Line{
		head(noMark, GlyphDash, RoleMuted, "between agents", fmt.Sprint(w.Between)), title("nobody waits on you (enter)", RoleMuted)}})
	if w.Planned {
		ideas.cards = append(ideas.cards, kanbanCard{key: workFoldDone, lines: []Line{
			head(noMark, GlyphDash, RoleMuted, "done", fmt.Sprint(w.Done)), title("enter or d shows them", RoleMuted)}})
	}
	return kanbanView{cols: []kanbanCol{you, team, next, ideas}}
}

// items are the list's items in reading order, column by column: the list
// keeps the selection, Enter, y, + - and the footer's word as on the rows.
func (k *kanbanView) items() []ListItem {
	var out []ListItem
	for _, c := range k.cols {
		for _, card := range c.cards {
			out = append(out, ListItem{Key: card.key, Line: card.lines[0], Selectable: card.key != ""})
		}
	}
	return out
}

// kanbanRect is one column's rectangle inside the frame's inner area.
type kanbanRect struct{ x, y, w, h int }

// kanbanGrid places n columns in the inner area (x, y, w, h): side by side
// when each keeps kanbanMinCol cells, else two per band, else one; one
// column apart side by side, the bands sharing the height.
func kanbanGrid(n, x, y, w, h int) []kanbanRect {
	per := n
	for per > 1 && (w-(per-1))/per < kanbanMinCol {
		if per > 2 {
			per = 2
		} else {
			per = 1
		}
	}
	bands := (n + per - 1) / per
	bandH := h / max(bands, 1)
	cw := (w - (per - 1)) / per
	out := make([]kanbanRect, n)
	for i := range out {
		b, c := i/per, i%per
		out[i] = kanbanRect{x: x + c*(cw+1), y: y + b*bandH, w: cw, h: bandH}
	}
	return out
}

// kanbanPlace is where a card is: its column, its rank in it, and the first
// rank each column shows (its scroll, so the selected card is in view).
func (k *kanbanView) place(sel string, rects []kanbanRect) (col, rank int, first []int) {
	col, rank = -1, -1
	first = make([]int, len(k.cols))
	for c, kc := range k.cols {
		for r, card := range kc.cards {
			if card.key != "" && card.key == sel {
				col, rank = c, r
			}
		}
	}
	if col >= 0 {
		for first[col] < rank && rank >= first[col]+kanbanFits(rects[col].h, k.cols[col].cards[first[col]:]) {
			first[col]++
		}
	}
	return col, rank, first
}

// kanbanFits is how many of cards, from the first, a column of height h
// shows: its heading, each card's lines (two, three with a gate), and a
// "+N more" line when they do not all fit.
func kanbanFits(h int, cards []kanbanCard) int {
	count := func(room int) int {
		n := 0
		for _, c := range cards {
			if room < len(c.lines) {
				break
			}
			room -= len(c.lines)
			n++
		}
		return n
	}
	if n := count(h - 1); n == len(cards) {
		return n
	}
	return count(h - 2)
}

// heading is column c's heading: the column holding the cursor (at the
// selector or in its cards) a pill in the Work tab's colour, so the person
// always sees which column has the focus; at the selector its ▸ too. At the
// tab level no column has it.
func (k *kanbanView) heading(c int) Line {
	title := k.cols[c].title
	if k.level == workAtTabs || c != k.col {
		return Line{{Text: title, Role: RoleText, Bold: true}}
	}
	if k.level == workAtCols {
		title = GlyphSelect + " " + title
	}
	// A block as the tab label is, the rest of the row plain.
	return Line{{Text: " " + title + " ", Role: panelHue[PanelWork], Bold: true, Pill: true}, {Text: " ", Role: RoleText}}
}

// draw draws the columns inside the list's inner area; sel is the list's
// selection, off its marquee offset (each cut line of the selected card
// moved left by it, never past its end).
func (k *kanbanView) draw(screen cellSink, x, y, w, h int, sel string, off int) {
	rects := kanbanGrid(len(k.cols), x, y, w, h)
	_, _, first := k.place(sel, rects)
	for c, kc := range k.cols {
		r := rects[c]
		if r.w <= 0 || r.h <= 0 {
			continue
		}
		drawLine(screen, r.x, r.y, r.w, k.heading(c), false)
		if c > 0 && r.x > x { // the column apart: a rule in the rules' colour, inset one row at each end (the person)
			for i := 0; i < r.h; i++ {
				var rule Line
				if i > 0 && i < r.h-1 {
					rule = Line{{Text: glyphVertical, Role: RoleLine}}
				}
				drawLine(screen, r.x-1, r.y+i, 1, rule, false)
			}
		}
		shown := kc.cards[first[c]:]
		fits := kanbanFits(r.h, shown)
		row := r.y + 1
		for _, card := range shown[:fits] {
			on := card.key != "" && card.key == sel && k.level == workAtCards
			for _, l := range card.lines {
				if on && off > 0 {
					l = scrolled(l, r.w, off)
				}
				drawLine(screen, r.x, row, r.w, FitLine(l, r.w), on)
				row++
			}
		}
		if more := len(shown) - fits; more > 0 && row < r.y+r.h { // never on the band below or the frame (foureyes)
			drawLine(screen, r.x, row, r.w, Line{{Text: fmt.Sprintf("+%d more", more), Role: RoleMuted}}, false)
		}
	}
}

// cardAt is the key of the card under (mx, my), "" for none.
func (k *kanbanView) cardAt(mx, my, x, y, w, h int, sel string) string {
	rects := kanbanGrid(len(k.cols), x, y, w, h)
	_, _, first := k.place(sel, rects)
	for c, r := range rects {
		if mx < r.x || mx >= r.x+r.w || my <= r.y || my >= r.y+r.h { // the heading (r.y) is no card
			continue
		}
		shown := k.cols[c].cards[first[c]:]
		row := r.y + 1
		for _, card := range shown[:kanbanFits(r.h, shown)] {
			if my < row+len(card.lines) {
				return card.key
			}
			row += len(card.lines)
		}
	}
	return ""
}

// step is ←→ on the Kanban: the card at the same rank in the next or
// previous column with a selectable card (else its last one), wrapping
// around the columns; with no card selected, the first card in that
// direction. "" only when no column has a card. The arrows never step the
// tabs here (the person, 2026-11-11): the digits do.
func (k *kanbanView) step(sel string, next bool) string {
	n := len(k.cols)
	keysOf := func(c int) []string {
		var keys []string
		for _, card := range k.cols[c].cards {
			if card.key != "" {
				keys = append(keys, card.key)
			}
		}
		return keys
	}
	col, rank := -1, 0
	for c := range k.cols {
		for r, key := range keysOf(c) {
			if key == sel {
				col, rank = c, r
			}
		}
	}
	d := 1
	if !next {
		d = -1
	}
	if col < 0 { // no card selected: the first card in that direction
		for i := 0; i < n; i++ {
			c := i
			if !next {
				c = n - 1 - i
			}
			if keys := keysOf(c); len(keys) > 0 {
				return keys[0]
			}
		}
		return ""
	}
	// Around the columns, wrapping (the person, 2026-11-11): never past an
	// edge into the tabs; a lone non-empty column keeps its card.
	for i := 1; i <= n; i++ {
		c := ((col+d*i)%n + n) % n
		if keys := keysOf(c); len(keys) > 0 {
			return keys[min(rank, len(keys)-1)]
		}
	}
	return sel
}
