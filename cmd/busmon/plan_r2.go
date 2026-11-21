package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The Plan's r2 layout (views-redesign §4.2, the person's target of
// 2026-11-21): one header line and a rule; the groups with open tasks
// expanded, every finished group folded into one line, no blank lines; rows
// in aligned columns (title, owner, state, age), the titles given the width
// left; bars and state words in their state's colour; the selected task's
// detail a strip under a rule at the bottom.

// planStateWord is a row's state word and its colour: orange waits for the
// person, blue in progress (or in review), green approved, grey done, red
// blocked; a task waiting on another reads "after <id>", muted.
func planStateWord(r plan.Row) (string, Role) {
	switch {
	case r.State == plan.Done:
		return "done", RoleMuted
	case r.State == plan.Blocked:
		return "blocked", RoleError
	case r.State == plan.Waiting && r.Task.Owner == plan.Person:
		return "decide", RoleAttention
	case r.State == plan.Waiting:
		return "waits for you", RoleAttention
	case r.State == plan.InReview && r.Verdict == plan.Approved:
		return "approved", RoleOK
	case r.State == plan.InReview, r.State == plan.InProgress, r.State == plan.Answered:
		return plan.Label(r.State), RoleAccent
	case r.State == plan.Upcoming && len(r.WaitingOn) > 0:
		w := "after " + r.WaitingOn[0]
		if len(r.WaitingOn) > 1 {
			w += fmt.Sprintf(" +%d", len(r.WaitingOn)-1)
		}
		return w, RoleMuted
	case r.State == plan.Unknown:
		return "?", RoleMuted
	}
	return plan.Label(r.State), RoleText
}

// planRowGlyph is a row's first cell, in its state word's colour: ✓ when
// approved or done, the pause mark when it waits on another task, else the
// state's own glyph (planLook).
func planRowGlyph(r plan.Row) (string, Role) {
	_, role := planStateWord(r)
	switch {
	case r.State == plan.Done, r.State == plan.InReview && r.Verdict == plan.Approved:
		return GlyphDone, role
	case r.State == plan.Upcoming && len(r.WaitingOn) > 0:
		return GlyphPause, role
	case r.State == plan.Upcoming:
		return " ", role
	}
	g, _ := planLook(r.State)
	return g, role
}

// planR2Cols are the rows' column widths: the widest title, owner and state
// word of the plan, whether any task has a priority (its column), and
// whether any row has an age.
type planR2Cols struct {
	title, owner, state int
	prio                bool
}

func planR2ColsOf(p planState) planR2Cols {
	var c planR2Cols
	for _, r := range p.rows {
		c.title = max(c.title, Width(Preview(r.Task.Title)))
		c.owner = max(c.owner, Width(clip(r.Task.Owner, 14)))
		w, _ := planStateWord(r)
		c.state = max(c.state, Width(w))
		if r.Task.Priority == bus.PriorityHigh || r.Task.Priority == bus.PriorityLow {
			c.prio = true
		}
	}
	return c
}

