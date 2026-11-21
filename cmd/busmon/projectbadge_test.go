package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// busmon-project-badge, part 1 (the person: "make the current project
// obvious at launch"): the project's name is a badge for its first seconds,
// and a project busmon chose itself (the last one opened on this broker, no
// -p) is said once in the bottom line.

// badgeShell is a shell started as main starts it: the badge on, the
// project given or not.
func badgeShell(t *testing.T, lastProject bool) Model {
	t.Helper()
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st, badge: true, lastProject: lastProject})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}

// projectSeg is the header's segment holding the project's name.
func projectSeg(t *testing.T, m Model) Seg {
	t.Helper()
	for _, s := range headerRows(m.headerData(), m.width, m.layout.Tier, m.layout.Header.H)[0] {
		if strings.TrimSpace(s.Text) == "demo" {
			return s
		}
	}
	t.Fatalf("no project in the header: %q", headerRows(m.headerData(), m.width, m.layout.Tier, m.layout.Header.H)[0].text())
	return Seg{}
}

// The badge, a pill in the accent colour (reverse video under NO_COLOR, the
// pill's own fallback), for projectBadgeFor from the first poll; then the
// name as before.
func TestProjectBadgeAtLaunch(t *testing.T) {
	m := badgeShell(t, false)
	if s := projectSeg(t, m); !s.Pill || s.Role != RoleAccent || !s.Bold {
		t.Fatalf("at launch: %+v", s)
	}
	m = step(t, m, fadeMsg(shellNow.Add(projectBadgeFor-time.Millisecond)))
	if s := projectSeg(t, m); !s.Pill {
		t.Fatalf("just before the end: %+v", s)
	}
	m = step(t, m, fadeMsg(shellNow.Add(projectBadgeFor)))
	if s := projectSeg(t, m); s.Pill || s.Role != RoleText {
		t.Fatalf("after %v: %+v", projectBadgeFor, s)
	}
	// A shell not started by main (every other test, the goldens): no badge.
	st := &shared{agents: map[string]*agentState{}}
	plain := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	plain = step(t, plain, tea.WindowSizeMsg{Width: 140, Height: 40})
	plain = step(t, plain, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	if s := projectSeg(t, plain); s.Pill {
		t.Fatalf("without badge: %+v", s)
	}
}

// No -p (and no AGENT_BUS_PROJECT): the bottom line says which project
// busmon opened and how to switch; with one given, the badge alone.
func TestProjectBadgeNotice(t *testing.T) {
	m := badgeShell(t, true)
	if l := m.noticeLine(140); !strings.Contains(l.text(), "last project opened: demo (no -p) "+GlyphNone+" M to switch") {
		t.Fatalf("notice %q", l.text())
	}
	if l := badgeShell(t, false).noticeLine(140); l != nil {
		t.Fatalf("a project given, a notice: %q", l.text())
	}
}

// main's rule: the project came from the last one used only when neither
// -p nor AGENT_BUS_PROJECT named it.
func TestProjectFromLastUsed(t *testing.T) {
	for _, c := range []struct {
		flag, env string
		want      bool
	}{{"", "", true}, {"demo", "", false}, {"", "demo", false}, {"a", "b", false}} {
		if got := projectFromLastUsed(c.flag, c.env); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

// The badge in a golden, at the usual widths.
func TestProjectBadgeGoldens(t *testing.T) {
	pinLocal(t)
	for _, w := range []int{140, 100, 60} {
		st := &shared{agents: map[string]*agentState{}, person: "charlie"}
		m := newShell(shellConfig{project: "demo", self: "charlie", st: st, badge: true, lastProject: true})
		m = step(t, m, tea.WindowSizeMsg{Width: w, Height: 24})
		m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
		lines := strings.Split(m.grid().plain(), "\n")
		checkGolden(t, "project-badge-"+strconv.Itoa(w)+".txt", lines[0]+"\n"+lines[len(lines)-1]+"\n")
		checkGolden(t, "project-badge-"+strconv.Itoa(w)+".ansi", strings.Split(m.View().Content, "\n")[0]+"\n")
	}
}
