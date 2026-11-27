package main

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// request-priority §6: + and - on one tracked request, in the Requests tab or
// on its page, write the person's level through the writer, in a command; the
// outcome is said and the next poll reads the board again (busmon never edits
// its own copy).

// priorityMsg is the writer's answer, at the time it came back.
type priorityMsg struct {
	task, level string
	plan        bool // a plan task's level (views-redesign §4, step 4b), not a request's
	changed     bool
	err         error
	at          time.Time
}

// planPriorityTarget is the plan task + and - would set (views-redesign §4,
// step 4b): the Plan tab's selected task, the drawer's, or a plan task's page;
// ok false when none of those is in front, why when that task cannot take one.
func (m *Model) planPriorityTarget() (task, level, why string, ok bool) {
	switch p := m.rd.top(); {
	case p != nil && p.kind == ovPlan:
		if p.sel < 0 || p.sel >= len(p.items) || p.items[p.sel].task == "" {
			return "", "", "only a plan task has a priority", true
		}
		task = p.items[p.sel].task
	case p != nil && p.kind == ovPlanTask:
		task = p.key
	case p == nil && m.ui.focus == PanelPlan:
		t, isTask := strings.CutPrefix(m.lists.plan.Selected(), planTabTask)
		if !isTask {
			return "", "", "only a plan task has a priority", true
		}
		task = t
	default:
		return "", "", "", false
	}
	for _, r := range m.snap.planState.rows {
		if r.Task.ID == task {
			if r.State == plan.Done {
				return "", "", "done: its priority orders nothing", true
			}
			return task, bus.PriorityLevel(r.Task.Priority), "", true
		}
	}
	return "", "", "not a task of the loaded plan", true
}

// prioOverride is a level this busmon wrote, kept until a successful board
// read that began after the write: + and - step from it, never from an older snapshot
// (reviewer-codex: two presses before a poll both stepped from low).
type prioOverride struct {
	level string
	at    time.Time
}

// endPriorityOverrides drops the overrides the board read that began at
// readAt has read past: the snapshot's requests come from it. A failed read
// keeps the older requests and their older time, so it ends none
// (reviewer-codex).
func (m *Model) endPriorityOverrides(readAt time.Time) {
	for task, o := range m.ui.prioSeen {
		if o.at.Before(readAt) {
			delete(m.ui.prioSeen, task)
		}
	}
}

// priorityTarget is the request + and - would set from what the person has
// selected, or why none: a group, a row that is no tracked request, a done
// one. It reads the row's key only (the footer asks it at every frame): a
// single request's row is "req:<task>", a group's key ends in its kind.
func (m *Model) priorityTarget() (request, string) {
	const notRequest = "only a tracked request has a priority"
	key, ok := m.selectedRequestKey()
	if !ok {
		return request{}, notRequest
	}
	if strings.HasPrefix(key, "grp:") && strings.HasSuffix(key, "|"+kindRequest) {
		return request{}, "open the group to set one request's priority"
	}
	task, ok := strings.CutPrefix(key, "req:")
	if !ok {
		return request{}, notRequest
	}
	for _, r := range m.snap.requests {
		if r.Task == task && r.Tracked {
			if r.State == "done" {
				return request{}, "delivered: in review, its priority orders nothing"
			}
			return r, ""
		}
	}
	return request{}, notRequest
}

// selectedRequestKey is the key of the row the person has selected where a
// tracked request can stand: its page, a Requests row, a Work row that
// stands for one (correction 7); ok false elsewhere.
func (m *Model) selectedRequestKey() (string, bool) {
	if p := m.rd.top(); p != nil {
		return p.key, p.kind == ovRequest
	}
	switch m.ui.focus {
	case PanelWork:
		return m.workRequestKey()
	case PanelRequests:
		return m.lists.of(PanelRequests).Selected(), true
	}
	return "", false
}

// stepPriority is the level one step up or down: low, normal, high.
func stepPriority(level string, up bool) string {
	order := []string{bus.PriorityLow, bus.PriorityNormal, bus.PriorityHigh}
	i := 2 - bus.PriorityRank(level) // high 2, normal 1, low 0
	if up {
		i = min(i+1, 2)
	} else {
		i = max(i-1, 0)
	}
	return order[i]
}

// setPriority is + (up) or -: one step on the selected plan task, else on the
// selected tracked request.
func (m *Model) setPriority(up bool) tea.Cmd {
	if task, level, why, ok := m.planPriorityTarget(); ok {
		if why != "" {
			m.ui.flash, m.ui.flashErr = why, true
			return nil
		}
		return m.stepPriorityOf(task, level, true, up)
	}
	r, why := m.priorityTarget()
	if why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	return m.stepPriorityOf(r.Task, bus.PriorityLevel(r.Priority), false, up)
}

// prioKey keys a level this busmon wrote: a plan task's apart from a
// request's of the same name.
func prioKey(task string, isPlan bool) string {
	if isPlan {
		return planTabTask + task
	}
	return task
}

// stepPriorityOf writes one step from level (or from the level this busmon
// last wrote) for a request or a plan task.
func (m *Model) stepPriorityOf(task, level string, isPlan, up bool) tea.Cmd {
	if m.ui.prioBusy {
		m.ui.flash, m.ui.flashErr = "a priority change is on its way: press again once it is said", false
		return nil
	}
	cur := level
	if o, ok := m.ui.prioSeen[prioKey(task, isPlan)]; ok {
		cur = o.level
	}
	next := stepPriority(cur, up)
	if next == cur {
		m.ui.flash, m.ui.flashErr = Preview(task)+": already "+cur, false
		return nil
	}
	w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.ui.prioBusy = true
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		set := w.SetRequestPriority
		if isPlan {
			set = w.SetPlanPriority
		}
		changed, err := set(cc, task, next, self)
		return priorityMsg{task: task, level: next, plan: isPlan, changed: changed, err: err, at: time.Now()}
	}
}

// gotPriority says the outcome: the level, or the refusal as the bus words it.
func (m *Model) gotPriority(msg priorityMsg) {
	m.ui.prioBusy = false
	if msg.err == nil { // the level now stored, whatever the snapshot says until a newer poll
		if m.ui.prioSeen == nil {
			m.ui.prioSeen = map[string]prioOverride{}
		}
		m.ui.prioSeen[prioKey(msg.task, msg.plan)] = prioOverride{level: msg.level, at: msg.at}
	}
	switch {
	case msg.err != nil:
		m.ui.flash, m.ui.flashErr = Preview(msg.task)+": "+Preview(msg.err.Error()), true
	case !msg.changed:
		m.ui.flash, m.ui.flashErr = Preview(msg.task)+": already "+msg.level, false
	default:
		m.ui.flash, m.ui.flashErr = Preview(msg.task)+": "+msg.level, false
	}
}

// priorityLine is a request page's Priority row: the level, who set it and
// when; none for normal.
func priorityLine(r request) string {
	if r.Priority == "" {
		return ""
	}
	line := "Priority: " + bus.PriorityLevel(r.Priority)
	if r.PriorityBy != "" {
		line += " " + GlyphNone + " set by " + Preview(r.PriorityBy)
		if !r.PriorityAt.IsZero() {
			line += " " + r.PriorityAt.Local().Format("15:04")
		}
	}
	return line
}
