package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The plan board (home-inbox-drawer §4): one producer, planBoard, for the
// Plan tab (4) and the plan's page (p, P), which is a drawer under For you
// at the wide tier. The sections are NOW, NEXT, LATER and DONE (folded to a
// count until d shows it); the progress sits above them; at the wide tier the
// selected task's detail is under the list, inside the frame (views-redesign
// §4.1).

// planDetailRows is the selected task's detail under the list, inside the
// frame (views-redesign §4.1 point 2): a rule naming it, then two rows of
// its facts.
const planDetailRows = 3

// planBoardView is what the tab and the page draw: the body's lines, its
// text for y, its selectable items (a task, by line) and the selected task's
// detail, planDetailRows lines (nil without one: below the wide tier).
type planBoardView struct {
	lines  []Line
	plain  string
	items  []planItem
	detail []Line
	head   int // the lines above the sections (the progress, the goal): the Plan tab's frame says them instead
}

// planSection is one section's rows, in the order they are drawn.
type planSection struct {
	name string
	rows []plan.Row
}

// planApproved says both reviewers approved the task, waiting for its
// delivery or the person's merge: APPROVED's (views-redesign §4, Q3).
func planApproved(r plan.Row) bool {
	return r.State != plan.Done && r.Verdict == plan.Approved && r.Task.Owner != plan.Person &&
		(r.State == plan.InReview || r.State == plan.Waiting)
}

// planPrioRank orders by priority: high, normal, low.
func planPrioRank(t plan.Task) int {
	switch t.Priority {
	case bus.PriorityHigh:
		return 0
	case bus.PriorityLow:
		return 2
	}
	return 1
}

// planSections splits the rows into the roadmap (views-redesign §4): NOW is
// in hand (in progress, in review, answered, blocked); APPROVED is what both
// reviewers approved, waiting for delivery or the merge; NEXT is not started
// and unblocked, the person's decisions among them; LATER is blocked by
// another task, then the low-priority tasks, blocked or not, then the
// unknown; DONE last. NOW and NEXT sort by priority, then file order.
// unknownOnly: a read failed and every row not done is Unknown, so the board
// draws no sections but those tasks, in file order, under one muted line.
func planSections(rows []plan.Row) (secs []planSection, unknownOnly bool) {
	var now, approved, next, waits, low, unknown, done []plan.Row
	for _, r := range rows {
		switch {
		case r.State == plan.Done:
			done = append(done, r)
		case r.State == plan.Unknown:
			unknown = append(unknown, r)
		case planApproved(r):
			approved = append(approved, r)
		case r.State == plan.Upcoming && len(r.WaitingOn) > 0:
			waits = append(waits, r)
		case (r.State == plan.Upcoming || r.State == plan.Waiting && r.Task.Owner == plan.Person) && r.Task.Priority == bus.PriorityLow:
			low = append(low, r)
		case r.State == plan.Upcoming, r.State == plan.Waiting && r.Task.Owner == plan.Person:
			next = append(next, r)
		default: // in progress, in review, answered, blocked, an agent's waiting
			now = append(now, r)
		}
	}
	if len(unknown) > 0 && len(unknown)+len(done) == len(rows) {
		return []planSection{{"", unknown}, {"DONE", done}}, true
	}
	byPrio := func(rs []plan.Row) {
		slices.SortStableFunc(rs, func(a, b plan.Row) int { return planPrioRank(a.Task) - planPrioRank(b.Task) })
	}
	byPrio(now)
	byPrio(next)
	later := append(append(waits, low...), unknown...)
	return []planSection{{"NOW", now}, {"APPROVED", approved}, {"NEXT", next}, {"LATER", later}, {"DONE", done}}, false
}

// planBoard renders the board for width cells at tier. sel is the selected
// item; wide asks for the selected task's detail (drawn under the list). The narrow tier keeps the progress's count only (§6).
func planBoard(p planState, sel int, showDone bool, now time.Time, width int, tier Tier, wide bool, reqs []request) planBoardView {
	return planBoardOf(p, sel, showDone, false, now, width, tier, wide, reqs)
}

