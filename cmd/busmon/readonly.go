package main

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A stopped project reached by a switch is read-only (project-switch-ux
// item 1: the person's word on its banner; master's decision after
// reviewer-codex): one guard refuses every write, whatever page asks for
// it. The bus writer is wrapped (guardWriter), and busmon's other ways to
// change something check readOnly first: an agentbus run (all but S's
// own), settings, attach, the Projects page's delete. S with its y/n and
// every navigation still work; the local cache (read marks) is view state.

// readOnlyLine is the one line a refused write says.
const readOnlyLine = "stopped project: read-only, S starts services"

var errReadOnly = errors.New(readOnlyLine)

// readOnlyFlag is the guard's state, shared by the model's copies and the
// commands it starts: refresh sets it.
type readOnlyFlag struct{ v atomic.Bool }

func (f *readOnlyFlag) set(on bool) {
	if f != nil {
		f.v.Store(on)
	}
}

func (f *readOnlyFlag) on() bool { return f != nil && f.v.Load() }

// readOnly: the open project is the stopped one a switch reached.
func (m Model) readOnly() bool { return m.stoppedBanner() }

// refuseReadOnly says the line and reports true while read-only.
func (m *Model) refuseReadOnly() bool {
	if !m.readOnly() {
		return false
	}
	m.ui.flash, m.ui.flashErr = readOnlyLine, true
	return true
}

// startServicesArgs is S's own run, the one agentbus run allowed while
// read-only.
func (m Model) startServicesArgs() []string {
	return []string{"--project", m.cfg.project, "service", "start"}
}

func (m Model) isStartServices(args []string) bool { return slices.Equal(args, m.startServicesArgs()) }

// guardWriter is w, refusing its writes while ro is on; its reads pass.
// TestReadOnlyWriterRefusesEveryWrite calls every busWriter method but the
// reads it lists: a method added to busWriter fails it until it is guarded
// here.
func guardWriter(w busWriter, ro *readOnlyFlag) busWriter {
	if w == nil {
		return nil
	}
	return roWriter{w, ro}
}

// innerWriter is w without the guard, for the optional interfaces busmon
// asserts on it (liveStreamReader, recapReader: reads; seenWriter: the read
// receipt, which writeSeen skips while read-only).
func innerWriter(w busWriter) busWriter {
	if g, ok := w.(roWriter); ok {
		return g.busWriter
	}
	return w
}

type roWriter struct {
	busWriter
	ro *readOnlyFlag
}

func (w roWriter) TypedCmd(ctx context.Context, from, target, command string) (string, error) {
	if w.ro.on() {
		return "", errReadOnly
	}
	return w.busWriter.TypedCmd(ctx, from, target, command)
}

func (w roWriter) TypedNotify(ctx context.Context, from, message string) (string, error) {
	if w.ro.on() {
		return "", errReadOnly
	}
	return w.busWriter.TypedNotify(ctx, from, message)
}

func (w roWriter) HumanClose(ctx context.Context, from, target, ref, message string) (string, error) {
	if w.ro.on() {
		return "", errReadOnly
	}
	return w.busWriter.HumanClose(ctx, from, target, ref, message)
}

func (w roWriter) Answer(ctx context.Context, askID, qid, by, choice, text string) (string, int, error) {
	if w.ro.on() {
		return "", 0, errReadOnly
	}
	return w.busWriter.Answer(ctx, askID, qid, by, choice, text)
}

func (w roWriter) ForgetAgent(ctx context.Context, agent string) (bool, error) {
	if w.ro.on() {
		return false, errReadOnly
	}
	return w.busWriter.ForgetAgent(ctx, agent)
}

func (w roWriter) Resume(ctx context.Context, by, message string) (bool, error) {
	if w.ro.on() {
		return false, errReadOnly
	}
	return w.busWriter.Resume(ctx, by, message)
}

func (w roWriter) SetRequestPriority(ctx context.Context, task, level, by string) (bool, error) {
	if w.ro.on() {
		return false, errReadOnly
	}
	return w.busWriter.SetRequestPriority(ctx, task, level, by)
}

func (w roWriter) SetPlanPriority(ctx context.Context, task, level, by string) (bool, error) {
	if w.ro.on() {
		return false, errReadOnly
	}
	return w.busWriter.SetPlanPriority(ctx, task, level, by)
}

func (w roWriter) AddIdea(ctx context.Context, d bus.IdeaDef) (string, error) {
	if w.ro.on() {
		return "", errReadOnly
	}
	return w.busWriter.AddIdea(ctx, d)
}

func (w roWriter) SetIdeaState(ctx context.Context, id, expect, to, by string) error {
	if w.ro.on() {
		return errReadOnly
	}
	return w.busWriter.SetIdeaState(ctx, id, expect, to, by)
}
