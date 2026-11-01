package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// reqShell is the fixture shell on the Requests tab, its facts the spec's
// fixture (reqFixture): the cmd stream, the request, the gates, each
// target's cursor and the held reviewer.
func reqShell(t *testing.T, w, h int, fw *fakeWriter) Model {
	t.Helper()
	in := reqFixture(shellNow)
	cfg := shellConfig{project: "agent-bus-monitor", self: "charlie"}
	if fw != nil {
		cfg.w = fw
	}
	m := newShell(cfg)
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	snap := shellFixture(shellNow)
	snap.asks, snap.pause, snap.personGates = nil, nil, nil
	snap.cmdHistory, snap.requests, snap.threads = in.cmds, in.reqs, nil
	agents := map[string]*agentState{}
	for _, n := range []string{"master", "sentinel", "coder", "foureyes", "ui-kimi", "reviewer-codex"} {
		a := &agentState{state: "idle", stateAt: shellNow, judgedAt: shellNow, registered: true}
		if d, ok := in.delivery[n]; ok {
			a.delivered, a.deliveryOK = d.lastDelivered, true
		}
		a.gates, a.held = in.gates[n], in.held[n]
		agents[n] = a
	}
	snap.agents = agents
	m = step(t, m, pollMsg{snap: snap, at: shellNow})
	m.focusPanel(PanelRequests)
	m.refresh()
	return m
}

// The view at 190, 120 and 80 columns, from the spec's fixture.
func TestRequestsGoldens(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{190, 44}, {120, 40}, {80, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := reqShell(t, sz[0], sz[1], nil)
			m.paint = newPainter(th)
			checkGolden(t, fmt.Sprintf("requests-%dx%d.txt", sz[0], sz[1]), m.grid().plain()+"\n")
		})
	}
}

func rowKeys(m Model) []string {
	var out []string
	for _, it := range m.lists.requests.items {
		if it.Selectable {
			out = append(out, it.Key)
		}
	}
	return out
}

func hasKey(m Model, prefix string) string {
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, prefix) {
			return k
		}
	}
	return ""
}

// Enter on each row kind does what the spec's table says.
func TestRequestsEnter(t *testing.T) {
	m := reqShell(t, 120, 40, nil)
	n := len(rowKeys(m))
	grp := ""
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, "grp:") && strings.Contains(k, "|sentinel|master|") {
			grp = k
		}
	}
	if grp == "" {
		t.Fatalf("no sentinel>master group: %v", rowKeys(m))
	}
	m.lists.requests.Select(grp)
	if m = keys(t, m, kEnter); len(rowKeys(m)) != n+6 {
		t.Errorf("group expanded to %d rows, want %d", len(rowKeys(m)), n+6)
	}
	if m = keys(t, m, kEnter); len(rowKeys(m)) != n {
		t.Errorf("group did not fold: %d rows", len(rowKeys(m)))
	}
	m.lists.requests.Select(hasKey(m, "older:"))
	if m = keys(t, m, kEnter); len(rowKeys(m)) != n+20 {
		t.Errorf("+ 20 older expanded to %d rows, want %d", len(rowKeys(m)), n+20)
	}
	m = keys(t, m, kEnter)
	if m = keys(t, m, 'o'); len(rowKeys(m)) != n+77 {
		t.Errorf("o showed %d rows, want %d", len(rowKeys(m)), n+77)
	}
	m = keys(t, m, 'o')
	m.lists.requests.Select(reqNoticesKey) // the one notices line expands in place (foureyes)
	if m = keys(t, m, kEnter); m.rd.open() || len(rowKeys(m)) <= n {
		t.Fatalf("the notices line opened a page or expanded nothing: %d rows", len(rowKeys(m)))
	}
	group := ""
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, "ntc:grp:") {
			group = k
		}
	}
	m.lists.requests.Select(group)
	if m = keys(t, m, kEnter); !m.rd.open() || m.rd.top().kind != ovNotices || len(m.rd.top().body) < 1 {
		t.Fatalf("a notice group opened %v", m.rd.open())
	}
	m.closeReader()
	m.lists.requests.Select(reqNoticesKey)
	m = keys(t, m, kEnter)
	m.lists.requests.Select("req:busmon-group-a")
	if m = keys(t, m, kEnter); !m.rd.open() || m.rd.top().kind != ovRequest {
		t.Fatal("the tracked request opened no request page")
	}
	m.closeReader()
	m.lists.requests.Select(hasKey(m, "thr:"))
	if m = keys(t, m, kEnter); !m.rd.open() || m.rd.top().kind != ovMessage {
		t.Fatal("a thread opened no thread page")
	}
}

// d then y on a group closes each of its threads once.
func TestRequestsDeleteClosesEachThreadOfAGroup(t *testing.T) {
	fw := &fakeWriter{}
	m := reqShell(t, 120, 40, fw)
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, "grp:") && strings.Contains(k, "|sentinel|master|") {
			m.lists.requests.Select(k)
		}
	}
	m, cmd := pressCmd(t, keys(t, m, 'd'), 'y')
	m = run(t, m, cmd)
	if got := strings.Count(sentBy(fw), "close "); got != 6 {
		t.Fatalf("closed %d threads, want 6: %s", got, sentBy(fw))
	}
	for _, k := range rowKeys(m) {
		if strings.Contains(k, "|sentinel|master|") {
			t.Error("the closed group is still listed")
		}
	}
}

// d on the notices line closes nothing (every notice at once); d on one
// notice group, shown by Enter on the line, closes that group's threads.
func TestRequestsDeleteNotices(t *testing.T) {
	fw := &fakeWriter{}
	m := reqShell(t, 120, 40, fw)
	m.lists.requests.Select(reqNoticesKey)
	m, cmd := pressCmd(t, keys(t, m, 'd'), 'y')
	m = run(t, m, cmd)
	if got := strings.Count(sentBy(fw), "close "); got != 0 {
		t.Fatalf("d on the notices line closed %d threads", got)
	}
	m = keys(t, m, kEnter) // shows the groups
	for _, k := range rowKeys(m) {
		if strings.HasPrefix(k, "ntc:grp:") && strings.Contains(k, "|dispatch|sentinel|") {
			m.lists.requests.Select(k)
		}
	}
	m, cmd = pressCmd(t, keys(t, m, 'd'), 'y')
	m = run(t, m, cmd)
	if got := strings.Count(sentBy(fw), "close "); got != 6 {
		t.Fatalf("d on dispatch → sentinel's notices closed %d threads, want 6: %s", got, sentBy(fw))
	}
}
