package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// sibling-enforce §3: the person sees every parallel exception, on the
// Board row and under the plan task whose board field names the request.
func TestParallelIsShown(t *testing.T) {
	board := map[string]bus.BoardEntry{
		"build-a": {Owner: "coder", State: "accepted", Updated: boardNow.Unix(), Request: &bus.RequestInfo{ID: "1-0", From: "master", Target: "coder",
			CreatedAt: boardNow.UnixMilli(), AcceptedAt: boardNow.UnixMilli(), Parallel: &bus.ParallelMark{By: "master", Reason: "2 and 3 share no file"}}},
		"build-b": {Owner: "coder", State: "accepted", Updated: boardNow.Unix(), Request: &bus.RequestInfo{ID: "2-0", From: "master", Target: "coder",
			CreatedAt: boardNow.UnixMilli(), AcceptedAt: boardNow.UnixMilli()}},
	}
	v := boardViewOf(board, boardNow, 160, boardDefaults)
	rows := map[string]string{}
	for _, it := range v.items {
		rows[it.Key] = it.Line.text()
	}
	if !strings.Contains(rows["build-a"], "parallel: 2 and 3 share no file") || strings.Contains(rows["build-b"], "parallel") {
		t.Fatalf("board rows: %q", rows)
	}
	reqs := boardRequests(board)
	if got := planParallel(plan.Row{Task: plan.Task{ID: "t1", Board: "build-a"}}, reqs); got != "2 and 3 share no file" {
		t.Fatalf("plan task of build-a: %q", got)
	}
	for _, r := range []plan.Row{{Task: plan.Task{ID: "t2", Board: "build-b"}}, {Task: plan.Task{ID: "t3"}}} {
		if got := planParallel(r, reqs); got != "" {
			t.Fatalf("%s: %q", r.Task.ID, got)
		}
	}
	// Drawn under its task on the Plan board.
	a, other := boardRow("a1", plan.InProgress, "coder"), boardRow("b1", plan.InProgress, "coder")
	a.Task.Board, other.Task.Board = "build-a", "build-b"
	v2 := planBoard(boardPlan(a, other), 0, false, shellNow, 120, TierWide, false, reqs)
	under := -1
	for i, l := range v2.lines {
		if strings.Contains(l.text(), "parallel: 2 and 3 share no file") {
			under = i
		}
	}
	row := ""
	for i := under - 1; i >= 0 && row == ""; i-- { // the task row above it (its progress line may come between)
		if t := v2.lines[i].text(); strings.Contains(t, "title ") {
			row = t
		}
	}
	if under < 1 || !strings.Contains(row, "title a1") || strings.Count(v2.plain, "parallel:") != 1 {
		t.Fatalf("plan board:\n%s", v2.plain)
	}
}
