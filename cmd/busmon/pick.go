package main

// busmon --pick: the Projects picker of the herdr plugin (spec
// 2026-10-19-herdr-plugin-design §4). It lists the project templates, says
// whether the broker holds keys for each, and opens one with `agentbus up`.
//
// It writes nothing to Redis: the listing and the check before an open are
// reads (bus.ProjectKeys, a SCAN), and `agentbus up` does its own work. The
// window between that check and up is the accepted residual of busmon
// --delete's SCAN-then-DEL. A project with no keys is never re-created from
// here, and a check that fails is never read as absence.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

const (
	pickTimeout = 2 * time.Second        // one existence check (and the startup listing) on the broker
	pickUpWait  = 2 * time.Minute        // how long the picker waits for agentbus up before saying so
	pickSpin    = 120 * time.Millisecond // spinner frame
)

// pickModel is the picker's state: the rows, the filter, the selection and
// the status line. The open runs off the event loop, so every field is read
// and written under mu.
type pickModel struct {
	mu        sync.Mutex
	dir       string         // the projects directory the rows come from
	rows      []projlist.Row // every template, sorted by name
	filter    string         // what was typed: a case-insensitive substring
	sel       string         // the selected project's name ("" = the first visible)
	status    string         // the status line ("" = the key hints)
	statusErr bool           // the status is an error
	busy      string         // what an open is doing ("" = idle): shown with a spinner
	checking  bool           // the startup check has not answered yet
	connErr   error          // the broker connection failed at startup
	timeout   time.Duration  // the bound of one existence check
}

func newPickModel(dir string, rows []projlist.Row, listErr error) *pickModel {
	m := &pickModel{dir: dir, rows: rows, timeout: pickTimeout, checking: true}
	if listErr != nil {
		m.status, m.statusErr = "cannot read "+dir+": "+listErr.Error(), true
	}
	return m
}

// setStatus replaces the status line; it stays until the next key. An error
// is drawn in the error colour, anything else (a reason given by whoever
// opened the picker) as plain text.
func (m *pickModel) setStatus(text string, isErr bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.statusErr = text, isErr
}

func (m *pickModel) statusLine() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status, m.statusErr
}

func (m *pickModel) busyText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy
}

// setAnnotated installs the rows of the startup check and the connection's
// error, if it failed.
func (m *pickModel) setAnnotated(rows []projlist.Row, connErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows, m.connErr, m.checking = rows, connErr, false
}

// brokerDown: the startup check answered for no row. Annotate keeps the
// errors to itself, so this is "every row unknown", or a failed connection.
func (m *pickModel) brokerDown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.brokerDownLocked()
}

func (m *pickModel) brokerDownLocked() bool {
	if m.checking {
		return false
	}
	if m.connErr != nil {
		return true
	}
	for _, r := range m.rows {
		if r.Broker != projlist.Unknown {
			return false
		}
	}
	return len(m.rows) > 0
}

// animating: something is in flight, so the spinner needs frames.
func (m *pickModel) animating() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy != "" || m.checking
}

// visible is the rows the filter keeps: a case-insensitive substring of the
// name or of the working directory. Deliberately not fuzzy.
func (m *pickModel) visible() []projlist.Row {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visibleLocked()
}

func (m *pickModel) visibleLocked() []projlist.Row {
	f := strings.ToLower(m.filter)
	var out []projlist.Row
	for _, r := range m.rows {
		if strings.Contains(strings.ToLower(r.Name), f) || strings.Contains(strings.ToLower(r.WorkingDir), f) {
			out = append(out, r)
		}
	}
	return out
}

func (m *pickModel) selectedLocked() (projlist.Row, bool) {
	vis := m.visibleLocked()
	for _, r := range vis {
		if r.Name == m.sel {
			return r, true
		}
	}
	if len(vis) > 0 {
		return vis[0], true
	}
	return projlist.Row{}, false
}

func (m *pickModel) setBusy(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.busy = s
}

