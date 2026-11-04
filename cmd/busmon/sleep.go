package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/service"
)

// Z, the project's sleep (suspend spec 2026-11-03 §9): one key for both
// ways, on the open project. Not suspended, a confirmation, then agentbus
// suspend; suspended, a confirmation, then agentbus resume. busmon never
// stops or starts a service itself: it runs the CLI as a child, its output
// in the project's suspend.log, whose last line is the notice until it exits.

// sleepAsk is Z waiting for its y.
type sleepAsk struct{ resume bool }

// sleepRun is the child running: what it does, its log, its last line.
type sleepRun struct {
	id              int // which run: a message of an earlier one is ignored (reviewer-codex)
	verb, log, last string
}

// sleepAskMsg opens the confirmation once suspend.wait is read.
type sleepAskMsg struct {
	resume bool
	wait   time.Duration
}

// sleepStartedMsg: the child started (done is its exit), or did not (err).
type sleepStartedMsg struct {
	id        int
	verb, log string
	done      <-chan error
	err       error
}

// sleepLineMsg is the log's last line, read every second while it runs.
type sleepLineMsg struct {
	id   int
	line string
}

// sleepDoneMsg: the child exited.
type sleepDoneMsg struct {
	id  int
	err error
}

// sleepEvery is how often the log's last line is read.
const sleepEvery = time.Second

// sleepKey is Z: the confirmation by the pause's kind, read off the update
// path with suspend.wait; a second Z while a run goes says so.
func (m *Model) sleepKey() tea.Cmd {
	if r := m.ui.sleepRun; r != nil {
		m.ui.flash, m.ui.flashErr = r.verb+" already running (see "+r.log+")", true
		return nil
	}
	if m.snap.pauseFailed {
		m.ui.flash, m.ui.flashErr = "the pause could not be read: nothing done", true
		return nil
	}
	resume := m.snap.pause != nil && m.snap.pause.Suspended()
	wait := m.cfg.sleepWait
	return func() tea.Msg {
		msg := sleepAskMsg{resume: resume}
		if !resume && wait != nil {
			msg.wait = wait()
		}
		return msg
	}
}

// gotSleepAsk shows the question; y runs it, any other key cancels.
func (m *Model) gotSleepAsk(msg sleepAskMsg) {
	p := m.cfg.project
	m.ui.sleepAsk = &sleepAsk{resume: msg.resume}
	if msg.resume {
		m.ui.flash = "Resume " + p + "? Services, missing agents, then the pause lifted. y / n"
	} else {
		m.ui.flash = "Suspend " + p + "? The project is paused now, running turns may end (up to " + compactDur(msg.wait) +
			"), then its services stop. y / n"
	}
	m.ui.flashErr = true
}

// sleepConfirm answers the question: y starts the run, any other key not.
func (m *Model) sleepConfirm(k Key) tea.Cmd {
	a := m.ui.sleepAsk
	m.ui.sleepAsk = nil
	if k.Code != keyRune || k.Rune != 'y' {
		m.ui.flash, m.ui.flashErr = "nothing done", false
		return nil
	}
	m.ui.flash, m.ui.flashErr = "", false
	return m.startSleep(a.resume)
}

// startSleep runs agentbus suspend or resume for the open project, in a
// command: the child is started off the update path.
func (m *Model) startSleep(resume bool) tea.Cmd {
	if r := m.ui.sleepRun; r != nil { // one run at a time, whichever way it was asked for (foureyes)
		m.ui.flash, m.ui.flashErr = r.verb+" already running (see "+r.log+")", true
		return nil
	}
	run, logOf, p := m.cfg.sleepRunner, m.cfg.sleepLog, m.cfg.project
	verb := "suspend"
	if resume {
		verb = "resume"
	}
	if run == nil || logOf == nil {
		m.ui.flash, m.ui.flashErr = "no agentbus to run "+verb+" with", true
		return nil
	}
	m.ui.sleepSeq++
	id := m.ui.sleepSeq
	m.ui.sleepRun = &sleepRun{id: id, verb: verb, log: logOf(p), last: "starting" + GlyphEllipsis}
	args := []string{"--project", p, verb, "--yes"}
	log := m.ui.sleepRun.log
	return func() tea.Msg {
		done, err := run(args, log)
		return sleepStartedMsg{id: id, verb: verb, log: log, done: done, err: err}
	}
}

