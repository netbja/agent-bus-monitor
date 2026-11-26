package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The person's retro (self-improving loop §3.3, build step 3): /retro runs
// agentbus retro request on a task, after y: the one named, else the
// tracked request selected on Requests (its row or its page), done ones
// included, since a retro follows a task.
func TestPaletteRetro(t *testing.T) {
	asked := func(t *testing.T, w *palCWorld, task string) {
		want := "--project agent-bus-monitor retro request " + task
		t.Helper()
		if w.m.ui.sleepAsk == nil || !strings.Contains(dialogText(w.m), "agentbus "+want) {
			t.Fatalf("no confirmation naming the argv: dialog %q flash %q", dialogText(w.m), w.m.ui.flash)
		}
		if len(w.runs) != 0 {
			t.Fatalf("ran before y: %q", w.runs)
		}
		w.press(t, tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want {
			t.Fatalf("ran %q, want %q", w.runs, want)
		}
	}
	withTask := func(w *palCWorld) {
		w.m.snap.requests = append(w.m.snap.requests, request{Task: "fix-by-hand", Tracked: true, From: "master", Target: "coder", Owner: "coder", State: "done"})
		w.m.refresh()
	}

	// Named.
	w := newPalCWorld(t, "charlie")
	w.run(t, "/retro fix-by-hand")
	asked(t, w, "fix-by-hand")

	// The selected Requests row: the fixture's tracked request.
	w = newPalCWorld(t, "charlie")
	m := reqShell(t, 140, 40, nil) // the Requests fixture, with the world's runner
	m.cfg.settings.Identity, m.cfg.sleepWait, m.cfg.sleepLog, m.cfg.sleepRunner = w.m.cfg.settings.Identity, w.m.cfg.sleepWait, w.m.cfg.sleepLog, w.m.cfg.sleepRunner
	w.m = m
	row := hasKey(w.m, "req:")
	if row == "" {
		t.Fatal("the fixture has no tracked request row")
	}
	w.m.lists.requests.Select(row)
	w.run(t, "/retro")
	asked(t, w, strings.TrimPrefix(row, "req:"))

	// A done request's page: a retro follows a task.
	w = newPalCWorld(t, "charlie")
	withTask(w)
	w.m.push(page{kind: ovRequest, key: "req:fix-by-hand"})
	w.run(t, "/retro")
	asked(t, w, "fix-by-hand")

	// Nothing named, no request selected: refused before y, the way named.
	w = newPalCWorld(t, "charlie")
	w.run(t, "/retro")
	if w.m.ui.sleepAsk != nil || len(w.runs) != 0 || !w.m.ui.flashErr || !strings.Contains(w.m.ui.flash, "name the task") {
		t.Fatalf("no task: ask %v runs %q flash %q", w.m.ui.sleepAsk, w.runs, w.m.ui.flash)
	}

	// A page whose request is not tracked has no task for a retro.
	w = newPalCWorld(t, "charlie")
	w.m.snap.requests = append(w.m.snap.requests, request{Task: "a-board-task", From: "master", Target: "coder", State: "done"})
	w.m.push(page{kind: ovRequest, key: "req:a-board-task"})
	w.run(t, "/retro")
	if w.m.ui.sleepAsk != nil || len(w.runs) != 0 {
		t.Fatalf("an untracked request asked: runs %q", w.runs)
	}

	// A name that is no task name never reaches agentbus.
	w = newPalCWorld(t, "charlie")
	w.run(t, "/retro Bad!")
	if w.m.ui.sleepAsk != nil || len(w.runs) != 0 {
		t.Fatalf("a bad name asked: runs %q", w.runs)
	}
}
