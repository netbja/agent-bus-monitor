package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/plan"
)

// The Plan tab (views-redesign §2, §4): the lower panel's fourth tab (4; 6 at
// the narrow tier), the roadmap planBoardOf draws for the drawer, as a list
// like Board's. Its frame says the plan and its progress, never a for-you
// count; at the wide tier the selected task's detail is drawn at its
// bottom, under the list (views-redesign §4.1). p and P keep opening the
// drawer.

const (
	planTabTask = "plan:" // a task's row: the prefix, then its id
	planTabFold = "fold:" // a folded section's line: "later" or "done"
	planGaugeW  = 18
)

// planTabWidths is the list's row width, the whole of it, and whether the
// selected task's detail goes under the list: the wide tier (§4.1); the
// drawer's rule too (planDrawerWidths).
func planTabWidths(lower int, tier Tier) (list int, detail bool) {
	return lower, tier == TierWide
}

// fillPlanTab fills the tab's list from the plan board; lower is the row
// width (rowW of the lower frame).
func (m *Model) fillPlanTab(lower int) {
	s, l := &m.snap, &m.lists.plan
	title := Line{{Text: "Plan", Role: RoleText}}
	if !s.planState.loaded {
		l.SetItems([]ListItem{{Line: Line{{Text: "no plan loaded (master: agentbus plan load <path>@<commit>)", Role: RoleMuted}}}})
		l.SetFrame(m.lowerTitle(PanelPlan, title), nil)
		l.SetFooter(nil)
		return
	}
	title = append(title, Seg{Text: " " + GlyphNone + " " + Preview(s.planState.file.Plan), Role: RoleMuted})
	lw, _ := planTabWidths(lower, m.layout.Tier)
	v := planBoardOf(s.planState, -1, m.ui.planDone, m.ui.planLater, m.now, lw+listGutter, m.layout.Tier, false, s.requests)
	at := map[int]planItem{}
	for _, it := range v.items {
		at[it.line] = it
	}
	var items []ListItem
	for i := v.head; i < len(v.lines); i++ {
		ln := v.lines[i]
		it, ok := at[i]
		if !ok {
			if ln == nil && len(items) == 0 {
				continue // the frame's top border is the first section's space
			}
			items = append(items, ListItem{Line: trimPad(ln)}) // drawn after the gutter: its padding would spill past the frame
			continue
		}
		key := planTabTask + it.task
		if it.fold != "" {
			key = planTabFold + it.fold
		}
		items = append(items, ListItem{Key: key, Line: ln[1:], Selectable: true}) // the list draws its own mark: planMark's cell goes
	}
	l.SetItems(items)
	if l.Selected() == "" { // as the drawer: the first task selected, its detail shown
		l.First()
	}
	if _, detail := planTabWidths(lower, m.layout.Tier); detail { // the detail's rows, under the list (§4.1)
		l.SetBottom(planDetailRows)
	} else {
		l.SetBottom(0)
	}
	l.SetFrame(m.lowerTitle(PanelPlan, title), planTabLabel(s.planState, m.layout.Tier))
	hint := "+ - priority " + GlyphNone + " enter task" // the mock-ups' footer
	if m.layout.Tier == TierWide {
		hint = "+ - priority " + GlyphNone + " d done " + GlyphNone + " enter task"
	}
	l.SetFooter(Line{{Text: hint, Role: RoleMuted}})
}

// planTabDetail is the detail of the tab's selected task, width cells wide,
// nil without one (below the wide tier, a fold or no task selected). View
// reads it, so it follows the selection between two fills.
func (m *Model) planTabDetail(width int) []Line {
	if _, wide := planTabWidths(width, m.layout.Tier); !wide || !m.snap.planState.loaded {
		return nil
	}
	task, ok := strings.CutPrefix(m.lists.plan.Selected(), planTabTask)
	if !ok {
		return nil
	}
	for _, r := range m.snap.planState.rows {
		if r.Task.ID == task {
			return planDetail(m.snap.planState, r, width)
		}
	}
	return nil
}

// planTabLabel is the tab's progress: done/total and, at the wide tier, a
// gauge (the mock-ups).
func planTabLabel(p planState, tier Tier) Line {
	done, total := plan.Count(p.rows)
	count := fmt.Sprintf("%d/%d", done, total)
	if p.partial {
		count += "?"
	}
	l := Line{{Text: count + " done", Role: RoleMuted}}
	if tier != TierWide || total == 0 {
		return l
	}
	return append(append(l, Seg{Text: " ", Role: RoleMuted}), gaugeSegs(float64(done)*100/float64(total), planGaugeW, RoleDone, false)...)
}

// openPlanRow is Enter on the tab: a task's page, or a folded section opened.
func (m *Model) openPlanRow(key string) tea.Cmd {
	if task, ok := strings.CutPrefix(key, planTabTask); ok {
		return m.push(page{kind: ovPlanTask, key: task})
	}
	switch strings.TrimPrefix(key, planTabFold) {
	case "later":
		m.ui.planLater = true
	case "done":
		m.ui.planDone = true
	default:
		return nil
	}
	m.refresh()
	return nil
}

// trimPad is l without its trailing spaces: a line padded to the board's
// width, drawn narrower, keeps its words and loses no cell to a "…".
func trimPad(l Line) Line {
	out := append(Line(nil), l...)
	for len(out) > 0 {
		last := &out[len(out)-1]
		last.Text = strings.TrimRight(last.Text, " ")
		if last.Text != "" {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}
