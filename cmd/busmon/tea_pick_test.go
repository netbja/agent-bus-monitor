package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/netbja/agent-bus-monitor/projlist"
)

// pickFixture is the picker's golden list: one project of each broker state,
// one without a working dir, and one whose directory is cut on its left.
func pickFixture() []projlist.Row {
	return []projlist.Row{
		{Name: "agent-bus-monitor", WorkingDir: "/data/projects/agent-bus-monitor", Broker: projlist.Present},
		{Name: "ai-tradex-solana", WorkingDir: "/data/projects/ai-tradex-solana", Broker: projlist.Present},
		{Name: "hyperliquid", WorkingDir: "/data/projects/trading/very/deep/hyperliquid-bot", Broker: projlist.Absent},
		{Name: "polymarket", WorkingDir: "", Broker: projlist.Unknown},
	}
}

// pickRecorder records the picker's calls: the existence checks it reads
// and the ups it runs (nothing else reaches the broker).
type pickRecorder struct {
	checked, ran []string
	present      []string
	checkErr     error
	upErr        error
}

func (r *pickRecorder) keys(_ context.Context, p string) ([]string, error) {
	r.checked = append(r.checked, p)
	if r.checkErr != nil {
		return nil, r.checkErr
	}
	if slices.Contains(r.present, p) {
		return []string{p + ":status"}, nil
	}
	return nil, nil
}

func (r *pickRecorder) run(p string) error {
	r.ran = append(r.ran, p)
	return r.upErr
}

// pickShellAt is the picker after its startup check, at w x h.
func pickShellAt(t testing.TB, w, h int, rows []projlist.Row, rec *pickRecorder) pickShell {
	t.Helper()
	m := newPickShell(context.Background(), "/home/bernard/.config/agentbus/projects", nil, nil, rec.keys, rec.run)
	m.now = shellNow
	m = pickStep(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return pickStep(t, m, pickAnnotatedMsg{rows: rows})
}

func pickStep(t testing.TB, m pickShell, msg tea.Msg) pickShell {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(pickShell)
}

// pickPress is one key and its command.
func pickPress(t testing.TB, m pickShell, k any) (pickShell, tea.Cmd) {
	t.Helper()
	var msg tea.Msg
	switch k := k.(type) {
	case rune:
		msg = tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)})
	case tea.Key:
		msg = tea.KeyPressMsg(k)
	}
	next, cmd := m.Update(msg)
	return next.(pickShell), cmd
}

// pickRun runs a command and feeds its messages back until none is left;
// a spinner frame ends the chain (the program would schedule the next).
func pickRun(t testing.TB, m pickShell, cmd tea.Cmd) (pickShell, bool) {
	t.Helper()
	quit := false
	for cmd != nil {
		msg := cmd()
		cmd = nil
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				var q bool
				m, q = pickRun(t, m, c)
				quit = quit || q
			}
		case tea.QuitMsg:
			quit = true
		case pickSpinMsg:
		default:
			var next tea.Model
			next, cmd = m.Update(msg)
			m = next.(pickShell)
		}
	}
	return m, quit
}

// The picker's View at 80x24 and 140x40, dark and light: the text and the
// styled content, the same under both renderer width methods.
func TestPickShellGoldens(t *testing.T) {
	pinLocal(t)
	for _, pal := range []string{"dark", "light"} {
		for _, size := range [][2]int{{80, 24}, {140, 40}} {
			t.Run(fmt.Sprintf("%s-%dx%d", pal, size[0], size[1]), func(t *testing.T) {
				withPalette(t, pal, false, 1<<24)
				m := pickShellAt(t, size[0], size[1], pickFixture(), &pickRecorder{})
				m.paint = newPainter(th)
				m, _ = pickPress(t, m, kDown)
				content, plain := m.View().Content, m.plain()
				for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
					if got := placed(content, size[0], size[1], method); strings.TrimRight(got, "\n") != plain {
						t.Fatalf("width method %v places the View otherwise:\n%s\nwant:\n%s", method, got, plain)
					}
				}
				base := fmt.Sprintf("pick-%dx%d-%s", size[0], size[1], pal)
				checkGolden(t, base+".txt", plain+"\n")
				checkGolden(t, base+".ansi", content+"\n")
			})
		}
	}
}