// gotSleepStarted waits for the exit and reads the log every second.
// A message of an earlier run is ignored: its id is not the run's.
func (m *Model) gotSleepStarted(msg sleepStartedMsg) tea.Cmd {
	if r := m.ui.sleepRun; r == nil || r.id != msg.id {
		return nil
	}
	if msg.err != nil {
		m.ui.sleepRun = nil
		m.notice.push(notice{text: msg.verb + " not started: " + Preview(msg.err.Error()), level: noticeAttention})
		return nil
	}
	done := msg.done
	id := msg.id
	return tea.Batch(func() tea.Msg { return sleepDoneMsg{id: id, err: <-done} }, sleepTick(id, msg.log))
}

func sleepTick(id int, log string) tea.Cmd {
	return tea.Tick(sleepEvery, func(time.Time) tea.Msg { return sleepLineMsg{id: id, line: lastLine(log)} })
}

// gotSleepLine keeps the last line and reads again while the run goes.
func (m *Model) gotSleepLine(msg sleepLineMsg) tea.Cmd {
	r := m.ui.sleepRun
	if r == nil || r.id != msg.id { // an earlier run's tick ends its own chain
		return nil
	}
	if msg.line != "" {
		r.last = msg.line
	}
	return sleepTick(r.id, r.log)
}

// gotSleepDone: the outcome is the notice, with the last line on a failure.
func (m *Model) gotSleepDone(msg sleepDoneMsg) {
	r := m.ui.sleepRun
	if r == nil || r.id != msg.id { // an earlier run's exit never ends this one
		return
	}
	m.ui.sleepRun = nil
	if msg.err == nil {
		m.notice.push(notice{text: r.verb + " done", level: noticeDone})
		return
	}
	why := r.last
	if why == "" || strings.HasPrefix(why, "starting") {
		why = msg.err.Error()
	}
	m.notice.push(notice{text: r.verb + " failed: " + Preview(why) + " (" + r.log + ")", level: noticeAttention})
}

// sleepNotice is the run's line in the notice's place while it goes.
func (m Model) sleepNotice() *notice {
	if r := m.ui.sleepRun; r != nil {
		return &notice{text: r.verb + ": " + Preview(r.last), level: noticeInfo}
	}
	return nil
}

// realSleepRunner starts `<bin> args...` in a session of its own, its output
// appended to log (0600 in 0700), as the picker runs up: no pipe busmon
// copies, so busmon exiting never stops it.
func realSleepRunner(bin string) func(args []string, log string) (<-chan error, error) {
	return func(args []string, log string) (<-chan error, error) {
		if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		defer f.Close() // the child holds its own descriptor
		fmt.Fprintf(f, "--- %s agentbus %s\n", time.Now().Format(time.RFC3339), strings.Join(args, " "))
		cmd := exec.Command(bin, args...)
		cmd.Env = os.Environ()
		cmd.Stdout, cmd.Stderr = f, f
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return done, nil
	}
}

// realSleepLog is the project's suspend.log, beside its snapshot.
func realSleepLog(getenv func(string) string) func(project string) string {
	return func(project string) string {
		paths, err := service.PathsFrom(getenv)
		if err != nil {
			return filepath.Join(os.TempDir(), "agentbus-"+project+"-suspend.log")
		}
		return filepath.Join(paths.StateDir(project), "suspend.log")
	}
}

// realSleepWait is suspend.wait as agentbus suspend reads it.
func realSleepWait(getenv func(string) string) func() time.Duration {
	return func() time.Duration { return config.Machine(getenv).Duration("suspend.wait") }
}
