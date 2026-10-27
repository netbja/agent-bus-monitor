package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/projlist"
)

// The picker as a model of its own (busmon Charm spec, step b: "The
// picker"; wired by step c, until then --pick runs pick.go's tview screen).
// Same behaviour as pick.go: the templates with their broker state, a filter,
// and an open that checks the project afresh and runs `agentbus up` only for
// one the broker confirms. It writes nothing to Redis and never provisions.
// Every check and the up run in commands; View draws with pick.go's own
// drawPick.

// pickShell is the picker's model.
type pickShell struct {
	dir            string
	rows           []projlist.Row
	filter, status string
	statusErr      bool
	busy           string // what an open is doing ("" = idle)
	checking       bool   // the startup check has not answered yet
	connErr        error
	timeout        time.Duration
	list           *listState // behind a pointer: the model is copied through Update
	width, height  int
	now            time.Time
	spinning       bool // a spinner frame is scheduled
	closed         bool // the picker ends (tea.Quit was returned)
	keys           projlist.KeysFunc
	run            func(project string) error
	ctx            context.Context
	connFailed     func() error // the broker connection's error once known (nil: none)
	paint          *painter
}

// pickAnnotatedMsg is the startup check's answer.
type pickAnnotatedMsg struct {
	rows    []projlist.Row
	connErr error
}

// pickCheckedMsg is an open's fresh existence check.
type pickCheckedMsg struct {
	name  string
	state projlist.State
	err   error
}

// pickUpMsg is the end of `agentbus up` (or of the wait for it).
type pickUpMsg struct {
	name string
	err  error
}

// pickSpinMsg is a spinner frame.
type pickSpinMsg time.Time

func newPickShell(ctx context.Context, dir string, rows []projlist.Row, listErr error, keys projlist.KeysFunc, run func(string) error) pickShell {
	m := pickShell{dir: dir, rows: rows, checking: true, timeout: pickTimeout, list: &listState{}, keys: keys, run: run,
		ctx: ctx, paint: newPainter(th), connFailed: func() error { return nil }}
	m.list.SetHue(RoleAccent)
	if listErr != nil {
		m.status, m.statusErr = "cannot read "+dir+": "+listErr.Error(), true
	}
	return m
}

// Init checks every template on the broker and starts the spinner.
func (m pickShell) Init() tea.Cmd {
	rows, keys, ctx, failed := m.rows, m.keys, m.ctx, m.connFailed
	return tea.Batch(func() tea.Msg {
		annotated := projlist.Annotate(ctx, rows, keys, pickTimeout)
		return pickAnnotatedMsg{rows: annotated, connErr: failed()}
	}, m.spin())
}

func (m *pickShell) spin() tea.Cmd {
	m.spinning = true
	return tea.Tick(pickSpin, func(t time.Time) tea.Msg { return pickSpinMsg(t) })
}

// snapshot is the drawing's input, from the model (no lock: one goroutine).
func (m *pickShell) snapshot() pickSnapshot {
	pm := pickModel{dir: m.dir, rows: m.rows, filter: m.filter, status: m.status, statusErr: m.statusErr, busy: m.busy,
		checking: m.checking, connErr: m.connErr}
	return pickSnapshot{dir: m.dir, filter: m.filter, status: m.status, busy: m.busy, statusErr: m.statusErr,
		checking: m.checking, down: pm.brokerDownLocked(), all: m.rows, vis: pm.visibleLocked()}
}

// refresh fills the list from the model: the update path, never View.
func (m *pickShell) refresh() {
	if m.width >= minWidth && m.height >= minHeight {
		pickFill(m.list, m.snapshot(), 0, 0, m.width, m.height)
	}
}

func (m pickShell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case pickAnnotatedMsg:
		m.rows, m.connErr, m.checking = msg.rows, msg.connErr, false
	case pickSpinMsg:
		m.now, m.spinning = time.Time(msg), false
		if m.busy != "" || m.checking {
			cmd = m.spin()
		}
	case pickCheckedMsg:
		cmd = m.checked(msg)
	case pickUpMsg:
		m.busy = ""
		if msg.err != nil {
			m.status, m.statusErr = fmt.Sprintf("agentbus up %s: %v", msg.name, msg.err), true
			break
		}
		m.closed = true
		cmd = tea.Quit
	case tea.KeyPressMsg:
		cmd = m.key(keyOfTea(msg))
	case tea.MouseClickMsg:
		cmd = m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		if ms := msg.Mouse(); m.busy == "" && m.list.rect.contains(ms.X, ms.Y) {
			switch ms.Button {
			case tea.MouseWheelUp:
				m.list.ScrollBy(-3)
			case tea.MouseWheelDown:
				m.list.ScrollBy(3)
			}
		}
	}
	m.refresh()
	return m, cmd
}

