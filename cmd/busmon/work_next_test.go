package main

import "testing"

// workNext is Work focused on its first NEXT row (what key 4 did before it
// opened the Plan, views-redesign §4), else nothing selected: the tests'
// way to the NEXT cards and rows.
func workNext(t testing.TB, m Model) Model {
	t.Helper()
	m.focusPanel(PanelWork)
	m.refresh()
	if len(m.work.Next) > 0 {
		m.lists.work.Select(m.work.Next[0].Key)
	} else {
		m.lists.work.ClearSelection()
	}
	m.refresh() // drawn with the selection, as after a key
	m.armMarquee()
	return m
}
