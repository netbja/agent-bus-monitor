package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// sleepWorld is the shell with a fake runner for agentbus suspend/resume.
type sleepWorld struct {
	m     Model
	runs  [][]string
	logs  []string
	done  chan error
	start error
}

func newSleepWorld(t *testing.T, pause *bus.PauseRecord) *sleepWorld {
	t.Helper()
	w := &sleepWorld{done: make(chan error, 1)}
	m := fixtureShell(t, 140, 40)
	m.cfg.sleepWait = func() time.Duration { return 7 * time.Minute }
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/suspend.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return w.done, w.start
	}
	m.snap.pause = pause
	w.m = m
	return w
}

// press is one key and the message its command answers with (a command that
// starts a timer is not run: the tests feed its message themselves).
func (w *sleepWorld) press(t *testing.T, r rune) {
	t.Helper()
	next, cmd := w.m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	w.m = next.(Model)
	w.feed(t, cmd)
}

func (w *sleepWorld) feed(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case sleepAskMsg, sleepStartedMsg:
		next, more := w.m.Update(msg)
		w.m = next.(Model)
		if _, ok := msg.(sleepStartedMsg); !ok {
			w.feed(t, more)
		}
	}
}

var errExit1 = errors.New("exit status 1")

func suspendedPause() *bus.PauseRecord {
	return &bus.PauseRecord{Kind: bus.PauseKindSuspend, By: "charlie", Reason: "reboot",
		At: shellNow.Add(-time.Hour).UnixMilli(), Until: shellNow.Add(365 * 24 * time.Hour).UnixMilli()}
}

// Z asks by the pause's kind: a project not suspended is asked to sleep, its
// wait named; y runs agentbus suspend for the open project, its output in
// the project's suspend.log.
func TestSleepKeySuspends(t *testing.T) {
	w := newSleepWorld(t, nil)
	w.press(t, 'Z')
	if f := w.m.ui.flash; !strings.Contains(f, "Suspend agent-bus-monitor?") || !strings.Contains(f, "7m") || !strings.Contains(f, "y / n") {
		t.Fatalf("confirmation = %q", f)
	}
	w.press(t, 'y')
	want := "--project agent-bus-monitor suspend --yes"
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want || w.logs[0] != "/state/agentbus/agent-bus-monitor/suspend.log" {
		t.Fatalf("runs = %q, logs %q", w.runs, w.logs)
	}
	if w.m.ui.sleepRun == nil {
		t.Fatal("no run recorded")
	}
}

// On a suspended project, Z asks to resume and y runs agentbus resume --yes;
// any other key than y runs nothing.
func TestSleepKeyResumes(t *testing.T) {
	w := newSleepWorld(t, suspendedPause())
	w.press(t, 'Z')
	if f := w.m.ui.flash; !strings.Contains(f, "Resume agent-bus-monitor?") || !strings.Contains(f, "y / n") {
		t.Fatalf("confirmation = %q", f)
	}
	w.press(t, 'n')
	if len(w.runs) != 0 || w.m.ui.sleepAsk != nil {
		t.Fatalf("n ran %q", w.runs)
	}
	w.press(t, 'Z')
	w.press(t, 'y')
	if want := "--project agent-bus-monitor resume --yes"; len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want {
		t.Fatalf("runs = %q", w.runs)
	}
}

// While it runs, its last line is the notice; a second Z says it runs; once
// it exits, the outcome is the notice.
func TestSleepRunShowsItsProgress(t *testing.T) {
	w := newSleepWorld(t, nil)
	w.press(t, 'Z')
	w.press(t, 'y')
	w.m = step(t, w.m, sleepLineMsg{line: "agent-bus-monitor: waiting for coder's turn to end"})
	if got := w.m.noticeLine(200).text(); !strings.Contains(got, "waiting for coder") {
		t.Fatalf("notice while it runs = %q", got)
	}
	w.press(t, 'Z')
	if f := w.m.ui.flash; !strings.Contains(f, "already running") || len(w.runs) != 1 {
		t.Fatalf("second Z: %q, runs %q", f, w.runs)
	}
	w.m.ui.flash = ""
	w.m = step(t, w.m, sleepDoneMsg{err: nil})
	if w.m.ui.sleepRun != nil {
		t.Fatal("the run is still recorded")
	}
	w.m = step(t, w.m, fadeMsg{})
	if got := w.m.noticeLine(200).text(); !strings.Contains(got, "suspend done") {
		t.Fatalf("outcome notice = %q", got)
	}
}

// A run that fails says so with its last line.
func TestSleepRunFailure(t *testing.T) {
	w := newSleepWorld(t, nil)
	w.press(t, 'Z')
	w.press(t, 'y')
	w.m = step(t, w.m, sleepLineMsg{line: "agent-bus-monitor: no name recorded for you"})
	w.m = step(t, w.m, sleepDoneMsg{err: errExit1})
	w.m = step(t, w.m, fadeMsg{})
	if got := w.m.noticeLine(200).text(); !strings.Contains(got, "suspend failed") || !strings.Contains(got, "no name recorded") {
		t.Fatalf("failure notice = %q", got)
	}
}

// The header says a suspend, not a timed pause.
func TestPauseSegOfASuspend(t *testing.T) {
	pinLocal(t)
	got := pauseSeg(suspendedPause(), shellNow).text()
	want := "suspended since " + shellNow.Add(-time.Hour).Format("15:04") + " " + GlyphNone + " by charlie " + GlyphNone + " reboot"
	if got != want {
		t.Fatalf("pauseSeg = %q, want %q", got, want)
	}
}

// The pause popup on a suspend offers the full resume first, and pressing it
// runs agentbus resume as Z then y does.
func TestPausePopupOnASuspend(t *testing.T) {
	w := newSleepWorld(t, suspendedPause())
	w.m.ui.pausePopup = &pauseDialog{rec: *suspendedPause(), key: "k", sel: 0}
	g := newGrid(140, 40)
	drawPausePopup(g, w.m.pauseDialogRect(), w.m.ui.pausePopup.rec, 0, shellNow)
	if !strings.Contains(g.plain(), "Resume (services, agents)") || !strings.Contains(g.plain(), "Project suspended since") {
		t.Fatalf("popup:\n%s", g.plain())
	}
	w.feed(t, w.m.pressPause(0))
	if want := "--project agent-bus-monitor resume --yes"; len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want {
		t.Fatalf("runs = %q", w.runs)
	}
}

// The popup's Resume while a run goes starts nothing more and says so, as a
// second Z does (foureyes).
func TestPausePopupResumeWhileARunGoes(t *testing.T) {
	w := newSleepWorld(t, suspendedPause())
	w.press(t, 'Z')
	w.press(t, 'y')
	w.m.ui.pausePopup = &pauseDialog{rec: *suspendedPause(), key: "k", sel: 0}
	w.feed(t, w.m.pressPause(0))
	if len(w.runs) != 1 || !strings.Contains(w.m.ui.flash, "already running") {
		t.Fatalf("runs %q, flash %q", w.runs, w.m.ui.flash)
	}
}
