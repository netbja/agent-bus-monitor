package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"
)

// The plan board (home-inbox-drawer §4): one producer, planBoard, for the
// Plan tab (4) and the plan's page (p, P), which is a drawer right of For you
// at the wide tier. The sections are NOW, NEXT, LATER and DONE (folded to a
// count until d shows it); the progress sits above them; at the wide tier a
// side panel holds the selected task's facts.

// planSideWidth is the side panel's frame, borders included; the board takes
// it beside the list only when the list keeps planListMin cells.
const (
	planSideWidth = 34
	planListMin   = 40
)

// planBoardView is what the tab and the page draw: the body's lines, its
// text for y, its selectable items (a task, by line) and the side panel's
// lines for the selected task (nil when there is no side panel).
type planBoardView struct {
	lines []Line
	plain string
	items []planItem
	side  []Line
}

// planNow are NOW's states, in plan.Order's order.
var planNow = []plan.State{plan.Waiting, plan.Answered, plan.Blocked, plan.InReview, plan.InProgress}

// planSection is one section's rows, in the order they are drawn.
type planSection struct {
	name string
	rows []plan.Row
}

// planSections splits the rows (§4.2): NOW by plan.Order, NEXT and LATER in
// file order, DONE last. unknownOnly: a read failed and every row not done is
// Unknown, so the board draws no NOW, NEXT or LATER but those tasks, in file
// order, under one muted line.
func planSections(rows []plan.Row) (secs []planSection, unknownOnly bool) {
	var now, next, later, done, unknown []plan.Row
	for _, s := range planNow {
		for _, r := range rows {
			if r.State == s {
				now = append(now, r)
			}
		}
	}
	for _, r := range rows {
		switch {
		case r.State == plan.Upcoming && len(r.WaitingOn) == 0:
			next = append(next, r)
		case r.State == plan.Upcoming, r.State == plan.Unknown:
			later = append(later, r)
		case r.State == plan.Done:
			done = append(done, r)
		}
		if r.State == plan.Unknown {
			unknown = append(unknown, r)
		}
	}
	if len(unknown) > 0 && len(unknown)+len(done) == len(rows) {
		return []planSection{{"", unknown}, {"DONE", done}}, true
	}
	return []planSection{{"NOW", now}, {"NEXT", next}, {"LATER", later}, {"DONE", done}}, false
}