// planBoardOf is planBoard with LATER's fold: below the wide tier (no detail:
// the 80-column mock-up) LATER is a count line until laterOpen
// (views-redesign §4, Q5), and DONE is its count line until showDone; both
// lines are items Enter opens.
func planBoardOf(p planState, sel int, showDone, laterOpen bool, now time.Time, width int, tier Tier, wide bool, reqs []request) planBoardView {
	var v planBoardView
	var pl strings.Builder
	emit := func(l Line) {
		v.lines = append(v.lines, l)
		pl.WriteString(strings.TrimRight(l.text(), " ") + "\n")
	}
	emit(planProgress(p, width, tier))
	if tier != TierNarrow { // the plan's goal, when it was loaded and from which commit, as the P page said
		emit(planHead(p, now))
	}
	v.head = len(v.lines)
	paces := ownerPaces(reqs)
	cols := planColsOf(p)
	secs, unknownOnly := planSections(p.rows)
	secOf := map[string]string{} // each task's section: its row's first cell reads it
	for _, sec := range secs {
		for _, r := range sec.rows {
			secOf[r.Task.ID] = sec.name
		}
	}
	if unknownOnly {
		emit(nil)
		emit(Line{{Text: "a read failed: states unknown", Role: RoleMuted}})
	}
	var selRow *plan.Row
	row := func(r plan.Row) {
		idx := len(v.items)
		v.items = append(v.items, planItem{task: r.Task.ID, line: len(v.lines)})
		if idx == sel {
			selRow = &r
		}
		emit(planBoardRow(p, r, secOf[r.Task.ID], idx == sel, now, width, cols))
		if l, ok := planProgressLine(r, reqs, paces, now); ok { // progress-line §3: not a row
			emit(FitLine(append(Line{{Text: "      ", Role: RoleDim}}, l...), width))
		}
	}
	if len(p.file.Groups) > 0 && !unknownOnly { // plan-chantier §3: one block per group
		planGroupBlocks(p, showDone, width, tier, emit, row)
		secs = nil
	}
	fold := func(name string, l Line) { // a folded section's line, an item Enter opens
		v.items = append(v.items, planItem{fold: name, line: len(v.lines)})
		emit(append(planMark(len(v.items)-1 == sel), l...))
	}
	for _, sec := range secs {
		if len(sec.rows) == 0 {
			continue
		}
		if sec.name == "LATER" && tier != TierWide && !laterOpen {
			emit(nil)
			high := 0
			for _, r := range sec.rows {
				if r.Task.Priority == bus.PriorityHigh {
					high++
				}
			}
			text := fmt.Sprintf("LATER %d", len(sec.rows))
			if high > 0 {
				text += fmt.Sprintf(" %s %d %s", GlyphNone, high, GlyphUp)
			}
			fold("later", Line{{Text: text, Role: RoleText, Bold: true}, {Text: " (enter)", Role: RoleMuted}})
			continue
		}
		if sec.name == "DONE" && !showDone {
			emit(nil)
			if tier != TierWide {
				fold("done", Line{{Text: fmt.Sprintf("DONE %d", len(sec.rows)), Role: RoleMuted}, {Text: " (enter)", Role: RoleMuted}})
				continue
			}
			emit(Line{{Text: fmt.Sprintf("DONE  %d", len(sec.rows)), Role: RoleMuted}, {Text: "   d shows", Role: RoleMuted}})
			continue
		}
		if sec.name != "" {
			emit(nil)
			emit(Line{{Text: fmt.Sprintf("%s  %d", sec.name, len(sec.rows)), Role: RoleText, Bold: true}})
		}
		for _, r := range sec.rows {
			row(r)
		}
	}
	v.lines = append(v.lines, nil)
	v.plain = pl.String()
	if wide && selRow != nil {
		v.detail = planDetail(p, *selRow, width)
	}
	return v
}

// planCols are the roadmap's column widths (the mock-ups' aligned columns):
// the widest title and owner of the plan, and the state's; zero pads nothing.
type planCols struct{ title, owner, state int }

func planColsOf(p planState) planCols {
	c := planCols{state: 11} // "in progress", the widest state word an "after" follows
	for _, r := range p.rows {
		c.title = max(c.title, Width(Preview(r.Task.Title)))
		owner := r.Task.Owner
		if owner == plan.Person {
			owner = GlyphHuman + " " + owner
		}
		c.owner = max(c.owner, Width(clip(owner, 14)))
	}
	return c
}