// planR2Row is one task: the selection mark, its glyph, its priority (when a
// task of the plan has one), its title padded to the column (the width left
// once the other columns are counted), its owner, its state word in colour,
// and how long since its newest fact.
func planR2Row(p planState, r plan.Row, selected bool, now time.Time, width int, c planR2Cols) Line {
	glyph, role := planRowGlyph(r)
	word, wrole := planStateWord(r)
	if p.kept(r) {
		word += " ?" // derived from a value kept after a failed read
	}
	head := append(planMark(selected), Seg{Text: glyph + " ", Role: role})
	if c.prio {
		mark := "  "
		switch r.Task.Priority {
		case bus.PriorityHigh:
			mark = GlyphUp + " "
		case bus.PriorityLow:
			mark = GlyphDownArrow + " "
		}
		head = append(head, Seg{Text: mark, Role: RoleAccent})
	}
	age := ""
	if r.LastFact > 0 && r.State != plan.Done {
		age = plan.Age(r.LastFact, now)
	}
	tail := Line{{Text: "  " + padRight(clip(r.Task.Owner, 14), c.owner), Role: RoleMuted},
		{Text: "  " + padRight(word, c.state), Role: wrole}}
	if age != "" {
		tail = append(tail, Seg{Text: "  " + padLeft(age, 4), Role: RoleMuted})
	}
	titleW := max(8, min(c.title, width-head.Width()-tail.Width()-2))
	title := Seg{Text: padRight(clip(Preview(r.Task.Title), titleW), titleW), Role: RoleText}
	if hand, _, _ := planInHand(p); hand != nil && hand.Task.ID == r.Task.ID { // the task in hand (progress-line §2.4)
		title.Role, title.Bold = RoleAccent, true
	}
	return FitLine(append(append(head, title), tail...), width)
}

// planRule is a rule across the width, muted.
func planRule(width int) Line {
	return Line{{Text: strings.Repeat(glyphHorizontal, max(0, width)), Role: RoleMuted}}
}

// planOverallRole is the plan's state as a whole, by the group rule over
// every open task.
func planOverallRole(p planState) Role {
	var open []plan.Row
	for _, r := range p.rows {
		if r.State != plan.Done {
			open = append(open, r)
		}
	}
	_, role := planGroupState(open)
	return role
}

// planHeader is the board's one header line: the plan, a bar in the plan's
// state colour, done/total, then what is left and what is for the person;
// stale or partial, it says so. The count, those markers and a bar (4 cells
// at least) stay at every width, the narrow tier's included
// (reviewer-codex): a long name is clipped first, then the markers
// shorten, then what is left goes.
func planHeader(p planState, now time.Time, width int, tier Tier) Line {
	done, total := plan.Count(p.rows)
	count := fmt.Sprintf("%d/%d", done, total)
	if p.partial {
		count += "?"
	}
	forYou := 0 // what waits for the person, and the person's own open tasks
	for _, r := range p.rows {
		if r.State == plan.Waiting || (r.Task.Owner == plan.Person && r.State != plan.Done) {
			forYou++
		}
	}
	rest := fmt.Sprintf("%d left %s %d for you", total-done, GlyphNone, forYou)
	stale := p.rec.LoadedAt > 0 && now.Sub(time.UnixMilli(p.rec.LoadedAt)) > plan.StaleAfter
	marks := func(long bool) string {
		var m []string
		if stale {
			m = append(m, map[bool]string{true: "stale: master, agentbus plan load", false: "stale"}[long])
		}
		if p.partial {
			m = append(m, map[bool]string{true: "a read failed: values kept", false: "read failed"}[long])
		}
		return strings.Join(m, " "+GlyphNone+" ")
	}
	pct := 0.0
	if total > 0 {
		pct = float64(done) * 100 / float64(total)
	}
	minBar, maxBar := 6, 40
	if tier == TierNarrow {
		minBar, maxBar = 4, 12
	}
	// The name gives way first (reviewer-codex): the bar, the count and the
	// short markers keep their room, the name is clipped to what is left.
	reserve := 2 + minBar + 2 + Width(count)
	if m := marks(false); m != "" {
		reserve += 3 + Width(m)
	}
	name := Seg{Text: Truncate(Preview(p.file.Plan), max(1, width-reserve)), Role: RoleText, Bold: true}
	sep := " " + GlyphNone + " "
	build := func(barW int, tail string) Line {
		l := Line{name, {Text: "  ", Role: RoleMuted}}
		l = append(l, planBar(pct, barW, planOverallRole(p))...)
		l = append(l, Seg{Text: "  " + count, Role: RoleText})
		if tail != "" {
			l = append(l, Seg{Text: "   " + tail, Role: RoleMuted})
		}
		return l
	}
	join := func(parts ...string) string {
		var out []string
		for _, s := range parts {
			if s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, sep)
	}
	for _, tail := range []string{join(rest, marks(true)), join(rest, marks(false)), marks(false)} {
		fixed := build(0, tail).Width()
		if barW := min(width-fixed, maxBar); barW >= minBar {
			return FitLine(build(barW, tail), width)
		}
	}
	return FitLine(build(minBar, marks(false)), width)
}

