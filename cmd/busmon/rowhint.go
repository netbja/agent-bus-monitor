package main

import "strings"

// rowNoPair is rowHint's answer for a row whose Enter does nothing: the
// footer draws no enter pair there, so it never offers what Enter does not do
// (busmon-queue-triage §5.1, merge:unknown).
const rowNoPair = "\x00"

// rowHint is the footer's word for Enter on the selected row of panel p
// (busmon-queue-triage §5.1, busmon-row-actions), from the row's key and,
// for a row Enter changes in place, whether it is open (it.Expanded): the
// facts Enter itself reads. It names the page Enter opens, never an act the
// person then does there (an ask's "answer" is its page, the answer form);
// an in-place row's word names the change; rowNoPair where Enter does
// nothing (merge:unknown, or no row selected). known is false for a key outside the table, which keeps the
// binding's own word.
func rowHint(p PanelID, it ListItem) (word string, known bool) {
	key := it.Key
	if key == "" { // no row selected: Enter does nothing, no pair (the person's decision, 2026-11-07)
		return rowNoPair, true
	}
	inPlace := func(open, closed string) (string, bool) {
		if it.Expanded {
			return open, true
		}
		return closed, true
	}
	has := func(prefix string) bool { return strings.HasPrefix(key, prefix) }
	switch p {
	case PanelForYou:
		switch {
		case has("ask:"):
			return "answer", true
		case has("wait:"), has("prompt:"):
			return "agent", true
		case has("gate:"):
			return "thread", true
		case has(planNextPrefix):
			return "task", true
		case key == mergeUnknownKey:
			return rowNoPair, true
		case has("merge:"):
			return "PR", true
		case has("health:"):
			return "health", true
		}
	case PanelActivity:
		if key != "" {
			return inPlace("full text", "expand")
		}
	case PanelRequests:
		switch {
		case key == "asks", has("ask:"):
			return "answer", true
		case has("req:"):
			return "request", true
		case has("thr:"):
			return "thread", true
		case key == "fold", key == reqNoticesKey, has("grp:"), has("older:"):
			return inPlace("fold", "unfold")
		case has("ntc:"):
			return "notices", true
		}
	case PanelAgents:
		if key != "" {
			return "agent", true
		}
	case PanelBoard:
		if key != "" {
			return "task", true
		}
	case PanelPlan:
		switch {
		case has("task:"):
			return "task", true
		case key == "toggle":
			return "expand", true
		}
	}
	return "", false
}

// selectedItem is the selected row of list p (the zero item when none is).
func (m Model) selectedItem(p PanelID) ListItem {
	l := m.lists.of(p)
	sel := l.Selected()
	for _, it := range l.items {
		if it.Key == sel && sel != "" {
			return it
		}
	}
	return ListItem{}
}

// shellHint is the footer's word on the main screen (ModeShell only, never
// the picker's or a text mode's): Enter's on the selected row (rowHint),
// else the binding's own.
func (m Model) shellHint(act Action) string {
	if act != ActOpen || m.mode() != ModeShell {
		return ""
	}
	w, _ := rowHint(m.ui.focus, m.selectedItem(m.ui.focus))
	return w
}
