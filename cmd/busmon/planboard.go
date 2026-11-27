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
	emit(planHeader(p, now, width, tier)) // one header line, then a rule (views-redesign §4.2)
	emit(planRule(width))
	paces := ownerPaces(reqs)
	cols := planR2ColsOf(p)
	secs, unknownOnly := planSections(p.rows)
	secOf := map[string]string{} // each task's section: its row's first cell reads it
	for _, sec := range secs {
		for _, r := range sec.rows {
			secOf[r.Task.ID] = sec.name
		}
	}
	if unknownOnly {
		emit(Line{{Text: "a read failed: states unknown", Role: RoleMuted}})
	}
	var selRow *plan.Row
	row := func(r plan.Row) {
		idx := len(v.items)
		v.items = append(v.items, planItem{task: r.Task.ID, line: len(v.lines)})
		if idx == sel {
			selRow = &r
		}
		emit(planR2Row(p, r, idx == sel, now, width, cols))
		if l, ok := planProgressLine(r, reqs, paces, now); ok { // progress-line §3: not a row
			emit(FitLine(append(Line{{Text: "      ", Role: RoleDim}}, l...), width))
		}
		if why := planParallel(r, reqs); why != "" { // sibling-enforce §3: the exception, under its task
			emit(FitLine(Line{{Text: "      ", Role: RoleDim}, {Text: "parallel: " + Preview(why), Role: RoleAttention}}, width))
		}
	}
	fold := func(name string, l Line) { // a folded section's line, an item Enter opens
		v.items = append(v.items, planItem{fold: name, line: len(v.lines)})
		mark := RoleMuted // the fold's own ▶, accented when selected (views-redesign §4.2)
		if len(v.items)-1 == sel {
			mark = RoleAccent
		}
		emit(append(Line{{Text: GlyphSelect + " ", Role: mark}}, l...))
	}
	if len(p.file.Groups) > 0 && !unknownOnly { // plan-chantier §3: one block per group
		planGroupBlocks(p, showDone, width, tier, emit, row, fold)
		secs = nil
	}
	for _, sec := range secs {
		if len(sec.rows) == 0 {
			continue
		}
		if sec.name == "LATER" && tier != TierWide && !laterOpen {
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
			fold("later", Line{{Text: text, Role: RoleText, Bold: true}, {Text: "   (enter)", Role: RoleDim}})
			continue
		}
		if sec.name == "DONE" && !showDone { // folded into one line, an item Enter opens (views-redesign §4.2)
			fold("done", Line{{Text: fmt.Sprintf("DONE %s %s", GlyphNone, planTasks(len(sec.rows))), Role: RoleMuted}, {Text: "   (d shows)", Role: RoleDim}})
			continue
		}
		if sec.name != "" { // no blank line between sections (views-redesign §4.2)
			emit(Line{{Text: fmt.Sprintf("%s %s  %d", GlyphDown, sec.name, len(sec.rows)), Role: RoleText, Bold: true}})
		}
		for _, r := range sec.rows {
			row(r)
		}
	}
	v.plain = pl.String() // no blank line at the end: the strip or the frame follows (views-redesign §4.2)
	if wide && selRow != nil {
		v.detail = planStrip(p, *selRow, now, width)
	}
	return v
}

// planInHand is the plan's task in hand and the first upcoming one
// (plan.NowNext); ok says a plan is loaded. The idle view's next frame, the
// Plan header and its highlight read it (progress-line §2.3).
func planInHand(p planState) (now, next *plan.Row, ok bool) {
	if !p.loaded {
		return nil, nil, false
	}
	now, next = plan.NowNext(p.rows)
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

// planParallel is the parallel exception's reason of r's board request
// (its board field), "" none.
func planParallel(r plan.Row, reqs []request) string {
	if r.Task.Board == "" {
		return ""
	}
	for _, q := range reqs {
		if q.Task == r.Task.Board && q.Tracked {
			return q.Parallel
		}
	}
	return ""
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
