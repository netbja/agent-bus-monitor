package main

import (
	"github.com/netbja/agent-bus-monitor/plan"
)

// The plan board by chantier (spec docs/superpowers/specs/2026-11-15-plan-chantier-design.md
// §3): a plan with groups draws one block per group instead of NOW, NEXT
// and LATER. Each block is a header (the title, a bar whose cells show done
// then approved-and-waiting-for-a-merge, done/total, the task in hand and
// the one waiting for the person) and the group's open rows; done rows are
// folded into one DONE line until d shows them under their group. A plan
// without groups is drawn as before.

// planGroup is one block: its title and rows, open (in board order) and done.
type planGroup struct {
	title      string
	open, done []plan.Row
	byFile     []plan.Row // the open rows in file order, for the task in hand (planInHand's order)
}

// planGroupsOf are the plan's blocks in drawing order: a group with a row
// waiting for the person first, then one with a task in hand, then the
// rest, each in the groups list's order; the tasks with no group (Other,
// even when all are done) last before the finished groups.
func planGroupsOf(p planState) []planGroup {
	byID := map[string]*planGroup{}
	var listed []*planGroup
	for _, g := range p.file.Groups {
		pg := &planGroup{title: g.Title}
		byID[g.ID] = pg
		listed = append(listed, pg)
	}
	other := &planGroup{title: "Other"}
	groupOf := func(r plan.Row) *planGroup {
		if g := byID[r.Task.Group]; g != nil {
			return g
		}
		return other
	}
	for _, r := range p.rows {
		if r.State != plan.Done {
			g := groupOf(r)
			g.byFile = append(g.byFile, r)
		}
	}
	// Each group's rows in the board's order: NOW by plan.Order, then NEXT,
	// then LATER (planSections), done kept apart.
	secs, _ := planSections(p.rows)
	for _, sec := range secs {
		for _, r := range sec.rows {
			g := groupOf(r)
			if r.State == plan.Done {
				g.done = append(g.done, r)
			} else {
				g.open = append(g.open, r)
			}
		}
	}
	waits := func(g *planGroup) bool { return firstIn(g.open, plan.Waiting) != nil }
	inHand := func(g *planGroup) bool { return groupInHand(g.byFile) != nil }
	var out []planGroup
	for _, pick := range []func(*planGroup) bool{
		waits,
		func(g *planGroup) bool { return !waits(g) && inHand(g) },
		func(g *planGroup) bool { return !waits(g) && !inHand(g) && len(g.open) > 0 },
	} {
		for _, g := range listed {
			if pick(g) {
				out = append(out, *g)
			}
		}
	}
	if len(other.open)+len(other.done) > 0 { // Other last before the finished groups, finished or not
		out = append(out, *other)
	}
	for _, g := range listed {
		if len(g.open) == 0 {
			out = append(out, *g)
		}
	}
	return out
}

// firstIn is the first row in state s, or nil.
func firstIn(rows []plan.Row, s plan.State) *plan.Row {
	for i := range rows {
		if rows[i].State == s {
			return &rows[i]
		}
	}
	return nil
}

// groupInHand is planInHand's rule on a group's rows: the first in progress
// or in review, else the first waiting for the person, else the first
// blocked.
func groupInHand(rows []plan.Row) *plan.Row {
	for _, states := range [][]plan.State{{plan.InProgress, plan.InReview}, {plan.Waiting}, {plan.Blocked}} {
		for i := range rows {
			if indexOfState(states, rows[i].State) >= 0 {
				return &rows[i]
			}
		}
	}
	return nil
}
