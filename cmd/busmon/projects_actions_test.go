package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// adminWorld is the Projects page open over the fixture, with a fake runner.
type adminWorld struct {
	sleepWorld
	reads int
}

func newAdminWorld(t *testing.T) *adminWorld {
	t.Helper()
	w := &adminWorld{sleepWorld: sleepWorld{done: make(chan error, 1)}}
	src := adminFixture(shellNow)
	inner := src.broker
	src.broker = func(ctx context.Context) ([]adminFacts, error) { w.reads++; return inner(ctx) }
	m := shellWith(t, 140, 40, shellConfig{admin: &src}, false)
	m.cfg.sleepWait = func() time.Duration { return 7 * time.Minute }
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/suspend.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return w.done, w.start
	}
	w.m = m
	w.pressAdmin(t, 'M')
	if p := w.m.rd.top(); p == nil || p.kind != ovProjects {
		t.Fatal("the page did not open")
	}
	return w
}

// pressAdmin is press that also feeds the page's reads.
func (w *adminWorld) pressAdmin(t *testing.T, r rune) {
	t.Helper()
	next, cmd := w.m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	w.m = next.(Model)
	w.feedAdmin(t, cmd)
}

func (w *adminWorld) feedAdmin(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case projectsMsg, sleepAskMsg, sleepStartedMsg:
		next, more := w.m.Update(msg)
		w.m = next.(Model)
		if _, ok := msg.(sleepStartedMsg); !ok {
			w.feedAdmin(t, more)
		}
	case tea.BatchMsg:
		for _, c := range msg {
			w.feedAdmin(t, c)
		}
	}
}

// selectRow puts the selection on the project named.
func (w *adminWorld) selectRow(t *testing.T, name string) {
	t.Helper()
	p := w.m.rd.top()
	for i, r := range p.proj.rows {
		if r.name == name {
			p.sel, p.dirty = i, true
			w.m.refresh()
			return
		}
	}
	t.Fatalf("no row %s", name)
}

// finish ends the run: its exit, then what the page reads again.
func (w *adminWorld) finish(t *testing.T, err error, last string) {
	t.Helper()
	r := w.m.ui.sleepRun
	if r == nil {
		t.Fatal("no run")
	}
	r.last = last
	next, cmd := w.m.Update(sleepDoneMsg{id: r.id, err: err})
	w.m = next.(Model)
	w.feedAdmin(t, cmd)
}

// §4: Z on another project's row asks, then runs suspend for that project,
// its output in that project's log; on a suspended row it resumes; the
// list is read again once the run ends.
func TestProjectsSleepWake(t *testing.T) {
	w := newAdminWorld(t)
	w.selectRow(t, "trading")
	w.pressAdmin(t, 'Z')
	if f := w.m.ui.flash; !strings.Contains(f, "Suspend trading?") || !strings.Contains(f, "y / n") {
		t.Fatalf("confirmation %q", f)
	}
	w.pressAdmin(t, 'y')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "--project trading suspend --yes" || w.logs[0] != "/state/agentbus/trading/suspend.log" {
		t.Fatalf("runs %q logs %q", w.runs, w.logs)
	}
	if !strings.Contains(w.m.grid().plain(), "paused "+GlyphEllipsis) {
		t.Errorf("the running row is not marked:\n%s", w.m.grid().plain())
	}
	reads := w.reads
	w.finish(t, nil, "suspended trading")
	if w.reads != reads+1 || w.m.rd.top().kind != ovProjects {
		t.Fatalf("not read again after the run: %d -> %d", reads, w.reads)
	}
	w.selectRow(t, "demo")
	w.pressAdmin(t, 'Z')
	if !strings.Contains(w.m.ui.flash, "Resume demo?") {
		t.Fatalf("resume confirmation %q", w.m.ui.flash)
	}
	w.pressAdmin(t, 'n')
	if len(w.runs) != 1 {
		t.Fatalf("n ran %q", w.runs)
	}
	w.pressAdmin(t, 'Z')
	w.pressAdmin(t, 'y')
	if len(w.runs) != 2 || strings.Join(w.runs[1], " ") != "--project demo resume --yes" {
		t.Fatalf("runs %q", w.runs)
	}
}

// Z on a template-only row runs nothing; on the open project's row it is
// the shell's Z.
func TestProjectsSleepRefusals(t *testing.T) {
	w := newAdminWorld(t)
	w.selectRow(t, "newone")
	w.pressAdmin(t, 'Z')
	if !strings.Contains(w.m.ui.flash, "newone is a template only") || w.m.ui.sleepAsk != nil {
		t.Fatalf("template Z: %q", w.m.ui.flash)
	}
	w.selectRow(t, "agent-bus-monitor")
	w.pressAdmin(t, 'Z')
	w.pressAdmin(t, 'y')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "--project agent-bus-monitor suspend --yes" {
		t.Fatalf("runs %q", w.runs)
	}
}

