package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// deleteWorld is the Projects page with fake checks and a fake delete.
type deleteWorld struct {
	*adminWorld
	quiet   map[string]string // project -> its live reason ("" = quiet)
	quietN  int
	deleted []string
	result  deleteResult
	quit    bool
	flip    func() // runs between the panel's last step and the delete's own check
}

func newDeleteWorld(t *testing.T) *deleteWorld {
	t.Helper()
	w := &deleteWorld{adminWorld: newAdminWorld(t), quiet: map[string]string{}}
	src := *w.m.cfg.admin
	src.quiet = func(_ context.Context, p string) (string, error) {
		w.quietN++
		if w.flip != nil && w.quietN > 1 {
			w.flip()
		}
		return w.quiet[p], nil
	}
	src.remove = func(_ context.Context, p string) deleteResult {
		w.deleted = append(w.deleted, p)
		r := w.result
		r.project = p
		return r
	}
	w.m.cfg.admin = &src
	return w
}

// press feeds the delete's messages too.
func (w *deleteWorld) press(t *testing.T, k any) {
	t.Helper()
	var msg tea.Msg
	switch k := k.(type) {
	case rune:
		msg = tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)})
	case tea.Key:
		msg = tea.KeyPressMsg(k)
	}
	next, cmd := w.m.Update(msg)
	w.m = next.(Model)
	w.feedDelete(t, cmd)
}

func (w *deleteWorld) feedDelete(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case deleteCheckMsg, deleteDoneMsg, projectsMsg, sleepAskMsg, sleepStartedMsg:
		next, more := w.m.Update(msg)
		w.m = next.(Model)
		if _, ok := msg.(sleepStartedMsg); !ok {
			w.feedDelete(t, more)
		}
	case tea.BatchMsg:
		for _, c := range msg {
			w.feedDelete(t, c)
		}
	case tea.QuitMsg:
		w.quit = true
	}
}

func (w *deleteWorld) typeName(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		w.press(t, r)
	}
}

// §5.1: each live reason refuses at d, with its way out; nothing opens.
func TestProjectsDeleteRefusedWhileLive(t *testing.T) {
	for _, reason := range []string{
		"a dispatcher runs for old (host vdr): agentbus down old there, or Z",
		"stop its services first: x",
		"2 agents are live: Z puts the project to sleep",
		"close its herdr workspace first (3 panes)",
		"cannot check that old is quiet (connection refused)",
	} {
		w := newDeleteWorld(t)
		w.quiet["old"] = reason
		w.selectRow(t, "old")
		w.press(t, 'd')
		if w.m.ui.del != nil || !strings.Contains(w.m.ui.flash, reason) {
			t.Errorf("%q: panel %v flash %q", reason, w.m.ui.del != nil, w.m.ui.flash)
		}
	}
}

// §5.2: the backup first (b runs it, the panel goes on only when it
// succeeds; n goes on without one); the name typed byte for byte; esc at
// every step writes nothing.
func TestProjectsDeletePanel(t *testing.T) {
	w := newDeleteWorld(t)
	w.selectRow(t, "old")
	w.press(t, 'd')
	if w.m.ui.del == nil || w.m.mode() != ModeProjectDelete || !strings.Contains(w.m.grid().plain(), "A backup first?") {
		t.Fatalf("d opened %+v:\n%s", w.m.ui.del, w.m.grid().plain())
	}
	w.press(t, 'b')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "backup --project old" {
		t.Fatalf("backup runs %q", w.runs)
	}
	w.finish(t, errExit1, "age not found")
	if d := w.m.ui.del; d == nil || d.step != delBackup || !strings.Contains(d.note, "backup failed") || !strings.Contains(d.note, "/state/agentbus/old/suspend.log") {
		t.Fatalf("failed backup: %+v", d)
	}
	w.press(t, 'b')
	w.finish(t, nil, "/backups/agentbus-old.tar.zst")
	if d := w.m.ui.del; d == nil || d.step != delName || d.backup != "/backups/agentbus-old.tar.zst" {
		t.Fatalf("after the backup: %+v", d)
	}
	for _, bad := range []string{"old ", "Old", "ol", "old-2"} {
		w.m.ui.del.field = ""
		w.typeName(t, bad)
		w.press(t, tea.Key{Code: tea.KeyEnter})
		if len(w.deleted) != 0 || w.m.ui.del == nil || !strings.Contains(w.m.ui.del.note, "does not match") {
			t.Fatalf("%q deleted %v note %+v", bad, w.deleted, w.m.ui.del)
		}
	}
	w.m.ui.del.field = ""
	w.typeName(t, "d") // a d in the field is a letter, never a second delete
	w.press(t, tea.Key{Code: tea.KeyBackspace})
	w.typeName(t, "old")
	w.press(t, tea.Key{Code: tea.KeyEnter})
	if strings.Join(w.deleted, " ") != "old" || w.m.ui.del != nil || w.quietN != 2 {
		t.Fatalf("delete: %v panel %+v checks %d", w.deleted, w.m.ui.del, w.quietN)
	}

	for _, at := range []string{"backup", "name"} { // esc at each step: nothing runs, nothing deleted
		w := newDeleteWorld(t)
		w.selectRow(t, "old")
		w.press(t, 'd')
		if at == "name" {
			w.press(t, 'n')
			if d := w.m.ui.del; d == nil || d.backup != "no backup" {
				t.Fatalf("n: %+v", d)
			}
			w.typeName(t, "old")
		}
		w.press(t, tea.Key{Code: tea.KeyEscape})
		if w.m.ui.del != nil || len(w.deleted) != 0 || len(w.runs) != 0 {
			t.Errorf("esc at %s: panel %v deleted %v runs %v", at, w.m.ui.del != nil, w.deleted, w.runs)
		}
	}
}

