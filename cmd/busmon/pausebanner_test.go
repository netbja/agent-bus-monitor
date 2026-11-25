package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-suspended-banner (the person, 2026-11-22): a suspended or paused
// project says so on a full-width row under the header, /resume named, on
// every page; For you's first line is the same fact, Enter on it the
// /resume question; /resume asks as Z does.

func pausedRecord() *bus.PauseRecord {
	return &bus.PauseRecord{By: "charlie", Reason: "lunch", At: shellNow.Add(-8 * time.Minute).UnixMilli(),
		Until: shellNow.Add(112 * time.Minute).UnixMilli()}
}

// bannerRow is the row under the header, and whether every cell of it is
// drawn on the attention colour (the full-width band).
func bannerRow(m Model) (string, bool) {
	g := m.grid()
	y := m.layout.Header.H
	full := true
	for x := 0; x < m.width; x++ {
		if c := g.at(x, y); !c.st.Pill || c.st.Role != RoleAttention {
			full = false
		}
	}
	return strings.Split(g.plain(), "\n")[y], full
}

func TestPauseBanner(t *testing.T) {
	for _, c := range []struct {
		name  string
		pause *bus.PauseRecord
		want  []string
	}{
		{"suspended", suspendedPause(), []string{"SUSPENDED", "/resume", "by charlie", "reboot"}},
		{"paused", pausedRecord(), []string{"PAUSED", "/resume", "resumes in 1h52", "lunch"}},
		{"neither", nil, nil},
	} {
		for _, w := range []int{140, 100, 60} {
			m := fixtureShell(t, w, 40)
			m.snap.pause = c.pause
			m.refresh()
			row, full := bannerRow(m)
			if m.layout.Footer.Y != 39 {
				t.Errorf("%s width %d: footer on row %d", c.name, w, m.layout.Footer.Y)
			}
			if c.pause == nil {
				if m.layout.Banner.H != 0 || strings.Contains(row, "PAUSED") || strings.Contains(row, "SUSPENDED") {
					t.Errorf("%s width %d: a banner: %q", c.name, w, row)
				}
				continue
			}
			if !full {
				t.Errorf("%s width %d: the banner is not full-width on the attention colour: %q", c.name, w, row)
			}
			n := len(c.want)
			if w < 100 { // narrow: the word and /resume first, the rest as room allows
				n = 2
			}
			for _, part := range c.want[:n] {
				if !strings.Contains(row, part) {
					t.Errorf("%s width %d: banner %q lacks %q", c.name, w, row, part)
				}
			}
		}
	}
}

// A suspended project reached by a switch (from the Projects page) shows the
// same banner, not the stopped-project one, with the way back; /resume runs
// there, the one write besides S, and every other write is still refused.
func TestPauseBannerOnASwitchedStoppedProject(t *testing.T) {
	m := shellWith(t, 140, 40, shellConfig{from: "trading"}, false)
	m.snap.dispatch, m.snap.pause = "down", suspendedPause()
	m.refresh()
	row, full := bannerRow(m)
	for _, part := range []string{"SUSPENDED", "/resume", "B back to trading"} {
		if !strings.Contains(row, part) || !full {
			t.Errorf("banner %q (full %v) lacks %q", row, full, part)
		}
	}
	if strings.Contains(row, "stopped project") {
		t.Errorf("the stopped banner over the suspended one: %q", row)
	}
	if !m.readOnly() {
		t.Fatal("a stopped project reached by a switch is read-only")
	}
	var runs [][]string
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/suspend.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		runs = append(runs, args)
		return make(chan error), nil
	}
	if cmd := m.startRun("backup", []string{"--project", "agent-bus-monitor", "backup"}, "/l"); cmd != nil || len(runs) != 0 || m.ui.flash != readOnlyLine {
		t.Fatalf("a write ran while read-only: runs %q, flash %q", runs, m.ui.flash)
	}
	m.ui.flash = ""
	if cmd := m.startSleep(true); cmd != nil {
		cmd()
	}
	if want := "--project agent-bus-monitor resume --yes"; len(runs) != 1 || strings.Join(runs[0], " ") != want {
		t.Fatalf("resume on the read-only suspended project: runs %q, flash %q", runs, m.ui.flash)
	}
}