// planInHand is the plan's task in hand (the first in progress or in
// review, else the first waiting for the person, else the first blocked;
// an answered one is master's to record, not in hand: reviewer-codex) and
// the first upcoming one; nil when there is none. ok says a plan is loaded.
// The idle view's next frame, the Plan header and its highlight read it
// (progress-line §2.3).
func planInHand(p planState) (now, next *plan.Row, ok bool) {
	if !p.loaded {
		return nil, nil, false
	}
	for _, states := range [][]plan.State{{plan.InProgress, plan.InReview}, {plan.Waiting}, {plan.Blocked}} {
		for i := range p.rows {
			if now == nil && indexOfState(states, p.rows[i].State) >= 0 {
				now = &p.rows[i]
			}
		}
	}
	for i := range p.rows { // the task in hand is never upcoming
		if p.rows[i].State == plan.Upcoming {
			next = &p.rows[i]
			break
		}
	}
	return now, next, true
}

// planProgress is the line above the sections: the plan, "N of M done", a
// bar of thin cells (never animated), the share, then the task in hand and
// the next one (progress-line §2.3), next dropped first, then now, when the
// width is short; narrow: "6/11 55%" only.
func planProgress(p planState, width int, tier Tier) Line {
	done, total := plan.Count(p.rows)
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	count := fmt.Sprintf("%d/%d", done, total)
	if p.partial {
		count += "?"
	}
	if tier == TierNarrow {
		return Line{{Text: fmt.Sprintf("%s %d%%", count, pct), Role: RoleMuted}}
	}
	count = fmt.Sprintf("%d of %d", done, total)
	if p.partial {
		count += "?"
	}
	l := Line{{Text: Preview(p.file.Plan), Role: RoleText}, {Text: "  " + count + " done  ", Role: RoleMuted}}
	barW := clamp(width/6, 6, 20)
	fill := 0
	if total > 0 {
		fill = done * barW / total
	}
	l = append(l, Seg{Text: strings.Repeat(GlyphThinBar, fill), Role: RoleDone}, Seg{Text: strings.Repeat(GlyphThinBar, barW-fill), Role: RoleMuted}) // the gauges' thin cells
	l = append(l, Seg{Text: fmt.Sprintf("  %d%%", pct), Role: RoleMuted})
	sep := Seg{Text: "  " + GlyphNone + "  ", Role: RoleMuted}
	now, next, _ := planInHand(p)
	if now == nil && next == nil && total > 0 && done == total {
		return append(l, sep, Seg{Text: "every task is done", Role: RoleMuted})
	}
	var parts []Line
	if now != nil {
		parts = append(parts, Line{{Text: "now: ", Role: RoleMuted}, {Text: Preview(now.Task.Title), Role: RoleText}, {Text: inHandOwner(*now), Role: RoleMuted}})
	}
	if next != nil {
		parts = append(parts, Line{{Text: "next: ", Role: RoleMuted}, {Text: Preview(next.Task.Title), Role: RoleMuted}})
	}
	for n := len(parts); n >= 0; n-- { // too wide: next goes first, then now
		try := append(Line(nil), l...)
		for _, part := range parts[:n] {
			try = append(append(try, sep), part...)
		}
		if try.Width() <= width {
			return try
		}
	}
	return l
}

// inHandOwner is " (owner)", or "" with no owner.
func inHandOwner(r plan.Row) string {
	if r.Task.Owner == "" {
		return ""
	}
	return " (" + Preview(r.Task.Owner) + ")"
}

// planHead is the muted line under the progress: the goal, the load's age
// and commit, stale or kept values.
func planHead(p planState, now time.Time) Line {
	text := Preview(p.file.Goal) + " " + GlyphNone + " loaded " + plan.Age(p.rec.LoadedAt, now) + " ago (" + short7(p.rec.Commit) + ")"
	if now.Sub(time.UnixMilli(p.rec.LoadedAt)) > plan.StaleAfter {
		text += " " + GlyphNone + " stale: master, agentbus plan load"
	}
	if p.partial {
		text += " " + GlyphNone + " ? a read failed: values kept"
	}
	return Line{{Text: text, Role: RoleMuted}}
}

