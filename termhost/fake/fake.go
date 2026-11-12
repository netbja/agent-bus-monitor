// Package fake is an in-memory termhost.Host for tests: it keeps the
// interface's rules (spec 2026-11-10-herdr-exit-design.md, 4.2) and lets a
// test script each terminal's program (ready, input empty, landing, a
// person active). It runs no process.
package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

type term struct {
	t                   termhost.Term
	ready, empty, lands bool
	personActive        bool
	leftBehind          bool
	screen              []string
	submitted           []string
	activity            termhost.Activity
	procs               []termhost.Proc
}

// Host is the fake. The zero value is not usable: New makes one.
type Host struct {
	mu    sync.Mutex
	next  int
	terms map[termhost.TermID]*term
	order []termhost.TermID
}

// New is an empty fake host.
func New() *Host { return &Host{terms: map[termhost.TermID]*term{}} }

// Host returns h itself, so a *Host is its own contract.Harness.
func (h *Host) Host() termhost.Host { return h }

func (h *Host) get(id termhost.TermID) (*term, error) {
	t, ok := h.terms[id]
	if !ok {
		return nil, termhost.ErrUnknownTerm
	}
	return t, nil
}

// Start makes a live terminal, never reusing one; its program starts not
// ready, with an empty input, landing what is pasted.
func (h *Host) Start(_ context.Context, s termhost.Spec) (termhost.Term, error) {
	if !bus.ValidName(s.Project) || !bus.ValidName(s.Name) {
		return termhost.Term{}, termhost.ErrInvalidName
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	id := termhost.TermID(fmt.Sprintf("fake-%d", h.next))
	t := &term{t: termhost.Term{ID: id, Project: s.Project, Name: s.Name, PID: 1000 + h.next, PIDStart: uint64(h.next), Alive: true, Build: "fake"},
		empty: true, lands: true}
	h.terms[id] = t
	h.order = append(h.order, id)
	return t.t, nil
}

// List is project's terminals, in start order, live or closed.
func (h *Host) List(_ context.Context, project string) ([]termhost.Term, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []termhost.Term
	for _, id := range h.order {
		if t := h.terms[id]; t.t.Project == project {
			out = append(out, t.t)
		}
	}
	return out, nil
}

// Close ends the terminal's program.
func (h *Host) Close(_ context.Context, id termhost.TermID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return err
	}
	t.t.Alive = false
	return nil
}

// Screen is the terminal's last lines lines (all for lines <= 0).
func (h *Host) Screen(_ context.Context, id termhost.TermID, lines int) (termhost.Screen, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return termhost.Screen{}, err
	}
	l := t.screen
	if lines > 0 && len(l) > lines {
		l = l[len(l)-lines:]
	}
	return termhost.Screen{Lines: slices.Clone(l), Styled: slices.Clone(l), CursorRow: len(l)}, nil
}

// Activity is what SetActivity set.
func (h *Host) Activity(_ context.Context, id termhost.TermID) (termhost.Activity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return termhost.Activity{}, err
	}
	return t.activity, nil
}

// Processes is what SetProcesses set.
func (h *Host) Processes(_ context.Context, id termhost.TermID) ([]termhost.Proc, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return nil, err
	}
	return slices.Clone(t.procs), nil
}

// Submit keeps the interface's rules: refused while a person is active, a
// left-behind line stands, or the program is not ready with an empty input;
// a line not seen is left in the input (ErrNotLanded) and the terminal left
// behind until its input reads empty again.
func (h *Host) Submit(_ context.Context, id termhost.TermID, line termhost.Line) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return err
	}
	switch {
	case !line.Valid():
		return termhost.ErrInvalidLine
	case !t.t.Alive:
		return termhost.ErrUnknownTerm
	case t.personActive:
		return termhost.ErrPersonActive
	}
	if t.leftBehind {
		if !t.empty {
			return termhost.ErrLeftBehind
		}
		t.leftBehind = false // a settled screen shows the input empty
	}
	if !t.ready || !t.empty {
		return termhost.ErrNotReady
	}
	if !t.lands {
		t.leftBehind, t.empty = true, false // pasted, not seen: no CR
		return termhost.ErrNotLanded
	}
	t.submitted = append(t.submitted, line.Text())
	t.screen = append(t.screen, "> "+line.Text())
	return nil
}

// WaitScreen reports whether match shows on the screen; the fake's screen
// changes only under the test's calls, so it does not wait.
func (h *Host) WaitScreen(_ context.Context, id termhost.TermID, match string, _ time.Duration) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, err := h.get(id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(t.screen, func(l string) bool { return strings.Contains(l, match) }), nil
}

// Ping always answers.
func (h *Host) Ping(context.Context) error { return nil }

// Describe counts the live terminals.
func (h *Host) Describe(context.Context) (termhost.HostInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, t := range h.terms {
		if t.t.Alive {
			n++
		}
	}
	return termhost.HostInfo{Builds: []string{"fake"}, Terms: n}, nil
}

func (h *Host) set(id termhost.TermID, f func(*term)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.terms[id]; ok {
		f(t)
	}
}

// SetReady scripts the program's ready signature.
func (h *Host) SetReady(id termhost.TermID, v bool) { h.set(id, func(t *term) { t.ready = v }) }

// SetInputEmpty scripts the program's input region.
func (h *Host) SetInputEmpty(id termhost.TermID, v bool) { h.set(id, func(t *term) { t.empty = v }) }

// SetLands scripts whether a pasted line is seen before CR.
func (h *Host) SetLands(id termhost.TermID, v bool) { h.set(id, func(t *term) { t.lands = v }) }

// SetPersonActive scripts a viewer typing or a draft.
func (h *Host) SetPersonActive(id termhost.TermID, v bool) {
	h.set(id, func(t *term) { t.personActive = v })
}

// SetScreen replaces the terminal's screen lines.
func (h *Host) SetScreen(id termhost.TermID, lines ...string) {
	h.set(id, func(t *term) { t.screen = slices.Clone(lines) })
}

// SetActivity sets what Activity returns.
func (h *Host) SetActivity(id termhost.TermID, a termhost.Activity) {
	h.set(id, func(t *term) { t.activity = a })
}

// SetProcesses sets what Processes returns.
func (h *Host) SetProcesses(id termhost.TermID, p ...termhost.Proc) {
	h.set(id, func(t *term) { t.procs = slices.Clone(p) })
}

// Submitted is what the terminal's program received as submitted lines.
func (h *Host) Submitted(id termhost.TermID) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.terms[id]; ok {
		return slices.Clone(t.submitted)
	}
	return nil
}