// A paused (not suspended) project reached stopped by a switch: the banner
// names S, not /resume, and a resume run stays refused as every write is.
func TestPauseBannerOnASwitchedPausedProject(t *testing.T) {
	m := shellWith(t, 140, 40, shellConfig{from: "trading"}, false)
	m.snap.dispatch, m.snap.pause = "down", pausedRecord()
	m.refresh()
	row, _ := bannerRow(m)
	if !strings.Contains(row, "PAUSED") || !strings.Contains(row, "S start services") || strings.Contains(row, "/resume") {
		t.Errorf("banner %q", row)
	}
	var runs [][]string
	m.cfg.sleepLog = func(p string) string { return "/l" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		runs = append(runs, args)
		return make(chan error), nil
	}
	if cmd := m.startSleep(true); cmd != nil || len(runs) != 0 || m.ui.flash != readOnlyLine {
		t.Fatalf("resume of a paused read-only project: cmd %v, runs %q, flash %q", cmd != nil, runs, m.ui.flash)
	}
}

// The banner stays over the Projects page.
func TestPauseBannerOverTheProjectsPage(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.snap.pause = suspendedPause()
	m.push(page{kind: ovProjects})
	m.refresh()
	if !m.rd.open() {
		t.Fatal("the Projects page did not open")
	}
	if row, full := bannerRow(m); !strings.Contains(row, "SUSPENDED") || !full {
		t.Fatalf("Projects page: banner %q (full %v)\n%s", row, full, m.grid().plain())
	}
}

// For you's first line: "Project suspended · /resume" (paused: "Project
// paused · /resume"), never counted; Enter on it asks the /resume question.
func TestForYouPauseRow(t *testing.T) {
	for _, c := range []struct {
		pause *bus.PauseRecord
		want  string
	}{{suspendedPause(), "Project suspended · /resume"}, {pausedRecord(), "Project paused · /resume"}, {nil, ""}} {
		w := newSleepWorld(t, c.pause)
		w.m.refresh()
		items := w.m.lists.forYou.items
		first := ""
		if len(items) > 0 {
			first = items[0].Line.text()
		}
		if c.want == "" {
			if strings.Contains(first, "/resume") {
				t.Errorf("not paused: For you begins %q", first)
			}
			continue
		}
		if first != c.want || items[0].Key != forYouResumeKey {
			t.Errorf("For you begins %q (key %q), want %q", first, items[0].Key, c.want)
			continue
		}
		if n := len(decisions(w.m.forYou)); n != len(decisions(forYou(w.m.snap, w.m.feed.feed))) {
			t.Errorf("the pause row is counted: %d", n)
		}
		w.m.focusPanel(PanelForYou)
		w.m.lists.forYou.Select(forYouResumeKey)
		next, cmd := w.m.Update(tea.KeyPressMsg(kEnter))
		w.m = next.(Model)
		w.feed(t, cmd)
		if f := dialogText(w.m); !strings.Contains(f, "Resume agent-bus-monitor?") {
			t.Errorf("Enter on the pause row: dialog %q", f)
			continue
		}
		w.press(t, 'y')
		if want := "--project agent-bus-monitor resume --yes"; len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want {
			t.Errorf("runs %q", w.runs)
		}
		if word, ok := rowHint(PanelForYou, ListItem{Key: forYouResumeKey}); !ok || word != "resume" {
			t.Errorf("footer word %q", word)
		}
	}
}

// /resume asks as Z does (the confirm dialog): what comes back named, the
// argv shown; a pause only lifts the pause.
func TestPaletteResumeAsksAsZ(t *testing.T) {
	for _, c := range []struct {
		pause *bus.PauseRecord
		body  string
	}{{suspendedPause(), "Services, missing agents, then the pause lifted."}, {pausedRecord(), "The pause lifted: the agents are woken again."}} {
		w := newPalCWorld(t, "charlie")
		w.m.snap.pause = c.pause
		w.run(t, "/resume")
		d := w.m.ui.dialog
		if d == nil || d.Title != "Resume agent-bus-monitor?" || d.Yes != "Resume" || !strings.Contains(dialogText(w.m), c.body) ||
			!strings.Contains(dialogText(w.m), "agentbus --project agent-bus-monitor resume --yes") {
			t.Fatalf("dialog %q", dialogText(w.m))
		}
		w.press(t, tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		if len(w.runs) != 1 {
			t.Fatalf("runs %q", w.runs)
		}
	}
}

// At the medium tier, unfocused, the pause row is one of For you's four rows:
// three items, then "+N more".
func TestForYouPauseRowMedium(t *testing.T) {
	m := fixtureShell(t, 80, 24)
	m.ui.focus = PanelAgents
	m.refresh()
	items := m.lists.forYou.items
	n := len(forYouPanelItems(m.forYou))
	if len(items) != 5 || items[0].Key != forYouResumeKey || items[4].Line.text() != fmt.Sprintf("+%d more", n-3) {
		t.Fatalf("rows %d: first %q, last %q", len(items), items[0].Key, items[len(items)-1].Line.text())
	}
}
