package main

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/bus"
)

// request-priority §6: + and - on one tracked request, in the Requests tab or
// on its page, write the person's level through the writer, in a command; the
// outcome is said and the next poll reads the board again (busmon never edits
// its own copy).

// priorityMsg is the writer's answer, at the time it came back.
type priorityMsg struct {
	task, level string
	changed     bool
	err         error
	at          time.Time
}

// prioOverride is a level this busmon wrote, kept until a poll that started
// after the write: + and - step from it, never from an older snapshot
// (reviewer-codex: two presses before a poll both stepped from low).
type prioOverride struct {
	level string
	at    time.Time
}

// endPriorityOverrides drops the overrides a poll that started at started
// has read past.
func (m *Model) endPriorityOverrides(started time.Time) {
	for task, o := range m.ui.prioSeen {
		if o.at.Before(started) {
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
	if m.ui.prioBusy {
		m.ui.flash, m.ui.flashErr = "a priority change is on its way: press again once it is said", false
		return nil
	}
	cur := bus.PriorityLevel(r.Priority)
	if o, ok := m.ui.prioSeen[r.Task]; ok {
		cur = o.level
	}
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
	m.ui.prioBusy = true
	return func() tea.Msg {
		cc, cancel := context.WithTimeout(ctx, pollBudget)
		defer cancel()
		changed, err := w.SetRequestPriority(cc, task, next, self)
		return priorityMsg{task: task, level: next, changed: changed, err: err, at: time.Now()}
	}
}

// gotPriority says the outcome: the level, or the refusal as the bus words it.
func (m *Model) gotPriority(msg priorityMsg) {
	m.ui.prioBusy = false
	if msg.err == nil { // the level now stored, whatever the snapshot says until a newer poll
		if m.ui.prioSeen == nil {
			m.ui.prioSeen = map[string]prioOverride{}
		}
		m.ui.prioSeen[msg.task] = prioOverride{level: msg.level, at: msg.at}
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