// Keys: typing filters by name or directory (a substring), Backspace widens
// it again; the arrows move; any key ends a status line; Esc and ^C close (q
// is a letter of the filter, as in tview's picker).
func TestPickShellKeys(t *testing.T) {
	m := pickShellAt(t, 120, 30, pickFixture(), &pickRecorder{})
	for _, r := range "trad" {
		m, _ = pickPress(t, m, r)
	}
	vis := m.snapshot().vis
	if len(vis) != 2 || vis[0].Name != "ai-tradex-solana" || vis[1].Name != "hyperliquid" { // hyperliquid by its directory
		t.Fatalf("filter trad: %v", vis)
	}
	m, _ = pickPress(t, m, kDown)
	if row, _ := m.selected(); row.Name != "hyperliquid" {
		t.Fatalf("down: %s", row.Name)
	}
	for range 4 {
		m, _ = pickPress(t, m, kBackspce)
	}
	if len(m.snapshot().vis) != 4 || m.filter != "" {
		t.Fatalf("backspace: filter %q, %d rows", m.filter, len(m.snapshot().vis))
	}
	m.status, m.statusErr = "an error", true
	if m, _ = pickPress(t, m, kDown); m.status != "" {
		t.Fatal("a key did not end the status line")
	}
	if mm, _ := pickPress(t, m, 'q'); mm.closed || mm.filter != "q" {
		t.Fatal("q did not type into the filter")
	}
	for _, k := range []any{kEsc, tea.Key{Code: 'c', Mod: tea.ModCtrl}} {
		if mm, cmd := pickPress(t, m, k); !mm.closed || cmd == nil {
			t.Fatalf("%v did not close the picker", k)
		}
	}
}

// Enter on a project the broker confirms checks it afresh, runs agentbus up
// for it, once, and closes; nothing else is called.
func TestPickShellOpensAPresentProject(t *testing.T) {
	rec := &pickRecorder{present: []string{"agent-bus-monitor"}}
	m := pickShellAt(t, 120, 30, pickFixture(), rec)
	m, cmd := pickPress(t, m, kEnter)
	if m.busy == "" || cmd == nil || len(rec.checked) != 0 {
		t.Fatalf("Enter: busy %q, checked in Update %v", m.busy, rec.checked)
	}
	if again, cmd2 := pickPress(t, m, kEnter); cmd2 != nil || again.busy != m.busy {
		t.Fatal("a second Enter while the open runs started another")
	}
	m, quit := pickRun(t, m, cmd)
	if !quit || !m.closed || !slices.Equal(rec.checked, []string{"agent-bus-monitor"}) || !slices.Equal(rec.ran, []string{"agent-bus-monitor"}) {
		t.Fatalf("quit %v, checked %v, ran %v", quit, rec.checked, rec.ran)
	}
}

// A project the broker does not hold is never provisioned: the picker says
// how to re-create it; a check that fails is never read as absence; a
// failed up is said, and the picker stays.
func TestPickShellNeverProvisions(t *testing.T) {
	for _, c := range []struct {
		name   string
		row    int
		rec    *pickRecorder
		status string
		ran    bool
	}{
		{"absent", 2, &pickRecorder{}, "hyperliquid has no keys on the broker (deleted?). To re-create it: agentbus up hyperliquid /data/projects/trading/very/deep/hyperliquid-bot", false},
		{"no dir", 3, &pickRecorder{}, "polymarket has no keys on the broker (deleted?). To re-create it: agentbus up polymarket <path>", false},
		{"check fails", 0, &pickRecorder{checkErr: errors.New("connection refused")}, "cannot check agent-bus-monitor on the broker: connection refused", false},
		{"up fails", 0, &pickRecorder{present: []string{"agent-bus-monitor"}, upErr: errors.New("template broken")}, "agentbus up agent-bus-monitor: template broken", true},
	} {
		m := pickShellAt(t, 120, 30, pickFixture(), c.rec)
		for range c.row {
			m, _ = pickPress(t, m, kDown)
		}
		m, cmd := pickPress(t, m, kEnter)
		m, quit := pickRun(t, m, cmd)
		if quit || m.closed || m.busy != "" || m.status != c.status || !m.statusErr || (len(c.rec.ran) > 0) != c.ran {
			t.Errorf("%s: quit %v, busy %q, status %q, ran %v", c.name, quit, m.busy, m.status, c.rec.ran)
		}
	}
}

// While an open runs, keys other than Esc, q and ^C do nothing, and so do
// clicks.
func TestPickShellBusyIgnoresInput(t *testing.T) {
	m := pickShellAt(t, 120, 30, pickFixture(), &pickRecorder{})
	m, _ = pickPress(t, m, kEnter)
	sel, _ := m.selected()
	for _, k := range []any{kDown, 'x', kBackspce} {
		m, _ = pickPress(t, m, k)
	}
	_, iy, _, _ := m.list.inner()
	m = pickStep(t, m, tea.MouseClickMsg{X: 5, Y: iy + 2, Button: tea.MouseLeft})
	if now, _ := m.selected(); now.Name != sel.Name || m.filter != "" {
		t.Fatalf("busy: selection %s -> %s, filter %q", sel.Name, now.Name, m.filter)
	}
}

