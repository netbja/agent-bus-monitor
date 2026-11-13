package main

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The Work tab's actions (requests-plan-redesign, layout A): enter on a row,
// y, key 4's alias, d and the decide and merge chips.

// openWorkRow is enter on a Work row: each kind opens the page its own panel
// would (For you's for what waits on the person, the request or thread page,
// the plan task, the agent), an idea says where it lives, and the folded
// lines open the traffic between agents or the plan with its done tasks.
func (m *Model) openWorkRow(key string) tea.Cmd {
	switch {
	case key == workFoldBetween:
		m.focusPanel(PanelRequests)
		return nil
	case key == workFoldDone:
		return m.openPlanDone()
	case strings.HasPrefix(key, workAgentPrefix):
		return m.pushAgent(strings.TrimPrefix(key, workAgentPrefix))
	case strings.HasPrefix(key, workIdeaPrefix):
		return m.pushIdea(strings.TrimPrefix(key, workIdeaPrefix))
	case strings.HasPrefix(key, "plan:"):
		return m.push(page{kind: ovPlanTask, key: strings.TrimPrefix(key, "plan:")})
	case strings.HasPrefix(key, "req:"), strings.HasPrefix(key, "thr:"):
		return m.openRequestRow(key)
	}
	if i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Key == key }); i >= 0 {
		return m.openForYouItem(m.forYou[i])
	}
	return nil
}

// openPlanDone is d on Work and enter on its done line: the plan's drawer
// with DONE shown.
func (m *Model) openPlanDone() tea.Cmd {
	if !m.snap.planState.loaded {
		m.ui.flash, m.ui.flashErr = "no plan loaded (master: agentbus plan load <path>@<commit>)", false
		return nil
	}
	return m.push(page{kind: ovPlan, drawer: true, expanded: true})
}

// pushIdea is an idea's page (Work's IDEAS, F6's enter): its facts, text
// and changes, rebuilt at each poll; s sets its state, y copies its title.
func (m *Model) pushIdea(id string) tea.Cmd {
	for _, it := range m.snap.ideas {
		if it.ID == id {
			return m.push(page{kind: ovIdea, key: id})
		}
	}
	return nil
}

// workCopy is y on a Work row: its text, an idea's title, an agent's name.
func (m *Model) workCopy(key string) (text, what string) {
	if it, ok := m.work.itemOf(key); ok {
		return it.Text, "text"
	}
	for _, it := range m.work.Ideas {
		if workIdeaPrefix+it.ID == key {
			return it.Title, "title"
		}
	}
	if a, ok := strings.CutPrefix(key, workAgentPrefix); ok {
		if st := m.snap.agents[a]; st != nil {
			return agentDetailPlain(st), "status message"
		}
	}
	return "", ""
}

// focusWorkNext is key 4 (6 at the narrow tier): Work, its first NEXT row,
// else NEXT's heading in view.
func (m *Model) focusWorkNext() {
	m.focusPanel(PanelWork)
	m.refresh()
	if len(m.work.Next) > 0 {
		m.lists.work.Select(m.work.Next[0].Key)
		return
	}
	m.lists.work.ClearSelection()
}

// workRequest is the tracked request a Work row stands for (+ and -), with
// why not when there is none: only a tracked request has a priority, and
// the footer says so (correction 7).
func (m *Model) workRequestKey() (string, bool) {
	key := m.lists.of(PanelWork).Selected()
	if it, ok := m.work.itemOf(key); ok && it.Req != nil {
		return "req:" + it.Req.Task, true
	}
	return "", false
}

// selectWork focuses Work on its first YOU row that ok accepts: the chips'
// click when For you lists no such row (the person's plan tasks and the
// requests owed are counted, not For you rows; foureyes).
func (m *Model) selectWork(ok func(workItem) bool) bool {
	for _, it := range m.work.You {
		if ok(it) {
			m.focusPanel(PanelWork)
			m.refresh()
			m.lists.work.Select(it.Key)
			return true
		}
	}
	return false
}
