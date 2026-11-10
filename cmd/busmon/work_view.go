package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"
)

// The Work tab (requests-plan-redesign, layout A; the person's answer of
// 2026-11-10): tab 3, in place of Requests and Plan. It draws the Work model
// (work_model.go) top-down, by who owes what: YOU, TEAM, NEXT, IDEAS, then
// two folded lines, the traffic between agents (enter: today's Requests
// list) and what is done (enter: the plan's drawer with DONE shown).

// Row keys of the Work tab besides the model's own.
const (
	workAgentPrefix = "agent:"         // a TEAM row: enter opens the agent
	workIdeaPrefix  = "idea:"          // an IDEAS row: enter says where it lives
	workFoldBetween = "between-agents" // enter: the Requests list, as a sub-view of Work
	workFoldDone    = "plan-done"      // enter: the plan's drawer, DONE shown
	workNextHead    = "head:next"      // NEXT's heading: where key 4 lands when NEXT is empty
	workCompactW    = 100              // below this many cells, the compact rows (the 80-column view)
	workWhyW        = 38               // the wide rows' "why it waits on you" column
	workHeadPrefix  = "head:"          // a section heading: never selectable
)

// workGlyph is a YOU row's mark by its key: the For you row's own.
func workGlyph(it workItem) (string, Role) {
	switch {
	case strings.HasPrefix(it.Key, "merge:"), strings.HasPrefix(it.Key, "gate:"):
		return GlyphGate, RoleDispatch
	case strings.HasPrefix(it.Key, "health:"):
		return GlyphBlocked, RoleAttention
	case strings.HasPrefix(it.Key, "ask:"), strings.HasPrefix(it.Key, "req:"), strings.HasPrefix(it.Key, "thr:"):
		return "?", RoleAttention
	}
	return GlyphHuman, RoleAttention // a waiting agent, its prompt, a plan task of the person's
}

// workWhy says why a YOU row waits on the person.
func workWhy(it workItem) string {
	switch {
	case strings.HasPrefix(it.Key, "ask:"):
		return "an ask: answer it in the reader"
	case strings.HasPrefix(it.Key, "wait:"):
		return "the agent waits on your answer"
	case strings.HasPrefix(it.Key, "prompt:"):
		return "its pane asks you something"
	case strings.HasPrefix(it.Key, "req:"):
		return "a request to you: accept or answer it"
	case strings.HasPrefix(it.Key, "thr:"):
		return "the sender waits on your answer"
	case strings.HasPrefix(it.Key, "merge:"):
		return "both approved the pushed head: merge it"
	case strings.HasPrefix(it.Key, "gate:"):
		return "a review that concerns you"
	case strings.HasPrefix(it.Key, "health:"):
		return "the stall guard: F5 says why"
	case strings.HasPrefix(it.Key, "plan:"):
		return "a plan task of yours: decide it"
	}
	return ""
}

// workWho is a row's owner as the page names them: you for the person,
// marked in NEXT, where the others are agents (YOU's mark says it already).
func workWho(who string, marked bool) string {
	if who == plan.Person || who == "you" {
		if marked {
			return GlyphHuman + " you"
		}
		return "you"
	}
	return who
}

// gateAge is a gate's age, "" when its ref is no stream id.
func gateAge(since, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	return humanAge(now.Sub(since))
}

// workLabel is the frame's label: the four counts, worded at the wide sizes
// and as glyphs in the compact view (correction 6).
func workLabel(w workModel, compact bool) string {
	if compact {
		return fmt.Sprintf("%s%d %s%d %s%d %s%d", GlyphHuman, w.youCount(), GlyphWorking, len(w.Team), GlyphIdle, len(w.Next), GlyphIdea, len(w.Ideas))
	}
	return fmt.Sprintf("%d for you %s %d at work %s %d next %s %d ideas", w.youCount(), GlyphNone, len(w.Team), GlyphNone, len(w.Next), GlyphNone, len(w.Ideas))
}