// foureyes: a project that becomes live between d and the delete deletes
// nothing; the notice says became live.
func TestProjectsDeleteBecameLive(t *testing.T) {
	w := newDeleteWorld(t)
	w.flip = func() { w.quiet["old"] = "a dispatcher runs for old (host vdr): agentbus down old there, or Z" }
	w.selectRow(t, "old")
	w.press(t, 'd')
	w.press(t, 'n')
	w.typeName(t, "old")
	w.press(t, tea.Key{Code: tea.KeyEnter})
	if len(w.deleted) != 0 || w.m.ui.del != nil || !noticeHas(w.m, "became live: a dispatcher runs for old") {
		t.Fatalf("deleted %v panel %v notices %v", w.deleted, w.m.ui.del != nil, w.m.notice.queue)
	}
}

// §5.3: the outcomes on the notice: clean, or keys that came back, or the
// script's refusal; the list is read again.
func TestProjectsDeleteOutcomes(t *testing.T) {
	for _, c := range []struct {
		res  deleteResult
		want string
	}{
		{deleteResult{keys: 12, services: []string{"dispatch"}}, "deleted 12 keys of old; removed the dispatch service here"},
		{deleteResult{keys: 12, left: []string{"old:status"}}, "but 1 key came back (old:status)"},
		{deleteResult{err: errors.New("old: a dispatch holds its lease (nothing deleted)")}, "nothing deleted"},
		// foureyes: once the script deleted, never "nothing deleted".
		{deleteResult{keys: 12, err: errors.New("deleted 12 keys of old, then could not scan again: i/o timeout")}, "deleted 12 keys of old, then could not scan again"},
	} {
		w := newDeleteWorld(t)
		w.result = c.res
		reads := w.reads
		w.selectRow(t, "old")
		w.press(t, 'd')
		w.press(t, 'n')
		w.typeName(t, "old")
		w.press(t, tea.Key{Code: tea.KeyEnter})
		if !noticeHas(w.m, c.want) || w.reads != reads+1 || w.quit || (c.res.keys > 0 && c.res.err != nil && noticeHas(w.m, "nothing deleted")) {
			t.Errorf("%+v: notices %v reads %d->%d quit %v", c.res, w.m.notice.queue, reads, w.reads, w.quit)
		}
	}
}