// open checks the selected project afresh, bounded by the timeout, and runs
// up only when the broker holds its keys. It reports whether the picker should
// close: true only after a successful run. A refusal or a failure is left on
// the status line. It blocks: the picker calls it off the event loop.
func (m *pickModel) open(ctx context.Context, keys projlist.KeysFunc, run func(project string) error) (closePicker bool) {
	m.mu.Lock()
	row, ok := m.selectedLocked()
	timeout := m.timeout
	m.mu.Unlock()
	if !ok {
		return false
	}
	defer m.setBusy("")
	m.setBusy("checking " + row.Name + " on the broker")
	state, err := projlist.Exists(ctx, keys, row.Name, timeout)
	m.mu.Lock()
	for i := range m.rows {
		if m.rows[i].Name == row.Name {
			m.rows[i].Broker = state // the fresh fact replaces the startup one
		}
	}
	m.mu.Unlock()
	switch state {
	case projlist.Absent:
		path := row.WorkingDir
		if path == "" {
			path = "<path>"
		}
		m.setStatus(fmt.Sprintf("%s has no keys on the broker (deleted?). To re-create it: agentbus up %s %s", row.Name, row.Name, path), true)
		return false
	case projlist.Unknown:
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no answer within %s (%w)", timeout, err)
		}
		m.setStatus(fmt.Sprintf("cannot check %s on the broker: %v", row.Name, err), true)
		return false
	}
	m.setBusy("agentbus up " + row.Name)
	if err := run(row.Name); err != nil {
		m.setStatus(fmt.Sprintf("agentbus up %s: %v", row.Name, err), true)
		return false
	}
	return true
}

// agentbusBeside is the agentbus installed next to this busmon (symlinks
// resolved: setup.sh links both from the checkout), else the one on PATH.
func agentbusBeside() string {
	exe, err := os.Executable()
	if err != nil {
		return "agentbus"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	p := filepath.Join(filepath.Dir(exe), "agentbus")
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return "agentbus"
}

// upRunner runs `<bin> up <project>` with this process's environment. Its
// output never reaches the screen the picker draws on: stdout is dropped and
// the last line of stderr becomes the error. The wait is bounded; up itself
// is never killed, so a provisioning is not cut off midway.
func upRunner(bin string, wait time.Duration) func(project string) error {
	return func(project string) error {
		cmd := exec.Command(bin, "up", project)
		cmd.Env = os.Environ()
		var stderr tailBuffer
		cmd.Stdout, cmd.Stderr = io.Discard, &stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return nil
			}
			if tail := stderr.lastLine(); tail != "" {
				return errors.New(strings.TrimPrefix(tail, "agentbus up: "))
			}
			return err
		case <-time.After(wait):
			return fmt.Errorf("still running after %s; left to finish, not killed", wait)
		}
	}
}

// tailBuffer keeps the last 4 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - 4096; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) lastLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// brokerConn connects in the background, so the picker draws at once; a
// check waits for the connection within its own bound.
type brokerConn struct {
	done  chan struct{}
	err   error
	keys0 projlist.KeysFunc
	close func() error
}

func dialBroker(host string) *brokerConn {
	c := &brokerConn{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		client, err := bus.Connect(host) // the one resolution order (REDIS_URL, REDIS_HOST, --host)
		if err != nil {
			c.err = fmt.Errorf("Redis connection failed: %w", err)
			return
		}
		c.keys0 = func(ctx context.Context, p string) ([]string, error) { return bus.ProjectKeys(ctx, client, p) }
		c.close = client.Close
	}()
	return c
}