// §4: x stops a project's services here after a y, only when one is active
// here; b backs up any row at once, its archive path the notice; a second
// action while a run goes is refused.
func TestProjectsStopAndBackup(t *testing.T) {
	w := newAdminWorld(t)
	w.selectRow(t, "old")
	w.pressAdmin(t, 'x')
	if !strings.Contains(w.m.ui.flash, "no active service of old on this machine") || w.m.ui.sleepAsk != nil {
		t.Fatalf("x on old: %q", w.m.ui.flash)
	}
	w.selectRow(t, "agent-bus-monitor")
	w.pressAdmin(t, 'x')
	if !strings.Contains(w.m.ui.flash, "Stop the services of agent-bus-monitor here?") {
		t.Fatalf("x confirmation %q", w.m.ui.flash)
	}
	w.pressAdmin(t, 'y')
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "down agent-bus-monitor" {
		t.Fatalf("runs %q", w.runs)
	}
	w.pressAdmin(t, 'x') // while it runs: refused at once, never a question first
	if w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, "already running") {
		t.Fatalf("x during a run: ask %v flash %q", w.m.ui.sleepAsk, w.m.ui.flash)
	}
	w.selectRow(t, "demo")
	w.pressAdmin(t, 'b')
	if len(w.runs) != 1 || !strings.Contains(w.m.ui.flash, "already running") {
		t.Fatalf("a second run: %q %q", w.runs, w.m.ui.flash)
	}
	w.finish(t, nil, "stopped")
	w.pressAdmin(t, 'b')
	if len(w.runs) != 2 || strings.Join(w.runs[1], " ") != "backup --project demo" || w.logs[1] != "/state/agentbus/demo/suspend.log" {
		t.Fatalf("backup runs %q logs %q", w.runs, w.logs)
	}
	w.finish(t, nil, "/home/c/.local/share/agentbus/backups/agentbus-demo-20261026.tar.zst")
	var texts []string
	for _, n := range append(w.m.notice.queue, derefNotice(w.m.notice.cur)...) {
		texts = append(texts, n.text)
	}
	if got := strings.Join(texts, " | "); !strings.Contains(got, "backup of demo done: /home/c/.local/share/agentbus/backups/agentbus-demo-20261026.tar.zst") {
		t.Fatalf("backup notice %q", got)
	}
}

func derefNotice(n *notice) []notice {
	if n == nil {
		return nil
	}
	return []notice{*n}
}

// reviewer-codex: a backup that exits before the first one-second read of
// its log still names its archive: the log's last line is read at the exit.
func TestProjectsFastBackupNamesTheArchive(t *testing.T) {
	w := newAdminWorld(t)
	log := filepath.Join(t.TempDir(), "suspend.log")
	if err := os.WriteFile(log, []byte("--- agentbus backup --project demo\nwrote /backups/agentbus-demo.tar.zst\n/backups/agentbus-demo.tar.zst\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.m.cfg.sleepLog = func(string) string { return log }
	w.selectRow(t, "demo")
	next, cmd := w.m.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}))
	w.m = next.(Model)
	started := cmd().(sleepStartedMsg)
	w.done <- nil // it has already exited
	next, cmd = w.m.Update(started)
	w.m = next.(Model)
	var done sleepDoneMsg
	for _, c := range cmd().(tea.BatchMsg) { // the exit's waiter, not the tick
		if d, ok := c().(sleepDoneMsg); ok {
			done = d
			break
		}
	}
	if w.m.ui.sleepRun == nil || !strings.HasPrefix(w.m.ui.sleepRun.last, "starting") {
		t.Fatalf("the log was read before the exit: %+v", w.m.ui.sleepRun)
	}
	next, _ = w.m.Update(done)
	w.m = next.(Model)
	if !noticeHas(w.m, "backup of demo done: /backups/agentbus-demo.tar.zst") {
		t.Fatalf("notices %v", w.m.notice.queue)
	}
}

func noticeHas(m Model, s string) bool {
	for _, n := range append(m.notice.queue, derefNotice(m.notice.cur)...) {
		if strings.Contains(n.text, s) {
			return true
		}
	}
	return false
}

// foureyes P3: unknown is never none: with the broker or the services
// unread, Z and x say so; the runner's header line is no archive.
func TestProjectsActionsOnUnknowns(t *testing.T) {
	w := newAdminWorld(t)
	p := w.m.rd.top()
	for i := range p.proj.rows {
		p.proj.rows[i].brokerKnown, p.proj.rows[i].svcKnown = false, false
	}
	w.selectRow(t, "old")
	w.pressAdmin(t, 'Z')
	if !strings.Contains(w.m.ui.flash, "state is unknown") || w.m.ui.sleepAsk != nil {
		t.Fatalf("Z: %q", w.m.ui.flash)
	}
	w.pressAdmin(t, 'x')
	if !strings.Contains(w.m.ui.flash, "services of old here are unknown") || w.m.ui.sleepAsk != nil {
		t.Fatalf("x: %q", w.m.ui.flash)
	}
	w.pressAdmin(t, 'b')
	r := w.m.ui.sleepRun
	next, _ := w.m.Update(sleepDoneMsg{id: r.id, last: "--- 2026-10-26T16:04:05Z agentbus backup --project old"})
	w.m = next.(Model)
	if !noticeHas(w.m, "backup of old done") || noticeHas(w.m, "done: ---") {
		t.Fatalf("notices %v", w.m.notice.queue)
	}
}