// key is the picker's key table (ModePick). Any key ends a status line;
// while an open runs only esc and ^C act (up is left to finish).
func (m *pickShell) key(k Key) tea.Cmd {
	act := lookup(ModePick, PanelAgents, TierWide, k)
	if k.Code == keyCtrlC {
		act = ActQuit
	}
	if m.busy != "" {
		if act == ActQuit || act == ActClose {
			m.closed = true
			return tea.Quit
		}
		return nil
	}
	m.status, m.statusErr = "", false
	switch act {
	case ActQuit, ActClose:
		m.closed = true
		return tea.Quit
	case ActUp:
		m.list.Move(-1)
	case ActDown:
		m.list.Move(1)
	case ActPageUp:
		m.list.Move(-max(1, m.list.rows()))
	case ActPageDown:
		m.list.Move(max(1, m.list.rows()))
	case ActOpen:
		return m.start()
	case ActPickType:
		if k.Code == keyRune {
			m.filter += string(k.Rune)
		} else if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	}
	return nil
}

// click selects a row; a click on the selected row opens it (ListView's
// rule); nothing while an open runs.
func (m *pickShell) click(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft || m.busy != "" {
		return nil
	}
	if i, ok := m.list.itemAt(ms.X, ms.Y); ok && m.list.items[i].Selectable && m.list.items[i].Key == m.list.Selected() {
		return m.start()
	}
	m.list.click(ms.X, ms.Y)
	return nil
}

// selected is the selected row, else the first visible one.
func (m *pickShell) selected() (projlist.Row, bool) {
	pm := pickModel{rows: m.rows, filter: m.filter, sel: m.list.Selected()}
	return pm.selectedLocked()
}

// start opens the selected project: a fresh existence check first, in a
// command; a second Enter while it runs finds busy set.
func (m *pickShell) start() tea.Cmd {
	row, ok := m.selected()
	if !ok || m.busy != "" {
		return nil
	}
	m.busy = "checking " + row.Name + " on the broker"
	keys, ctx, timeout := m.keys, m.ctx, m.timeout
	check := func() tea.Msg {
		state, err := projlist.Exists(ctx, keys, row.Name, timeout)
		return pickCheckedMsg{name: row.Name, state: state, err: err}
	}
	if m.spinning {
		return check
	}
	return tea.Batch(check, m.spin())
}

// checked takes the fresh fact; only a present project is opened, with up
// in a command.
func (m *pickShell) checked(msg pickCheckedMsg) tea.Cmd {
	for i := range m.rows {
		if m.rows[i].Name == msg.name {
			m.rows[i].Broker = msg.state // the fresh fact replaces the startup one
		}
	}
	switch msg.state {
	case projlist.Absent:
		path := "<path>"
		for _, r := range m.rows {
			if r.Name == msg.name && r.WorkingDir != "" {
				path = r.WorkingDir
			}
		}
		m.busy = ""
		m.status, m.statusErr = fmt.Sprintf("%s has no keys on the broker (deleted?). To re-create it: agentbus up %s %s", msg.name, msg.name, path), true
		return nil
	case projlist.Unknown:
		err := msg.err
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no answer within %s (%w)", m.timeout, err)
		}
		m.busy = ""
		m.status, m.statusErr = fmt.Sprintf("cannot check %s on the broker: %v", msg.name, err), true
		return nil
	}
	m.busy = "agentbus up " + msg.name
	run, name := m.run, msg.name
	return func() tea.Msg { return pickUpMsg{name: name, err: run(name)} }
}

// View draws the picker with pick.go's drawPick, from a copy of the list.
func (m pickShell) View() tea.View {
	g := newGrid(m.width, m.height)
	lst := *m.list
	cx, cy, ok := drawPick(g, 0, 0, m.width, m.height, m.snapshot(), &lst, m.now)
	v := tea.NewView(g.render(m.paint))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if ok {
		v.Cursor = tea.NewCursor(cx, cy)
	}
	return v
}

// pickPlain is the picker's screen as text (tests).
func (m pickShell) plain() string {
	g := newGrid(m.width, m.height)
	lst := *m.list
	drawPick(g, 0, 0, m.width, m.height, m.snapshot(), &lst, m.now)
	return strings.TrimRight(g.plain(), "\n")
}