// planBoardRow is one task (views-redesign §4): its first cell, the title,
// the owner (♙ before the person's), the state's word or its details, and
// its dependencies, "after <first>" with "+N" for more. The first cell is
// the priority (↑ high, ↓ low), else the state's glyph in NOW and ✓ in
// APPROVED, nothing in NEXT and LATER. The person's task reads "decide",
// never "waiting for you": what the person owes now is Work's; a LATER task
// reads why it is there, "blocked" (waiting on a task) or "low".
func planBoardRow(p planState, r plan.Row, sec string, selected bool, now time.Time, width int, cols planCols) Line {
	glyph, role := planLook(r.State)
	switch {
	case r.Task.Priority == bus.PriorityHigh:
		glyph, role = GlyphUp, RoleAccent
	case r.Task.Priority == bus.PriorityLow:
		glyph, role = GlyphDownArrow, RoleMuted
	case sec == "APPROVED":
		glyph, role = GlyphDone, RoleDispatch
	case sec == "NEXT", sec == "LATER":
		glyph = " "
	}
	owner := r.Task.Owner
	if owner == plan.Person {
		owner = GlyphHuman + " " + owner
	}
	rd := r
	if sec == "LATER" { // "after" says what it waits on: its word is "blocked"
		rd.WaitingOn = nil
	}
	why := planRowDetails(rd, now)
	if why == "" {
		why = plan.Label(r.State)
		switch {
		case r.State == plan.Waiting && r.Task.Owner == plan.Person:
			why = "decide"
		case sec == "LATER" && len(r.WaitingOn) > 0: // why it is later (the mock-up): waiting on a task
			why = "blocked"
		case sec == "LATER" && r.Task.Priority == bus.PriorityLow: // or its low priority
			why = "low"
		}
	}
	if p.kept(r) {
		why += " ?" // derived from a value kept after a failed read
	}
	owner = clip(owner, 14)
	if a := r.Task.After; len(a) > 0 {
		why = padRight(why, cols.state) + "  after " + a[0]
		if len(a) > 1 {
			why += fmt.Sprintf(" +%d", len(a)-1)
		}
	}
	head := append(planMark(selected), Seg{Text: glyph + " ", Role: role})
	tail := Line{{Text: "  " + padRight(owner, cols.owner), Role: RoleMuted}, {Text: "  " + why, Role: RoleMuted}}
	titleW := max(8, width-head.Width()-min(tail.Width(), width/2))
	title := Seg{Text: padRight(clip(Preview(r.Task.Title), titleW), min(cols.title, titleW)), Role: RoleText}
	if now, _, _ := planInHand(p); now != nil && now.Task.ID == r.Task.ID { // the task in hand (progress-line §2.4)
		title.Role, title.Bold = RoleAccent, true
	}
	l := append(head, title)
	return FitLine(append(l, tail...), width)
}

// planFact is one of a task's facts under the list: its key and value.
type planFact struct{ key, val string }

// planFacts are the selected row's facts, read from the derived plan.Row
// (Verdict is its roll-up label): its state, what the person may do, its
// owner, priority, review and PR, then its dependencies (✓ when done), the tasks that
// wait on it and its reason (views-redesign §4, §4.1).
func planFacts(p planState, r plan.Row) []planFact {
	or := func(s string) string {
		if s == "" {
			return GlyphDash
		}
		return s
	}
	state := map[string]plan.State{}
	var unblocks []string
	for _, o := range p.rows {
		state[o.Task.ID] = o.State
		if slices.Contains(o.Task.After, r.Task.ID) {
			unblocks = append(unblocks, o.Task.ID)
		}
	}
	after := GlyphDash
	if len(r.Task.After) > 0 {
		var deps []string
		for _, a := range r.Task.After {
			if state[a] == plan.Done {
				a += " " + GlyphDone
			}
			deps = append(deps, a)
		}
		after = strings.Join(deps, ", ")
	}
	prio := "normal"
	switch r.Task.Priority {
	case bus.PriorityHigh:
		prio = GlyphUp + " high"
	case bus.PriorityLow:
		prio = GlyphDownArrow + " low"
	}
	forYou := "nothing"
	if _, ok := planStartable(planState{loaded: true, rows: []plan.Row{r}}, r.Task.ID); ok {
		forYou = "start it: enter, then s"
	} else if r.Task.Owner == plan.Person && r.State != plan.Done {
		forYou = plan.Label(r.State)
	}
	pr := GlyphDash
	if r.PR > 0 {
		pr = fmt.Sprintf("%d", r.PR)
	}
	return []planFact{ // the first row by what matters to the person, so a short width cuts the least
		{"state", plan.Label(r.State)},
		{"for you", forYou},
		{"owner", or(r.Task.Owner)},
		{"prio", prio},
		{"review", or(r.Verdict)},
		{"PR", pr}, // a PR and a reason both hold (a blocked task with its PR linked): two facts (reviewer-codex)
		{"after", after},
		{"unblocks", or(strings.Join(unblocks, ", "))},
		{"reason", or(r.Reason)},
	}
}

