package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// moreLines appends n Activity lines, from shellNow+from.
func moreLines(t *testing.T, m Model, n int, from time.Duration) Model {
	t.Helper()
	var evs []bus.Event
	for i := 0; i < n; i++ {
		evs = append(evs, bus.Event{ID: fmt.Sprintf("%d-0", shellNow.Add(from+time.Duration(i)*time.Second).UnixMilli()),
			Kind: "notify", From: "x", Message: fmt.Sprintf("line %d", i)})
	}
	return step(t, m, tailMsg{events: evs})
}

// §4.3.1 case 1: the selection moved with ↑↓ wins over the typed line: the
// second row runs, not the top one.
func TestPaletteEnterCase1MovedSelection(t *testing.T) {
	rows := paletteMatch("he")
	if len(rows) < 2 {
		t.Fatalf("/he lists %v", palNames(t, rows))
	}
	top, second := paletteTable[rows[0]].Name, paletteTable[rows[1]].Name
	m, _ := palShell(t)
	m = palette(t, m, "/he", kDown, kEnter)
	via, _ := palShell(t)
	via = palette(t, via, "/"+second, kEnter)
	viaTop, _ := palShell(t)
	viaTop = palette(t, viaTop, "/"+top, kEnter)
	k1, ok1 := palTop(m)
	k2, ok2 := palTop(via)
	k3, _ := palTop(viaTop)
	if k1 != k2 || ok1 != ok2 || k1 == k3 {
		t.Fatalf("/he ↓ Enter ran %v (%v); the second row %s runs %v, the top %s %v", k1, ok1, second, k2, top, k3)
	}
}

// Case 2: a complete name whose argument is missing runs nothing, says what
// is missing, and keeps the text.
func TestPaletteEnterCase2MissingArgument(t *testing.T) {
	m, w := palShell(t)
	m = palette(t, m, "/write", kEnter)
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "agent") {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
	if m.ui.line != lineMessage || m.ui.edit != "/write" || m.rd.open() {
		t.Fatalf("line %v %q page %v", m.ui.line, m.ui.edit, m.rd.top())
	}
}

// Case 3: a word that is no name but matches rows takes the top row.
func TestPaletteEnterCase3TopRow(t *testing.T) {
	m, _ := palShell(t)
	m = palette(t, m, "/requ", kEnter)
	if k, ok := palTop(m); !ok || k != ovRequests {
		t.Fatalf("/requ + Enter: %v", m.rd.top())
	}
}

// Case 4: a word that matches no row runs nothing, says so, keeps the text.
func TestPaletteEnterCase4NoMatch(t *testing.T) {
	m, w := palShell(t)
	m = palette(t, m, "/zzz", kEnter)
	if len(w.sent) != 0 || m.rd.open() || !strings.Contains(m.ui.flash, "no command matches") {
		t.Fatalf("sent %v page %v flash %q", w.sent, m.rd.top(), m.ui.flash)
	}
	if m.ui.line != lineMessage || m.ui.edit != "/zzz" {
		t.Fatalf("line %v %q", m.ui.line, m.ui.edit)
	}
}

// §8.1: / opens the palette as Ctrl+P does, on the panels and on a page.
func TestSlashOpensThePalette(t *testing.T) {
	slash, _ := palShell(t)
	slash = keys(t, slash, '/')
	ctrl, _ := palShell(t)
	ctrl = keys(t, ctrl, kCtrlP)
	if slash.ui.line != ctrl.ui.line || slash.ui.edit != "/" || ctrl.ui.edit != "/" || slash.mode() != ctrl.mode() || !slash.paletteListOn() {
		t.Fatalf("/: line %v %q mode %v; Ctrl+P: line %v %q mode %v", slash.ui.line, slash.ui.edit, slash.mode(), ctrl.ui.line, ctrl.ui.edit, ctrl.mode())
	}
	m, _ := palShell(t)
	m = keys(t, m, tea.Key{Code: tea.KeyF5}, '/')
	if k, ok := palTop(m); !ok || k != ovHealth || m.ui.edit != "/" || !m.paletteListOn() {
		t.Fatalf("/ on a page: page %v edit %q", m.rd.top(), m.ui.edit)
	}
}

// §8.2: f opens the filter line on the panels.
func TestFOpensTheFilterLine(t *testing.T) {
	m, _ := palShell(t)
	if m = keys(t, m, 'f'); m.mode() != ModeFilter || m.ui.focus != PanelActivity {
		t.Fatalf("f: mode %v focus %v", m.mode(), m.ui.focus)
	}
}