// planBoard renders the board for width cells at tier. sel is the selected
// item; wide asks for the side panel's lines (the caller gives the list its
// width beside it). The narrow tier keeps the progress's count only (§6).
func planBoard(p planState, sel int, showDone bool, now time.Time, width int, tier Tier, wide bool, reqs []request) planBoardView {
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
	paces := ownerPaces(reqs)
	secs, unknownOnly := planSections(p.rows)
	if unknownOnly {
		emit(nil)
		emit(Line{{Text: "a read failed: states unknown", Role: RoleMuted}})
	}
	var selRow *plan.Row
	for _, sec := range secs {
		if len(sec.rows) == 0 {
			continue
		}
		if sec.name == "DONE" && !showDone {
			emit(nil)
			emit(Line{{Text: fmt.Sprintf("DONE  %d", len(sec.rows)), Role: RoleMuted}, {Text: "   d shows", Role: RoleMuted}})
			continue
		}
		if sec.name != "" {
			emit(nil)
			emit(Line{{Text: fmt.Sprintf("%s  %d", sec.name, len(sec.rows)), Role: RoleText, Bold: true}})
		}
		for _, r := range sec.rows {
			idx := len(v.items)
			v.items = append(v.items, planItem{task: r.Task.ID, line: len(v.lines)})
			if idx == sel {
				selRow = &r
			}
			emit(planBoardRow(p, r, idx == sel, now, width))
			if l, ok := planProgressLine(r, reqs, paces, now); ok { // progress-line §3: not a row
				emit(FitLine(append(Line{{Text: "      ", Role: RoleDim}}, l...), width))
			}
		}
	}
	v.lines = append(v.lines, nil)
	v.plain = pl.String()
	if wide && selRow != nil {
		v.side = planSide(*selRow, now)
	}
	return v
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

// planBoardRow is one task: the state's glyph, the title, the owner, the
// state's word or its reason.
func planBoardRow(p planState, r plan.Row, selected bool, now time.Time, width int) Line {
	glyph, role := planLook(r.State)
	owner := r.Task.Owner
	if owner == plan.Person {
		owner = GlyphHuman + " " + owner
	}
	why := planRowDetails(r, now)
	if why == "" {
		why = plan.Label(r.State)
	}
	if p.kept(r) {
		why += " ?" // derived from a value kept after a failed read
	}
	head := append(planMark(selected), Seg{Text: glyph + " ", Role: role})
	tail := Line{{Text: "  " + clip(owner, 14), Role: RoleMuted}, {Text: "  " + why, Role: RoleMuted}}
	titleW := max(8, width-head.Width()-min(tail.Width(), width/2))
	title := Seg{Text: clip(Preview(r.Task.Title), titleW), Role: RoleText}
	if now, _, _ := planInHand(p); now != nil && now.Task.ID == r.Task.ID { // the task in hand (progress-line §2.4)
		title.Role, title.Bold = RoleAccent, true
	}
	l := append(head, title)
	return FitLine(append(l, tail...), width)
}

// planSide is the side panel (§4.3): the selected row's own fields, read from
// the derived plan.Row only (Verdict is its roll-up label), then Enter's act.
func planSide(r plan.Row, now time.Time) []Line {
	or := func(s string) string {
		if s == "" {
			return GlyphDash
		}
		return s
	}
	review := or(r.Verdict)
	waits := GlyphDash
	if len(r.WaitingOn) > 0 {
		waits = strings.Join(r.WaitingOn, ", ")
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
	field := func(k, v string) Line {
		return Line{{Text: fmt.Sprintf("%-9s", k), Role: RoleMuted}, {Text: v, Role: RoleText}}
	}
	return []Line{
		{{Text: Preview(r.Task.ID), Role: RoleText, Bold: true}},
		field("state", plan.Label(r.State)),
		field("owner", or(r.Task.Owner)),
		field("review", review),
		field("PR", pr), // a PR and a reason both hold (a blocked task with its PR linked): two fields (reviewer-codex)
		field("reason", or(r.Reason)),
		field("waits on", waits),
		field("for you", forYou),
		nil,
		{{Text: "enter", Role: RoleAccent, Bold: true}, {Text: " task page", Role: RoleMuted}},
	}
}

// planBoardItems is the Plan tab's rows from the board (keys task:<id>).
func planBoardItems(v planBoardView, width int) []ListItem {
	keys := make(map[int]string, len(v.items))
	for _, it := range v.items {
		keys[it.line] = "task:" + it.task
	}
	out := make([]ListItem, 0, len(v.lines))
	for i, l := range v.lines {
		key, ok := keys[i]
		if ok && len(l) > 0 && l[0].Text == "  " {
			l = l[1:] // the list draws its own mark
		}
		out = append(out, ListItem{Key: key, Line: FitLine(l, width), Selectable: ok})
	}
	return out
}

// drawPlanSide draws the side panel inside the frame r, at its right, over
// the list's selection highlight (the list's rows are narrower).
func drawPlanSide(g cellSink, r Rect, side []Line) {
	x, h := r.X+r.W-2-planSideWidth, min(len(side)+2, r.H-2)
	drawFrame(g, x, r.Y+1, planSideWidth, h, Frame{Title: Line{{Text: "Task", Role: RoleText}}, Hue: RoleMuted})
	for i, l := range side {
		if i >= h-2 {
			break
		}
		drawLine(g, x+2, r.Y+2+i, planSideWidth-4, l, false)
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
