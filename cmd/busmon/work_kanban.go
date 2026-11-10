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
// column's width (never wrapped: a third line would break the columns'
// rhythm). At 130 columns a column is 31 cells: the title keeps 29.
//
// The folded lines close the Ideas column: between agents and done, two cards
// Enter opens as on the list. On a selected card ←→ move between columns, to
// the same rank (else the last card), and past the first or last column to
// the next tab; with none selected they step the tabs. ↑↓ walk the cards in
// reading order, column by column.

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
	lines [2]Line
}

// kanbanCol is one column: its heading and its cards.
type kanbanCol struct {
	title string
	cards []kanbanCard
}

// kanbanView is the Kanban the Work list draws in place of its rows.
type kanbanView struct {
	cols []kanbanCol
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
		you.cards = append(you.cards, kanbanCard{key: it.Key, lines: [2]Line{
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
		if len(r.Reviewing) > 0 { // the gate it holds: its age on line 1, unless a task is its title
			g := r.Reviewing[0]
			if r.Task == nil {
				what = "reviewing " + g.Subject + " for " + g.Who
			}
			where = strings.TrimSpace(where + " review " + gateAge(g.Since, d.now))
		} else if len(r.Reviewers) > 0 {
			where = strings.TrimSpace(where + " " + strings.TrimSpace(strings.TrimPrefix(teamReviewers(r, d.now), "  review:")))
		}
		team.cards = append(team.cards, kanbanCard{key: workAgentPrefix + r.Agent, lines: [2]Line{
			head(reqMark(r.Task), glyph, role, r.Agent, where), title(what, RoleText)}})
	}
	if len(w.Idle) > 0 { // the free agents: they can take a request (correction 6)
		team.cards = append(team.cards, kanbanCard{lines: [2]Line{
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
		next.cards = append(next.cards, kanbanCard{key: it.Key, lines: [2]Line{
			head(reqMark(it.Req), GlyphIdle, RoleMuted, workWho(it.Who, true), where), title(it.Text, RoleText)}})
	}

	ideas := kanbanCol{title: fmt.Sprintf("Ideas  %d", len(w.Ideas))}
	if w.IdeasErr {
		ideas.cards = append(ideas.cards, kanbanCard{lines: [2]Line{
			head(noMark, GlyphIdea, RoleAttention, "unread"), title("the open questions could not be read", RoleAttention)}})
	}
	for _, it := range w.Ideas {
		ideas.cards = append(ideas.cards, kanbanCard{key: workIdeaPrefix + it.ID, lines: [2]Line{
			head(noMark, GlyphIdea, RoleAccent, "idea", path.Base(it.Where)), title(it.Title, RoleText)}})
	}
	ideas.cards = append(ideas.cards, kanbanCard{key: workFoldBetween, lines: [2]Line{
		head(noMark, GlyphDash, RoleMuted, "between agents", fmt.Sprint(w.Between)), title("nobody waits on you (enter)", RoleMuted)}})
	if w.Planned {
		ideas.cards = append(ideas.cards, kanbanCard{key: workFoldDone, lines: [2]Line{
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
// when each keeps kanbanMinCol cells, else two per band, else one; a blank
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
		if fits := kanbanFits(rects[col].h, len(k.cols[col].cards)); rank >= fits {
			first[col] = rank - fits + 1
		}
	}
	return col, rank, first
}

// kanbanFits is how many cards a column of height h shows: its heading, two
// lines a card, and a "+N more" line when they do not all fit.
func kanbanFits(h, cards int) int {
	room := (h - 1) / 2
	if cards > room {
		room = (h - 2) / 2
	}
	return max(room, 0)
}

// draw draws the columns inside the list's inner area; sel is the list's
// selection.
func (k *kanbanView) draw(screen cellSink, x, y, w, h int, sel string) {
	rects := kanbanGrid(len(k.cols), x, y, w, h)
	_, _, first := k.place(sel, rects)
	for c, kc := range k.cols {
		r := rects[c]
		if r.w <= 0 || r.h <= 0 {
			continue
		}
		drawLine(screen, r.x, r.y, r.w, Line{{Text: kc.title, Role: RoleText, Bold: true}}, false)
		if c > 0 && r.x > x { // the blank column apart
			for i := 0; i < r.h; i++ {
				drawLine(screen, r.x-1, r.y+i, 1, nil, false)
			}
		}
		fits := kanbanFits(r.h, len(kc.cards))
		row := r.y + 1
		for i := first[c]; i < len(kc.cards) && i < first[c]+fits; i++ {
			card := kc.cards[i]
			on := card.key != "" && card.key == sel
			drawLine(screen, r.x, row, r.w, FitLine(card.lines[0], r.w), on)
			drawLine(screen, r.x, row+1, r.w, FitLine(card.lines[1], r.w), on)
			row += 2
		}
		if more := len(kc.cards) - first[c] - fits; more > 0 && fits >= 0 {
			drawLine(screen, r.x, row, r.w, Line{{Text: fmt.Sprintf("+%d more", more), Role: RoleMuted}}, false)
		}
	}
}

// cardAt is the key of the card under (mx, my), "" for none.
func (k *kanbanView) cardAt(mx, my, x, y, w, h int, sel string) string {
	rects := kanbanGrid(len(k.cols), x, y, w, h)
	_, _, first := k.place(sel, rects)
	for c, r := range rects {
		if mx < r.x || mx >= r.x+r.w || my <= r.y || my >= r.y+r.h {
			continue
		}
		i := first[c] + (my-r.y-1)/2
		if i < first[c]+kanbanFits(r.h, len(k.cols[c].cards)) && i < len(k.cols[c].cards) {
			return k.cols[c].cards[i].key
		}
	}
	return ""
}

// step is ←→ on a selected card: the card at the same rank in the next or
// previous column with a selectable card (else its last one). It returns ""
// with no card selected, or past the first or last column: the arrows then
// step the lower panel's tabs, as on every other tab.
func (k *kanbanView) step(sel string, next bool) string {
	col, rank := -1, 0
	for c, kc := range k.cols {
		r := 0 // its rank among the column's selectable cards
		for _, card := range kc.cards {
			if card.key == "" {
				continue
			}
			if card.key == sel {
				col, rank = c, r
			}
			r++
		}
	}
	if col < 0 {
		return ""
	}
	d := 1
	if !next {
		d = -1
	}
	for c := col + d; c >= 0 && c < len(k.cols); c += d {
		var keys []string
		for _, card := range k.cols[c].cards {
			if card.key != "" {
				keys = append(keys, card.key)
			}
		}
		if len(keys) > 0 {
			return keys[min(rank, len(keys)-1)]
		}
	}
	return ""
}