// §8.3, follow on: a selection above the live window pauses the viewport;
// the setting stays on, and G resumes live.
func TestFollowOnPausesTheViewOnly(t *testing.T) {
	pinLocal(t)
	m := moreLines(t, fixtureShell(t, 140, 40), 40, time.Second)
	m = keys(t, m, '3', '1', 'g') // Activity, the first line
	act := &m.lists.activity
	if act.Live() || !act.Follows() || m.cfg.settings.NoFollow {
		t.Fatalf("g: live %v follows %v", act.Live(), act.Follows())
	}
	if m = keys(t, m, 'G'); !m.lists.activity.Live() {
		t.Fatal("G did not resume with follow on")
	}
}

// §8.3, follow off: Activity never scrolls by itself; G jumps to the newest
// line once, does not resume live, and the lines after it are counted.
func TestFollowOffGJumpsOnce(t *testing.T) {
	pinLocal(t)
	m := shellWith(t, 140, 40, shellConfig{settings: busmonSettings{NoFollow: true}}, false)
	m = moreLines(t, m, 40, time.Second)
	act := &m.lists.activity
	if act.Live() || act.Follows() {
		t.Fatalf("follow off: live %v follows %v", act.Live(), act.Follows())
	}
	m = keys(t, m, '3', '1', 'g', 'G')
	act = &m.lists.activity
	if act.Live() || act.Selected() != "" {
		t.Fatalf("G: live %v sel %q", act.Live(), act.Selected())
	}
	newest := max(0, len(act.items)-act.rows())
	if act.top != newest {
		t.Fatalf("G: top %d, want the newest rows (%d)", act.top, newest)
	}
	m = moreLines(t, m, 5, time.Hour)
	if m.lists.activity.Live() || m.lists.activity.top != newest || m.ui.pause.New() != 5 {
		t.Fatalf("after G: live %v top %d (want %d) new %d", m.lists.activity.Live(), m.lists.activity.top, newest, m.ui.pause.New())
	}
	if !strings.Contains(m.grid().plain(), "auto-scroll off") {
		t.Error("the title does not say auto-scroll off")
	}
}

// §8.3: /follow flips display.follow in config.toml, as Enter on the key in
// the settings page does, applies it and flashes the new value.
func TestPaletteFollowFlipsTheSetting(t *testing.T) {
	pinLocal(t)
	dir := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": dir, "HOME": dir}
	m := shellWith(t, 140, 40, shellConfig{getenv: func(k string) string { return env[k] }}, false)
	m = palette(t, m, "/follow")
	m, cmd := pressCmd(t, m, kEnter)
	if cmd == nil {
		t.Fatal("/follow returned no command")
	}
	msg := runFollow(t, cmd)
	m = step(t, m, msg)
	if !m.cfg.settings.NoFollow || m.lists.activity.Follows() || !strings.Contains(m.ui.flash, "auto-scroll off") {
		t.Fatalf("after /follow: nofollow %v follows %v flash %q", m.cfg.settings.NoFollow, m.lists.activity.Follows(), m.ui.flash)
	}
	f := config.Load(filepath.Join(dir, "agentbus", "config.toml"), "config.toml")
	if r := (config.Resolver{Files: []*config.File{f}}); r.Bool("display.follow") {
		b, _ := os.ReadFile(filepath.Join(dir, "agentbus", "config.toml"))
		t.Fatalf("config.toml: %s", b)
	}
	// Back on, through the row itself (a reminder popup may now take i: the
	// settings read again turn the fixture's reminders on).
	if got := palNames(t, paletteMatch("autoscroll")); len(got) == 0 || got[0] != "follow" {
		t.Fatalf("autoscroll lists %v", got)
	}
	cmd = m.palFollow(nil)
	m = step(t, m, runFollow(t, cmd))
	if m.cfg.settings.NoFollow || !m.lists.activity.Follows() || !m.lists.activity.Live() || !strings.Contains(m.ui.flash, "auto-scroll on") {
		t.Fatalf("after /autoscroll: nofollow %v live %v flash %q", m.cfg.settings.NoFollow, m.lists.activity.Live(), m.ui.flash)
	}
}

// runFollow runs a command (possibly a batch) until it yields the followMsg.
func runFollow(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	var find func(tea.Cmd) tea.Msg
	find = func(c tea.Cmd) tea.Msg {
		if c == nil {
			return nil
		}
		switch msg := c().(type) {
		case followMsg:
			return msg
		case tea.BatchMsg:
			for _, sub := range msg {
				if r := find(sub); r != nil {
					return r
				}
			}
		}
		return nil
	}
	msg := find(cmd)
	if msg == nil {
		t.Fatal("no followMsg from /follow")
	}
	return msg
}