// §5.2.3: the open project asks a second y; after a clean delete busmon
// quits with its message; anything else keeps it open.
func TestProjectsDeleteOpenProject(t *testing.T) {
	w := newDeleteWorld(t)
	w.result = deleteResult{keys: 1204}
	w.selectRow(t, "agent-bus-monitor")
	w.press(t, 'd')
	w.press(t, 'n')
	w.typeName(t, "agent-bus-monitor")
	w.press(t, tea.Key{Code: tea.KeyEnter})
	if d := w.m.ui.del; d == nil || d.step != delOpen || len(w.deleted) != 0 || !strings.Contains(w.m.grid().plain(), "busmon has agent-bus-monitor open") {
		t.Fatalf("open project: %+v deleted %v", d, w.deleted)
	}
	w.press(t, 'x') // only y deletes
	if len(w.deleted) != 0 || w.m.ui.del == nil {
		t.Fatalf("x deleted %v", w.deleted)
	}
	w.press(t, 'y')
	if strings.Join(w.deleted, " ") != "agent-bus-monitor" || !w.quit ||
		w.m.exitNote != "agent-bus-monitor deleted (1204 keys); busmon --pick to open another project" {
		t.Fatalf("after y: deleted %v quit %v note %q", w.deleted, w.quit, w.m.exitNote)
	}
}

// d on a template-only row deletes nothing and opens nothing; a check that
// answers for a row the person left opens nothing either.
func TestProjectsDeleteGuards(t *testing.T) {
	w := newDeleteWorld(t)
	w.selectRow(t, "newone")
	w.press(t, 'd')
	if w.m.ui.del != nil || w.quietN != 0 || !strings.Contains(w.m.ui.flash, "no key on the broker") {
		t.Fatalf("template d: panel %v checks %d flash %q", w.m.ui.del != nil, w.quietN, w.m.ui.flash)
	}
	w.selectRow(t, "old")
	w.m.gotDeleteCheck(deleteCheckMsg{project: "demo"})
	if w.m.ui.del != nil {
		t.Fatal("a check for another row opened the panel")
	}
	w.m.gotDeleteCheck(deleteCheckMsg{project: "old"})
	if w.m.ui.del == nil || w.m.ui.del.row.name != "old" {
		t.Fatalf("the check for the selected row: %+v", w.m.ui.del)
	}
}

// A run in progress (a backup, a sleep) holds the delete back: refused at
// d, nothing checked. The came-back message names the keys and says how out.
func TestProjectsDeleteWaitsForARun(t *testing.T) {
	w := newDeleteWorld(t)
	w.selectRow(t, "demo")
	w.press(t, 'b')
	w.selectRow(t, "old")
	w.press(t, 'd')
	if w.m.ui.del != nil || w.quietN != 0 || !strings.Contains(w.m.ui.flash, "delete waits for it") {
		t.Fatalf("d during a run: panel %v checks %d flash %q", w.m.ui.del != nil, w.quietN, w.m.ui.flash)
	}
	got := cameBack("demo", 1204, []string{"demo:status", "demo:dispatch", "demo:agents", "demo:usage"})
	want := "deleted 1204 keys of demo, but 4 keys came back (demo:status, demo:dispatch, demo:agents): something of demo still runs; stop it, then delete again"
	if got != want {
		t.Fatalf("cameBack:\n%s\nwant\n%s", got, want)
	}
}

// master on foureyes' P2: a host without /proc says its agents are not
// checked, never quietly passes; a check that errs refuses (foureyes: the
// core of "unknown is never quiet").
func TestProjectsDeleteUnknowns(t *testing.T) {
	w := newDeleteWorld(t)
	src := *w.m.cfg.admin
	src.procAgents = func() bool { return false }
	src.quiet = func(context.Context, string) (string, error) { return "", errors.New("connection refused") }
	w.m.cfg.admin = &src
	w.pressAdmin(t, 'R')
	w.selectRow(t, "old")
	w.press(t, 'd')
	if w.m.ui.del != nil || !strings.Contains(w.m.ui.flash, "cannot check that old is quiet (connection refused)") {
		t.Fatalf("an unreadable check: panel %v flash %q", w.m.ui.del != nil, w.m.ui.flash)
	}
	src.quiet = func(context.Context, string) (string, error) { return "", nil }
	w.m.cfg.admin = &src
	w.press(t, 'd')
	if w.m.ui.del == nil || !strings.Contains(w.m.grid().plain(), "agents: not checkable on this host") {
		t.Fatalf("no /proc: panel %v\n%s", w.m.ui.del != nil, w.m.grid().plain())
	}
	w.press(t, tea.Key{Code: tea.KeyEscape})
	src.procAgents = func() bool { return true }
	w.m.cfg.admin = &src
	w.pressAdmin(t, 'R')
	w.press(t, 'd')
	if w.m.ui.del == nil || strings.Contains(w.m.grid().plain(), "not checkable") {
		t.Fatal("the line shows with /proc")
	}
}