// planGroupState is a group's state word and colour, from its open tasks:
// one waits for the person (orange), else one is blocked (red), else every
// one is approved (green), else one is in progress or in review (blue), else
// nothing has started (the text colour); no open task: done (grey).
func planGroupState(open []plan.Row) (string, Role) {
	if len(open) == 0 {
		return "done", RoleMuted
	}
	has := func(f func(plan.Row) bool) bool {
		for _, r := range open {
			if f(r) {
				return true
			}
		}
		return false
	}
	switch {
	case has(func(r plan.Row) bool { return r.State == plan.Waiting }):
		return "waits for you", RoleAttention
	case has(func(r plan.Row) bool { return r.State == plan.Blocked }):
		return "blocked", RoleError
	case !has(func(r plan.Row) bool { return !(r.State == plan.InReview && r.Verdict == plan.Approved) }):
		return "approved", RoleOK // every open task approved: only the merge left
	case has(func(r plan.Row) bool {
		return r.State == plan.InProgress || r.State == plan.Answered || r.State == plan.InReview
	}):
		return "in progress", RoleAccent
	}
	return "not started", RoleText
}

// planGroupLine is a group's line: ▼, its title padded to the widest, a
// bar of its done tasks in its state's colour, done/total and its state
// word in that colour. Narrow: the title, the count and the word.
func planGroupLine(g planGroup, titleW, width int, tier Tier) Line {
	done, total := len(g.done), len(g.open)+len(g.done)
	word, role := planGroupState(g.open)
	title := Preview(g.title)
	count := fmt.Sprintf("%d/%d", done, total)
	if tier == TierNarrow {
		return FitLine(Line{{Text: GlyphDown + " " + title, Role: RoleText, Bold: true}, {Text: " " + count + "  ", Role: RoleMuted}, {Text: word, Role: role}}, width)
	}
	const barW = 12
	fill := 0
	if total > 0 {
		fill = done * barW / total
	}
	return FitLine(Line{{Text: GlyphDown + " " + padRight(title, titleW) + "  ", Role: RoleText, Bold: true},
		{Text: strings.Repeat(GlyphBarFull, fill), Role: role},
		{Text: strings.Repeat(GlyphBarEmpty, barW-fill), Role: RoleDim},
		{Text: "  " + padLeft(count, 5) + "  ", Role: RoleMuted},
		{Text: word, Role: role}}, width)
}

// planGroupBlocks emits the groups with open tasks, each with its open
// rows (and its done rows while d shows them), then every finished group:
// one folded line while d hides them ("▶ N groups done · M tasks"), an item
// Enter opens; shown, each finished group's line and rows. No blank lines.
func planGroupBlocks(p planState, showDone bool, width int, tier Tier, emit func(Line), row func(plan.Row), fold func(string, Line)) {
	groups := planGroupsOf(p)
	titleW := 0
	for _, g := range groups {
		titleW = max(titleW, Width(Preview(g.title)))
	}
	var finished []planGroup
	for _, g := range groups {
		if len(g.open) == 0 {
			finished = append(finished, g)
			continue
		}
		emit(planGroupLine(g, titleW, width, tier))
		for _, r := range g.open {
			row(r)
		}
		if showDone {
			for _, r := range g.done {
				row(r)
			}
		}
	}
	if len(finished) == 0 {
		return
	}
	if !showDone {
		tasks := 0
		for _, g := range finished {
			tasks += len(g.done)
		}
		word := "groups"
		if len(finished) == 1 {
			word = "group"
		}
		fold("done", Line{{Text: fmt.Sprintf("%d %s done %s %s", len(finished), word, GlyphNone, planTasks(tasks)), Role: RoleMuted},
			{Text: "   (d shows)", Role: RoleDim}})
		return
	}
	for _, g := range finished {
		emit(planGroupLine(g, titleW, width, tier))
		for _, r := range g.done {
			row(r)
		}
	}
}

