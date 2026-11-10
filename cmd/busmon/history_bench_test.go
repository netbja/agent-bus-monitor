package main

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/history"
)

// realHistView loads a real archive for a benchmark: BUSMON_HIST_BENCH_DIR
// names it (a history dir); without it the benchmark skips, so the suite
// never reads a real archive.
func realHistView(b *testing.B) histView {
	dir := os.Getenv("BUSMON_HIST_BENCH_DIR")
	if dir == "" {
		b.Skip("BUSMON_HIST_BENCH_DIR unset")
	}
	now := time.Now()
	v := histView{days: histDaysStep, loaded: true}
	var err error
	v.archive, v.cursor, v.cut, err = history.ReadBack(dir, now.AddDate(0, 0, -histDaysStep),
		history.Cursor{Day: now.UTC().Format("2006-01-02"), Offset: -1}, histReadCap)
	if err != nil {
		b.Fatal(err)
	}
	v.window(now)
	b.Logf("%d entries", len(v.entries))
	return v
}

// The cost of one history page build on a real archive: what every key press
// paid before the fix (history-view-lag).
func BenchmarkHistoryBodyReal(b *testing.B) {
	v := realHistView(b)
	b.ResetTimer()
	for i := range b.N {
		historyBody(v, i%100, time.Local, 160)
	}
}

// What a key press costs now: re-marking one row of the built page.
func BenchmarkHistoryMoveReal(b *testing.B) {
	v := realHistView(b)
	_, rows, items := historyBody(v, -1, time.Local, 160)
	b.ResetTimer()
	for i := range b.N {
		markHistRow(rows, items, i%len(items))
	}
}

// One key press on the History page, end to end: the update and the frame
// drawn (View), on a real archive, the page already built.
func BenchmarkHistoryKeyReal(b *testing.B) {
	v := realHistView(b)
	m := fixtureShell(b, 160, 50)
	m.push(page{kind: ovHistory})
	m.rd.top().hist, m.rd.top().dirty = &v, true
	m.refresh()
	down := tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"})
	up := tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"})
	b.ResetTimer()
	for i := range b.N {
		msg := down
		if i%40 >= 20 {
			msg = up
		}
		next, _ := m.Update(msg)
		m = next.(Model)
		_ = m.View()
	}
}