func (c *brokerConn) keys(ctx context.Context, project string) ([]string, error) {
	select {
	case <-c.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.keys0(ctx, project)
}

// failed is the connection's error once it has one; nil while it is pending.
func (c *brokerConn) failed() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

func (c *brokerConn) shutdown() {
	select {
	case <-c.done:
		if c.close != nil {
			c.close()
		}
	default:
	}
}

// runPicker is main's entry to the picker (a test replaces it to check the
// ordering of main without a terminal).
var runPicker = pickMain

// pickMain runs the picker full screen and returns the exit code.
func pickMain(host, theme string) int {
	dir := rolecfg.ProjectsDir(os.Getenv)
	rows, listErr := projlist.Templates(dir)
	m := newPickModel(dir, rows, listErr)
	// Why the picker was opened instead of a dashboard (the plugin's reason)
	// belongs on the status line: m.setStatus(reason, false), here.
	conn := dialBroker(host)
	defer conn.shutdown()

	scr, err := tcell.NewScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: terminal: %v\n", err)
		return 1
	}
	app := tview.NewApplication().SetScreen(scr)
	_, noColor := os.LookupEnv("NO_COLOR")
	t, err := ResolveTheme(theme, "", os.Getenv("COLORFGBG"), noColor, scr.Colors())
	if err != nil {
		scr.Fini()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	th = t

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newPicker(app, m, conn.keys, upRunner(agentbusBeside(), pickUpWait))
	p.ctx = ctx
	go func() {
		annotated := projlist.Annotate(ctx, rows, conn.keys, pickTimeout)
		m.setAnnotated(annotated, conn.failed())
		app.QueueUpdateDraw(func() {})
	}()
	go func() { // spinner frames, only while something is in flight
		tick := time.NewTicker(pickSpin)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if m.animating() {
					app.QueueUpdateDraw(func() {})
				}
			}
		}
	}()
	if err := app.EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// picker is the picker's screen: one ListView inside pickView, the key
// capture, and the open started off the event loop.
type picker struct {
	app  *tview.Application
	m    *pickModel
	keys projlist.KeysFunc
	run  func(project string) error
	ctx  context.Context
	list *ListView
	view *pickView

	quit   func() // stops the application (tests: a no-op)
	closed bool
	sync   bool // tests: open on the caller's goroutine
}

func newPicker(app *tview.Application, m *pickModel, keys projlist.KeysFunc, run func(string) error) *picker {
	p := &picker{app: app, m: m, keys: keys, run: run, ctx: context.Background(), list: NewListView(), quit: app.Stop}
	p.view = &pickView{Box: tview.NewBox(), p: p}
	p.list.SetHue(RoleAccent)
	p.list.SetOnChange(func(key string, _ bool) {
		m.mu.Lock()
		m.sel = key
		m.mu.Unlock()
	})
	p.list.SetOnOpen(func(string) { p.start() }) // a click on the selected row
	app.SetRoot(p.view, true).SetFocus(p.list)
	app.SetInputCapture(p.handleKey)
	return p
}

func (p *picker) close() {
	p.closed = true
	p.quit()
}

// start opens the selected project unless an open is already running.
func (p *picker) start() {
	p.m.mu.Lock()
	_, ok := p.m.selectedLocked()
	if p.m.busy != "" || !ok {
		p.m.mu.Unlock()
		return
	}
	p.m.busy = "checking" // claimed here, on the event loop: a second Enter finds it
	p.m.mu.Unlock()
	if p.sync {
		if p.m.open(p.ctx, p.keys, p.run) {
			p.close()
		}
		return
	}
	go func() {
		done := p.m.open(p.ctx, p.keys, p.run)
		p.app.QueueUpdateDraw(func() {
			if done {
				p.close()
			}
		})
	}()
}

// handleKey is the picker's key table (ModePick). Any key ends a status
// line; while an open runs only esc and ^C act (up is left to finish).
func (p *picker) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	act := lookup(ModePick, PanelAgents, TierWide, ev)
	if p.m.busyText() != "" {
		if act == ActQuit || act == ActClose {
			p.close()
		}
		return nil
	}
	p.m.setStatus("", false)
	switch act {
	case ActQuit, ActClose:
		p.close()
	case ActUp:
		p.list.Move(-1)
	case ActDown:
		p.list.Move(1)
	case ActPageUp:
		p.list.Move(-max(1, p.list.rows()))
	case ActPageDown:
		p.list.Move(max(1, p.list.rows()))
	case ActOpen:
		p.start()
	case ActPickType:
		p.m.mu.Lock()
		if ev.Key() == tcell.KeyRune {
			p.m.filter += string(ev.Rune())
		} else if r := []rune(p.m.filter); len(r) > 0 {
			p.m.filter = string(r[:len(r)-1])
		}
		p.m.mu.Unlock()
	}
	return nil
}