// workDraw is what the rows read besides the model.
type workDraw struct {
	requests []request // for each owner's pace (the progress line)
	now      time.Time
	width    int
}

// workRows are the tab's rows in width cells.
func workRows(w workModel, d workDraw) []ListItem {
	compact := d.width < workCompactW
	var out []ListItem
	add := func(key string, l Line, sel bool) {
		out = append(out, ListItem{Key: key, Line: FitLine(l, d.width), Selectable: sel})
	}
	head := func(key, name string, n int, note string) {
		l := Line{{Text: fmt.Sprintf("%s  %d", name, n), Role: RoleText, Bold: true}}
		if note != "" {
			l = append(l, Seg{Text: "   " + note, Role: RoleMuted})
		}
		add(workHeadPrefix+key, l, false)
	}
	age := func(at time.Time) Line {
		if at.IsZero() {
			return nil
		}
		return Line{{Text: " " + humanAge(d.now.Sub(at)), Role: RoleMuted}}
	}

	// YOU
	head("you", "YOU", w.youCount(), "")
	if len(w.You) == 0 {
		add(workHeadPrefix+"you-none", Line{{Text: "  Nothing expected from you.", Role: RoleOK}}, false)
	}
	for _, it := range w.You {
		g, role := workGlyph(it)
		text := Preview(strings.TrimPrefix(it.Text, "merge "))
		lead := Line{col(g, 2, role), col(it.Verb, 7, role), col(Preview(workWho(it.Who, false)), 10, RoleText)}
		if it.Req != nil {
			lead = append(Line{priorityMark(it.Req.Priority)}, lead...)
		} else {
			lead = append(Line{noMark}, lead...)
		}
		a := age(it.At)
		var why Line
		if !compact {
			why = Line{{Text: "  " + Fit(workWhy(it), workWhyW-2), Role: RoleMuted}}
		}
		room := max(0, d.width-lead.Width()-a.Width()-why.Width())
		l := append(lead, FitLine(Line{{Text: text, Role: RoleText}}, room)...) // padded: the age and why line up
		add(it.Key, append(append(l, a...), why...), true)
	}

	// TEAM
	out = append(out, ListItem{})
	paces := ownerPaces(d.requests)
	head("team", "TEAM", len(w.Team), fmt.Sprintf("at work %s %d idle", GlyphNone, len(w.Idle)))
	for _, r := range w.Team {
		what := r.Message
		if r.Task != nil {
			what = r.Task.Task
			if r.Plan != "" {
				what += "  " + GlyphNone + " plan #" + r.Plan
			}
		}
		if len(r.Reviewing) > 0 { // the oldest gate it holds
			g := r.Reviewing[0]
			what = "reviewing " + g.Subject + " for " + g.Who
			if a := gateAge(g.Since, d.now); a != "" {
				what += "  " + GlyphNone + " gate open " + a
			}
			if n := len(r.Reviewing) - 1; n > 0 {
				what += fmt.Sprintf(" (+%d)", n)
			}
		}
		l := Line{noMark, col(GlyphWorking, 2, RoleOK), col(Preview(r.Agent), 16, RoleText)}
		rev := teamReviewers(r, d.now)
		room := max(0, d.width-l.Width()-Width(rev))
		l = append(l, FitLine(Line{{Text: Preview(what), Role: RoleText}}, room)...)
		if rev != "" {
			l = append(l, Seg{Text: rev, Role: RoleDispatch})
		}
		add(workAgentPrefix+r.Agent, l, true)
		if r.Task != nil {
			if p, ok := progressLine(*r.Task, paces[r.Agent], d.now); ok {
				add(workHeadPrefix+"progress:"+r.Agent, append(Line{{Text: strings.Repeat(" ", 4), Role: RoleDim}}, p...), false)
			}
		}
	}
	if len(w.Idle) > 0 { // they can take a request: the line stays, at every width (correction 6)
		l := Line{noMark}
		for i, n := range w.Idle {
			if i > 0 {
				l = append(l, Seg{Text: "   ", Role: RoleMuted})
			}
			l = append(l, Seg{Text: GlyphIdle + " " + Preview(n), Role: RoleMuted})
		}
		add(workHeadPrefix+"idle", append(l, Seg{Text: "   idle, can take a request", Role: RoleMuted}), false)
	}

	// NEXT
	out = append(out, ListItem{})
	head("next", "NEXT", len(w.Next), "the plan, then requests not in it")
	for _, it := range w.Next {
		var where string
		switch {
		case it.Plan != "" && len(it.After) > 0:
			where = "plan #" + it.Plan + " " + GlyphNone + " waits on " + strings.Join(it.After, ", ")
		case it.Plan != "":
			where = "plan #" + it.Plan
		default:
			where = "request " + GlyphNone + " requested " + humanAge(d.now.Sub(it.At)) + " " + GlyphNone + " not in the plan"
			if it.At.IsZero() {
				where = "request " + GlyphNone + " not in the plan"
			}
		}
		mark := noMark
		if it.Req != nil {
			mark = priorityMark(it.Req.Priority)
		}
		titleW := 28
		if compact {
			titleW = 24
		}
		l := Line{mark, col(GlyphIdle, 2, RoleMuted), col(Preview(it.Text), titleW, RoleText), col(Preview(workWho(it.Who, true)), 14, RoleText),
			{Text: where, Role: RoleMuted}}
		add(it.Key, l, true)
	}

	// IDEAS
	out = append(out, ListItem{})
	head("ideas", "IDEAS", len(w.Ideas), "not decided yet")
	if w.IdeasErr {
		add(workHeadPrefix+"ideas-err", Line{{Text: "  the open questions could not be read: the list may be old", Role: RoleAttention}}, false)
	}
	for _, it := range w.Ideas {
		l := Line{noMark, col(GlyphIdea, 2, RoleAccent)}
		if compact {
			l = append(l, Seg{Text: Preview(it.Title), Role: RoleText})
		} else {
			l = append(l, col(Preview(it.Title), 50, RoleText), Seg{Text: it.Where, Role: RoleMuted})
		}
		add(workIdeaPrefix+it.ID, l, true)
	}

	// the folded lines
	out = append(out, ListItem{})
	add(workFoldBetween, Line{{Text: fmt.Sprintf("%s between agents %s %d, nobody waits on you  (enter shows them) %s", GlyphDash, GlyphNone, w.Between, GlyphDash), Role: RoleMuted}}, true)
	if w.Planned {
		add(workFoldDone, Line{{Text: fmt.Sprintf("%s done %s %d  (enter or d shows them) %s", GlyphDash, GlyphNone, w.Done, GlyphDash), Role: RoleMuted}}, true)
	} else {
		add(workHeadPrefix+"no-plan", Line{{Text: "no plan loaded (master: agentbus plan load <path>@<commit>)", Role: RoleMuted}}, false)
	}
	return out
}

// teamReviewers is a TEAM row's review column: who reviews this agent's
// work and since when (correction 5); "" when nobody does.
func teamReviewers(r teamRow, now time.Time) string {
	if len(r.Reviewers) == 0 {
		return ""
	}
	parts := make([]string, len(r.Reviewers))
	for i, g := range r.Reviewers {
		parts[i] = strings.TrimSpace(g.Who + " " + gateAge(g.Since, now))
	}
	return "  review: " + strings.Join(parts, ", ")
}

// itemOf is the model's row behind a key (YOU or NEXT), if any.
func (w workModel) itemOf(key string) (workItem, bool) {
	for _, it := range w.You {
		if it.Key == key {
			return it, true
		}
	}
	for _, it := range w.Next {
		if it.Key == key {
			return it, true
		}
	}
	return workItem{}, false
}