// planStrip is the selected task's detail at the bottom (planDetailRows
// lines, full width): a rule, then its facts on one line (id, state,
// owner, how a done one ended, review, PR, dependencies, reason, priority),
// then what the person
// may do and the keys (planStripKeys).
func planStrip(p planState, r plan.Row, now time.Time, width int) []Line {
	sep := Seg{Text: " " + GlyphNone + " ", Role: RoleMuted}
	_, role := planStateWord(r)
	l := Line{{Text: Preview(r.Task.ID), Role: RoleText, Bold: true}, sep, {Text: plan.Label(r.State), Role: role}}
	if r.Task.Owner != "" {
		l = append(l, sep, Seg{Text: Preview(r.Task.Owner), Role: RoleText})
	}
	if r.State == plan.Done { // how it ended: merged, recorded and when
		l = append(l, sep, Seg{Text: Preview(plan.DoneLabel(r, now)), Role: RoleMuted})
	}
	var forYou string
	for _, f := range planFacts(p, r) {
		switch f.key {
		case "for you":
			forYou = f.val
			continue
		case "state", "owner":
			continue
		}
		if f.val == GlyphDash || (f.key == "prio" && f.val == "normal") {
			continue
		}
		text := f.key + " " + f.val
		switch f.key {
		case "review", "reason":
			text = f.val
		case "PR":
			text = "PR " + f.val
		}
		l = append(l, sep, Seg{Text: Preview(text), Role: RoleText})
	}
	return []Line{planRule(width), FitLine(l, width), planStripKeys(forYou, width)}
}

// planStripKeys is the strip's last line: what the person may do on the
// task, then the keys, said once in the frame (plan-tab-r3 item 3); forYou
// "" (no task selected) leaves the keys alone.
func planStripKeys(forYou string, width int) Line {
	var l Line
	if forYou != "" {
		l = Line{{Text: "for you: ", Role: RoleMuted}, {Text: Preview(forYou), Role: RoleText}, {Text: "        ", Role: RoleMuted}}
	}
	l = append(l, Seg{Text: "enter", Role: RoleAccent, Bold: true}, Seg{Text: ": task page   ", Role: RoleMuted},
		Seg{Text: "+ -", Role: RoleAccent, Bold: true}, Seg{Text: " priority   ", Role: RoleMuted},
		Seg{Text: "d", Role: RoleAccent, Bold: true}, Seg{Text: " done", Role: RoleMuted})
	return FitLine(l, width)
}

// planBar is a bar of cells: full blocks, one partial eighth, then the dim
// ░ track (the person's mock-up), the filled part in role.
func planBar(pct float64, cells int, role Role) Line {
	eighths := clamp(int(pct*float64(cells)*8/100+0.5), 0, cells*8)
	full, part := eighths/8, eighths%8
	partial := []string{"", GlyphEighth1, GlyphEighth2, GlyphEighth3, GlyphEighth4, GlyphEighth5, GlyphEighth6, GlyphEighth7}[part]
	empty := cells - full
	if partial != "" {
		empty--
	}
	return Line{{Text: strings.Repeat(GlyphBarFull, full) + partial, Role: role}, {Text: strings.Repeat(GlyphBarEmpty, max(0, empty)), Role: RoleDim}}
}

// planTasks is "1 task" or "N tasks".
func planTasks(n int) string {
	if n == 1 {
		return "1 task"
	}
	return fmt.Sprintf("%d tasks", n)
}
