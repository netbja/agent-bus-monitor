package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
)

// planTabWidths is the list's row width, the whole of it, and whether the
// selected task's detail goes under the list: the wide tier (§4.1); the
// drawer's rule too (planDrawerWidths).
func planTabWidths(lower int, tier Tier) (list int, detail bool) {
	return lower, tier != TierNarrow // the strip at 80 columns too (views-redesign §4.2)
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
	items := []ListItem{{}} // one blank row under the tab bar (plan-tab-r3 item 2)
	for i := v.head; i < len(v.lines); i++ {
		ln := v.lines[i]
		it, ok := at[i]
		if !ok {
			if ln == nil && len(items) == 1 {
				continue // the blank row is the first section's space
			}
			items = append(items, ListItem{Line: ln, Wide: true}) // the board's own line, from the gutter: its header, rules and groups (views-redesign §4.2)
			continue
		}
		if it.fold != "" { // a fold carries its own ▶, from the gutter as in the drawer
			items = append(items, ListItem{Key: planTabFold + it.fold, Line: ln, Selectable: true, Wide: true})
			continue
		}
		items = append(items, ListItem{Key: planTabTask + it.task, Line: dropCells(ln, listGutter), Selectable: true}) // the list draws its own mark: planMark's two cells go (cells, not a segment: FitLine merges a blank glyph into them)
	}
	l.SetItems(items)
	if l.Selected() == "" { // as the drawer: the first task selected, its detail shown
		l.First()
	}
	l.SetFrame(m.lowerTitle(PanelPlan, title), nil)               // the counts and the bar are the header line's (views-redesign §4.2)
	if _, detail := planTabWidths(lower, m.layout.Tier); detail { // the detail's rows, under the list (§4.1), the keys their last (plan-tab-r3 item 3)
		l.SetBottom(planDetailRows)
		l.SetFooter(nil)
		return
	}
	l.SetBottom(0)
	l.SetFooter(Line{{Text: "+ - priority " + GlyphNone + " d done " + GlyphNone + " enter task", Role: RoleMuted}}) // no strip: the keys on the frame
}

// planTabDetail is the detail of the tab's selected task, width cells wide:
// with a fold or no task selected, the rule and the keys alone; nil at the
// narrow tier. View reads it, so it follows the selection between two fills.
func (m *Model) planTabDetail(width int) []Line {
	if _, wide := planTabWidths(width, m.layout.Tier); !wide || !m.snap.planState.loaded {
		return nil
	}
	if task, ok := strings.CutPrefix(m.lists.plan.Selected(), planTabTask); ok {
		for _, r := range m.snap.planState.rows {
			if r.Task.ID == task {
				return planStrip(m.snap.planState, r, m.now, width)
			}
		}
	}
	return []Line{planRule(width), nil, planStripKeys("", width)} // a fold selected: the rule and the keys stay
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
