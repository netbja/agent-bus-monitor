package main

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/bus"
)

// request-priority §6: + and - on one tracked request, in the Requests tab or
// on its page, write the person's level through the writer, in a command; the
// outcome is said and the next poll reads the board again (busmon never edits
// its own copy).

// priorityMsg is the writer's answer.
type priorityMsg struct {
	task, level string
	changed     bool
	err         error
}

// priorityTarget is the request + and - would set from what the person has
// selected, or why none: a group, a row that is no tracked request, a done
// one. It reads the row's key only (the footer asks it at every frame): a
// single request's row is "req:<task>", a group's key ends in its kind.
func (m *Model) priorityTarget() (request, string) {
	const notRequest = "only a tracked request has a priority"
	key := ""
	if p := m.rd.top(); p != nil {
		if p.kind != ovRequest {
			return request{}, notRequest
		}
		key = p.key
	} else {
		if m.ui.focus != PanelRequests {
			return request{}, notRequest
		}
		key = m.lists.of(PanelRequests).Selected()
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

// setPriority is + (up) or -: one step on the selected tracked request.
func (m *Model) setPriority(up bool) tea.Cmd {
	r, why := m.priorityTarget()
	if why != "" {
		m.ui.flash, m.ui.flashErr = why, true
		return nil
	}
	cur := bus.PriorityLevel(r.Priority)
	next := stepPriority(cur, up)
	if next == cur {
		m.ui.flash, m.ui.flashErr = Preview(r.Task)+": already "+cur, false
		return nil
	}
	w, ctx, self := m.cfg.w, m.cfg.ctx, m.cfg.self
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	task := r.Task
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		changed, err := w.SetRequestPriority(cc, task, next, self)
		return priorityMsg{task: task, level: next, changed: changed, err: err}
	}
}

// gotPriority says the outcome: the level, or the refusal as the bus words it.
func (m *Model) gotPriority(msg priorityMsg) {
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
