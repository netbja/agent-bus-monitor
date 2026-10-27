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
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// pickModel is what the picker's rules read (pickShell, tea_pick.go, holds
// the state): the rows, the filter and the selection, and whether the startup
// check has answered.
type pickModel struct {
	rows     []projlist.Row // every template, sorted by name
	filter   string         // what was typed: a case-insensitive substring
	sel      string         // the selected project's name ("" = the first visible)
	checking bool           // the startup check has not answered yet
	connErr  error          // the broker connection failed at startup
}

// brokerDown: the startup check answered for no row. Annotate keeps the
// errors to itself, so this is "every row unknown", or a failed connection.
func (m *pickModel) brokerDown() bool {
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

// visible is the rows the filter keeps: a case-insensitive substring of the
// name or of the working directory. Deliberately not fuzzy.
func (m *pickModel) visible() []projlist.Row {
	f := strings.ToLower(m.filter)
	var out []projlist.Row
	for _, r := range m.rows {
		if strings.Contains(strings.ToLower(r.Name), f) || strings.Contains(strings.ToLower(r.WorkingDir), f) {
			out = append(out, r)
		}
	}
	return out
}

// selected is the selected row, else the first visible one.
func (m *pickModel) selected() (projlist.Row, bool) {
	vis := m.visible()
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
// output never reaches the screen the picker draws on, and nothing ties it to
// the picker's life: stdout is /dev/null, stderr a log file of its own (no
// pipe a busmon goroutine copies, so no SIGPIPE once busmon exits), and it
// runs in a session of its own (no hangup from the pane). The wait is
// bounded; up is never killed, so a provisioning is not cut off midway. On
// failure the last line of its stderr is the error; when the wait runs out,
// the log is left in place and the error names it.
func upRunner(bin string, wait time.Duration) func(project string) error {
	return func(project string) error {
		log, err := os.CreateTemp(os.TempDir(), "agentbus-up-*.log")
		if err != nil {
			return err
		}
		defer log.Close() // the child holds its own descriptor
		cmd := exec.Command(bin, "up", project)
		cmd.Env = os.Environ()
		cmd.Stdout, cmd.Stderr = nil, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			os.Remove(log.Name())
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			defer os.Remove(log.Name())
			if err == nil {
				return nil
			}
			if tail := lastLine(log.Name()); tail != "" {
				return errors.New(strings.TrimPrefix(tail, "agentbus up: "))
			}
			return err
		case <-time.After(wait):
			return fmt.Errorf("still running after %s; left to finish, not killed (its errors: %s)", wait, log.Name())
		}
	}
}

// lastLine is the last non-empty line of the file's final 4 KiB.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 4096 {
		f.Seek(-4096, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
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

// pickMain runs the picker full screen (pickShell, tea_pick.go) and returns
// the exit code.
func pickMain(host, theme string) int {
	dir := rolecfg.ProjectsDir(os.Getenv)
	rows, listErr := projlist.Templates(dir)
	_, noColor := os.LookupEnv("NO_COLOR")
	t, err := ResolveTheme(theme, "", os.Getenv("COLORFGBG"), noColor, terminalColors())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	th = t
	conn := dialBroker(host)
	defer conn.shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newPickShell(ctx, dir, rows, listErr, conn.keys, upRunner(agentbusBeside(), pickUpWait))
	m.connFailed = conn.failed
	if reason := os.Getenv("AGENTBUS_PLUGIN_REASON"); reason != "" {
		m.status, m.statusErr = reason, false // why the plugin opened the picker, until the first key
	}
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// pickSnapshot is what one Draw reads from the model, under its lock once.
type pickSnapshot struct {
	dir, filter, status, busy string
	statusErr, checking, down bool
	all, vis                  []projlist.Row
}

// pickFill gives the picker's list its place, header, frame and rows from s
// (on the update path).
func pickFill(l *listState, s pickSnapshot, x0, y0, w, h int) {
	l.rect, l.focused = Rect{x0, y0 + 2, w, h - 3}, true // the picker's one list always has the keys
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
}

// drawPick draws the picker in w x h at (x0, y0) on any cell sink: header,
// filter line, the list (filled by pickFill) and the footer. It returns
// where the filter's cursor goes, ok false while an open runs.
func drawPick(screen cellSink, x0, y0, w, h int, s pickSnapshot, l *listState, now time.Time) (cx, cy int, ok bool) {
	if w < minWidth || h < minHeight {
		msg := fmt.Sprintf("terminal too small (%dx%d)", w, h)
		for y := 0; y < h; y++ {
			drawLine(screen, x0, y0+y, w, nil, false)
		}
		drawLine(screen, x0, y0+h/2, w, Line{{Text: strings.Repeat(" ", max(0, (w-Width(msg))/2)) + msg, Role: RoleAttention}}, false)
		return 0, 0, false
	}
	spin := spinner(now)
	drawLine(screen, x0, y0, w, pickHeader(s, spin, w), false)

	filter := Line{{Text: " / ", Role: RoleAccent, Bold: true}, {Text: Preview(s.filter), Role: RoleText}}
	if s.filter == "" {
		filter = append(filter, Seg{Text: "type to filter by name or directory", Role: RoleDim})
	}
	drawLine(screen, x0, y0+1, w, filter, false)
	l.draw(screen)

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
	drawLine(screen, x0, y0+h-1, w, footer, false)
	return x0 + min(w-1, 3+Width(Preview(s.filter))), y0 + 1, s.busy == ""
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