// pickView is the picker's root: header, filter line, the list frame and the
// footer. Draw refills the list from the model; it never changes focus and
// never does I/O.
type pickView struct {
	*tview.Box
	p *picker
}

func (v *pickView) Focus(delegate func(p tview.Primitive)) { delegate(v.p.list) }
func (v *pickView) HasFocus() bool                         { return v.p.list.HasFocus() }

func (v *pickView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return v.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		if v.p.m.busyText() != "" {
			return true, nil
		}
		return v.p.list.MouseHandler()(action, event, setFocus)
	})
}

// pickSnapshot is what one Draw reads from the model, under its lock once.
type pickSnapshot struct {
	dir, filter, status, busy string
	statusErr, checking, down bool
	all, vis                  []projlist.Row
}

func (m *pickModel) snapshot() pickSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return pickSnapshot{dir: m.dir, filter: m.filter, status: m.status, busy: m.busy, statusErr: m.statusErr,
		checking: m.checking, down: m.brokerDownLocked(), all: m.rows, vis: m.visibleLocked()}
}

func (v *pickView) Draw(screen tcell.Screen) {
	x0, y0, w, h := v.GetRect()
	if w < minWidth || h < minHeight {
		msg := fmt.Sprintf("terminal too small (%dx%d)", w, h)
		for y := 0; y < h; y++ {
			DrawLine(screen, x0, y0+y, w, nil, false)
		}
		DrawLine(screen, x0, y0+h/2, w, Line{{Text: strings.Repeat(" ", max(0, (w-Width(msg))/2)) + msg, Role: RoleAttention}}, false)
		return
	}
	s := v.p.m.snapshot()
	now := time.Now()
	spin := spinner(now)

	DrawLine(screen, x0, y0, w, pickHeader(s, spin, w), false)

	filter := Line{{Text: " / ", Role: RoleAccent, Bold: true}, {Text: Preview(s.filter), Role: RoleText}}
	if s.filter == "" {
		filter = append(filter, Seg{Text: "type to filter by name or directory", Role: RoleDim})
	}
	DrawLine(screen, x0, y0+1, w, filter, false)
	if s.busy == "" {
		screen.ShowCursor(x0+min(w-1, 3+Width(Preview(s.filter))), y0+1)
	} else {
		screen.HideCursor()
	}

	l := v.p.list
	l.SetRect(x0, y0+2, w, h-3)
	iw := max(0, w-2-listGutter)
	header, items := pickItems(s, iw)
	l.SetHeader(header)
	label := fmt.Sprint(len(s.all))
	if len(s.vis) != len(s.all) {
		label = fmt.Sprintf("%d/%d", len(s.vis), len(s.all))
	}
	l.SetFrame(Line{{Text: "Templates", Role: RoleText}}, Line{{Text: label, Role: RoleMuted}})
	l.SetItems(items)
	if l.Selected() == "" {
		l.First()
	}
	l.Draw(screen)

	var footer Line
	switch {
	case s.busy != "":
		footer = Line{{Text: " " + spin + " ", Role: RoleAccent}, {Text: Preview(s.busy) + GlyphEllipsis, Role: RoleMuted}}
	case s.status != "":
		role := RoleText
		if s.statusErr {
			role = RoleError
		}
		footer = Line{{Text: " " + Preview(s.status), Role: role}}
	default:
		footer = footerHints(ModePick, PanelAgents, TierWide, w)
	}
	DrawLine(screen, x0, y0+h-1, w, footer, false)
}