// The mouse: a click selects a row, a click on the selected row opens it,
// the wheel scrolls three rows.
func TestPickShellMouse(t *testing.T) {
	var rows []projlist.Row
	for i := range 40 {
		rows = append(rows, projlist.Row{Name: fmt.Sprintf("p%02d", i), WorkingDir: "/src", Broker: projlist.Present})
	}
	rec := &pickRecorder{present: []string{"p02"}}
	m := pickShellAt(t, 100, 20, rows, rec)
	_, iy, _, _ := m.list.inner()
	m = pickStep(t, m, tea.MouseClickMsg{X: 5, Y: iy + 2, Button: tea.MouseLeft})
	if row, _ := m.selected(); row.Name != "p02" || m.busy != "" {
		t.Fatalf("click: %s, busy %q", row.Name, m.busy)
	}
	next, cmd := m.Update(tea.MouseClickMsg{X: 5, Y: iy + 2, Button: tea.MouseLeft})
	m = next.(pickShell)
	if m, quit := pickRun(t, m, cmd); !quit || !slices.Equal(rec.ran, []string{"p02"}) {
		t.Fatalf("a click on the selected row: quit %v, ran %v (busy %q)", quit, rec.ran, m.busy)
	}
	m = pickShellAt(t, 100, 20, rows, &pickRecorder{})
	if m = pickStep(t, m, tea.MouseWheelMsg{X: 5, Y: iy + 2, Button: tea.MouseWheelDown}); m.list.top != 3 {
		t.Fatalf("wheel: top %d", m.list.top)
	}
}

// The header says what the broker said: checking, ok, or unreachable when
// every row is unknown or the connection failed.
func TestPickShellBrokerHeader(t *testing.T) {
	m := newPickShell(context.Background(), "/cfg", pickFixture(), nil, (&pickRecorder{}).keys, nil)
	m = pickStep(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	if !strings.Contains(m.plain(), "checking the broker") {
		t.Fatal("no checking before the startup check answers")
	}
	ok := pickStep(t, m, pickAnnotatedMsg{rows: pickFixture()})
	if !strings.Contains(ok.plain(), "broker ok") {
		t.Fatal("no broker ok")
	}
	down := pickStep(t, m, pickAnnotatedMsg{rows: pickFixture(), connErr: errors.New("refused")})
	if !strings.Contains(down.plain(), "broker unreachable") {
		t.Fatal("no broker unreachable after a failed connection")
	}
}

// The picker's frames stay straight at every size, palette and hostile row,
// and its View is what the terminal shows under both width methods.
func TestPickShellAlignment(t *testing.T) {
	pinLocal(t)
	hostile := []projlist.Row{
		{Name: "alpha", WorkingDir: "/data/" + hostileText, Broker: projlist.Present},
		{Name: "b" + strings.Repeat("x", 31), WorkingDir: hostileText + strings.Repeat("/deep", 30), Broker: projlist.Absent},
		{Name: "gamma", WorkingDir: "", Broker: projlist.Unknown},
	}
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
	}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, sz := range append([][2]int{{80, 24}, {140, 40}}, alignSizes...) {
			for _, state := range []string{"rows", "empty", "status", "busy"} {
				name := fmt.Sprintf("%s-%d/%dx%d/%s", pal.name, pal.colors, sz[0], sz[1], state)
				rows := hostile
				if state == "empty" {
					rows = nil
				}
				m := pickShellAt(t, sz[0], sz[1], rows, &pickRecorder{})
				m.paint = newPainter(th)
				switch state {
				case "status":
					m.status, m.statusErr = hostileText, true
				case "busy":
					m.busy = "agentbus up " + hostileText
				}
				if sz[0] < minWidth || sz[1] < minHeight {
					continue
				}
				g := newGrid(sz[0], sz[1])
				lst := *m.list
				drawPick(g, 0, 0, sz[0], sz[1], m.snapshot(), &lst, m.now)
				checkFrameAt(t, func(x, y int) string { return g.at(x, y).text }, name+" list", m.list.rect, true)
				plain := g.plain()
				if strings.ContainsAny(plain, "\x1b\t\u202e") {
					t.Errorf("%s: control characters reached the screen", name)
				}
				content := m.View().Content
				for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
					if got := placed(content, sz[0], sz[1], method); got != plain {
						t.Errorf("%s: width method %v places it otherwise:\n%s\nwant:\n%s", name, method, got, plain)
					}
				}
				if t.Failed() {
					return
				}
			}
		}
	}
}