// planDetail is the selected task's detail, planDetailRows rows of width
// cells (views-redesign §4.1 point 2): a rule naming the task, enter's act at
// its right; its first six facts; the last three. A row keeps its facts
// whole and cuts its last one when short.
func planDetail(p planState, r plan.Row, width int) []Line {
	fs := planFacts(p, r)
	facts := func(fs []planFact) Line {
		var l Line
		for i, f := range fs {
			if i > 0 {
				l = append(l, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
			}
			l = append(l, Seg{Text: f.key + " ", Role: RoleMuted}, Seg{Text: Preview(f.val), Role: RoleText})
		}
		return FitLine(l, width)
	}
	head := Line{{Text: strings.Repeat(glyphHorizontal, 2) + " ", Role: RoleMuted}, {Text: "Task ", Role: RoleMuted}, {Text: Preview(r.Task.ID), Role: RoleText, Bold: true}, {Text: " ", Role: RoleMuted}}
	act := Line{{Text: " ", Role: RoleMuted}, {Text: "enter", Role: RoleAccent, Bold: true}, {Text: " task page", Role: RoleMuted}}
	if fill := width - head.Width() - act.Width(); fill > 0 {
		head = append(append(head, Seg{Text: strings.Repeat(glyphHorizontal, fill), Role: RoleMuted}), act...)
	}
	return []Line{FitLine(head, width), facts(fs[:6]), facts(fs[6:])}
}

// drawPlanDetail draws the detail's rows from (x, y), w cells wide.
func drawPlanDetail(g cellSink, x, y, w int, detail []Line) {
	for i, l := range detail {
		drawLine(g, x, y+i, w, l, false)
	}
}

// planLink is the plan task that names a board task (its board field).
type planLink struct {
	id, title string
	state     plan.State
}

// planLinks maps each board task a plan task names to that plan task
// (progress-line §2.2); empty without a plan.
func planLinks(p planState) map[string]planLink {
	out := map[string]planLink{}
	if !p.loaded {
		return out
	}
	for _, r := range p.rows {
		if r.Task.Board != "" {
			out[r.Task.Board] = planLink{id: r.Task.ID, title: r.Task.Title, state: r.State}
		}
	}
	return out
}

// planLinkLine is a request page's plan line: "plan  #t4 the title (in
// progress)"; ok false when no plan task names the request.
func planLinkLine(links map[string]planLink, task string) (Line, bool) {
	pl, ok := links[task]
	if !ok {
		return nil, false
	}
	return Line{S(RoleMuted, "plan  "), S(RoleText, "#"+Preview(pl.id)+" "+Preview(pl.title)), S(RoleMuted, " ("+plan.Label(pl.state)+")")}, true
}

// planProgressLine is the progress line under a task in progress whose
// board request (its board field) was accepted.
func planProgressLine(r plan.Row, reqs []request, paces map[string]pace, now time.Time) (Line, bool) {
	if r.State != plan.InProgress || r.Task.Board == "" {
		return nil, false
	}
	for _, q := range reqs {
		if q.Task == r.Task.Board && q.Tracked && q.State == "accepted" {
			return progressLine(q, paces[reqOwner(q)], now)
		}
	}
	return nil, false
}