// spinner is three dots, one lit, moving with the clock.
func spinner(now time.Time) string {
	i := int(now.UnixMilli()/pickSpin.Milliseconds()) % 3
	dots := []string{GlyphIdle, GlyphIdle, GlyphIdle}
	dots[i] = GlyphWorking
	return strings.Join(dots, "")
}

// pickHeader: the title and the directory on the left, the broker on the
// right; the directory gives way first, then the broker word shortens.
func pickHeader(s pickSnapshot, spin string, w int) Line {
	var right Line
	switch {
	case s.checking:
		right = Line{{Text: spin + " checking the broker", Role: RoleMuted}}
	case s.down:
		right = Line{{Text: GlyphBlocked + " broker unreachable", Role: RoleError}}
	default:
		right = Line{{Text: GlyphWorking + " broker ok", Role: RoleOK}}
	}
	right = append(right, Seg{Text: " "})
	title := Line{{Text: " Projects", Role: RoleText, Bold: true}}
	room := w - right.Width() - title.Width()
	if room < 1 {
		return FitLine(append(title, Seg{Text: " "}, right[0]), w)
	}
	left := title
	if room >= 6 {
		left = append(left, Seg{Text: "  " + truncLeft(Preview(s.dir), room-3), Role: RoleMuted})
	}
	return append(FitLine(left, w-right.Width()), right...)
}

// pickItems lays the rows out in iw cells: name, working dir, broker state.
// The directory is cut on its left (its end says the most) and leaves first
// when the width is short.
func pickItems(s pickSnapshot, iw int) (Line, []ListItem) {
	const stateW = 8 // "present" and a cell before the frame
	nameW, longestDir := 5, 12
	for _, r := range s.all {
		nameW = max(nameW, Width(Preview(r.Name))+1)
		longestDir = max(longestDir, Width(Preview(r.WorkingDir))+2)
	}
	rest := max(0, iw-stateW)
	nameW = min(nameW, rest)
	dirW := min(rest-nameW, longestDir) // the state stays next to its directory
	if dirW < 8 {
		nameW, dirW = rest, 0
	}
	header := FitLine(Line{col("name", nameW, RoleDim), col("working dir", dirW, RoleDim), col("broker", stateW, RoleDim)}, iw)

	note := func(lines ...Seg) []ListItem {
		var out []ListItem
		for _, l := range lines {
			out = append(out, ListItem{Line: Line{l}})
		}
		return out
	}
	switch {
	case len(s.all) == 0:
		return nil, note(Seg{Text: "no project templates in " + Preview(s.dir), Role: RoleMuted},
			Seg{Text: "agentbus up <project> <path> writes the first one", Role: RoleDim})
	case len(s.vis) == 0:
		return header, note(Seg{Text: "no project matches \"" + Preview(s.filter) + "\"", Role: RoleMuted})
	}
	var items []ListItem
	for _, r := range s.vis {
		dir := truncLeft(Preview(r.WorkingDir), max(0, dirW-1))
		if r.WorkingDir == "" {
			dir = GlyphDash
		}
		role := RoleAttention
		switch r.Broker {
		case projlist.Present:
			role = RoleOK
		case projlist.Absent:
			role = RoleMuted
		}
		items = append(items, ListItem{Key: r.Name, Selectable: true, Line: Line{
			col(Preview(r.Name), nameW, RoleText), col(dir, dirW, RoleMuted), col(r.Broker.String(), stateW, role)}})
	}
	return header, items
}

// truncLeft keeps the end of s within w cells, marking the cut with "…" on
// the left.
func truncLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	var clusters []string
	var widths []int
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		clusters = append(clusters, g.Str())
		widths = append(widths, g.Width())
	}
	used, i := 0, len(clusters)
	for i > 0 && used+widths[i-1] <= w-1 {
		i--
		used += widths[i]
	}
	return GlyphEllipsis + strings.Join(clusters[i:], "")
}
