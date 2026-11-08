package main

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"flag"
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/netbja/agent-bus-monitor/config"
)

var updateGoldens = flag.Bool("update", false, "rewrite testdata/golden/*")

// goldenSizes: the narrow, medium and wide tiers (busmon Charm spec, step b),
// and a tall wide screen where every agent row, so every flag, is on screen.
var goldenSizes = [][2]int{{60, 20}, {80, 24}, {140, 40}, {140, 60}}

// withPalette runs f with the theme of that palette and restores it.
func withPalette(t testing.TB, name string, noColor bool, colors int) {
	t.Helper()
	old := th
	nt, err := ResolveTheme(name, "", "", noColor, colors)
	if err != nil {
		t.Fatal(err)
	}
	th = nt
	t.Cleanup(func() { th = old })
}

// checkGolden compares got with testdata/golden/name byte for byte, or
// rewrites it with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run go test -run TestShellGoldens -update)", name, err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// goldenStates are the screens the goldens draw (busmon Charm spec, step b
// tests): the dashboard, the report with a long code block expanded in
// Activity, that report in the reader, and the compose window open.
var goldenStates = []string{"dashboard", "expanded", "reader", "compose", "notice", "health", "settings", "plan", "plantab",
	"notify-subscribe", "notify-qr", "notify-discord", "notify-desktop", "notify-result", "notify-failed", "notify-targets", "projects", "projects-detail", "projects-unknown", "idle"}

// reportID is the fixture's report, whose fenced code is longer than any width.
var reportID = fmt.Sprintf("%d-0", shellNow.Add(-13*time.Minute).UnixMilli())

// inState is the fixture shell brought to a golden state by keys.
func inState(t testing.TB, m Model, state string) Model {
	t.Helper()
	switch state {
	case "dashboard":
		return m
	case "idle": // the idle view, layout A (busmon idle view spec)
		m.cfg.settings.IdleView = time.Minute
		return pollAt(t, m, time.Hour)
	case "projects", "projects-detail", "projects-unknown": // the Projects page (M, projects-admin §9)
		src := adminFixture(shellNow)
		if state == "projects-unknown" {
			src = adminSource{templates: src.templates, host: "vdr",
				broker: func(context.Context) ([]adminFacts, error) { return nil, errors.New("connection refused") }}
		}
		m.cfg.admin = &src
		m, cmd := pressCmd(t, m, 'M')
		m = run(t, m, cmd)
		if state == "projects-detail" {
			m = keys(t, m, 'j')
			m, cmd = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
			m = run(t, m, cmd)
		}
		return m
	case "notice": // sentinel reloaded coder: the notice at the footer's right end (busmon-notice)
		snap := shellFixture(shellNow)
		for _, a := range snap.agents {
			a.judgedAt = shellNow
		}
		return step(t, m, pollMsg{snap: snap, at: shellNow, swaps: []bus.SwapEvent{{Role: "coder", Event: bus.SwapReloaded, Kind: bus.SwapKindReload, By: "sentinel"}}})
	case "health": // F5 (busmon pages): two checks to do, one in hand, one not measured; the first open
		snap := shellFixture(shellNow)
		snap.guardianHealth, snap.guardianNow = healthFixture(shellNow), shellNow
		snap.guardian = summarizeGuardian(snap.guardianHealth, shellNow)
		m = step(t, m, pollMsg{snap: snap, at: shellNow})
		return keys(t, keys(t, m, tea.Key{Code: tea.KeyF5}), kEnter)
	case "settings": // C (settings-panels): Display, theme and icons set in config.toml, Icons selected
		m.cfg.getenv = settingsEnv(t, "[display]\ntheme = \"dark\"\nicons = \"nerd\"\n[waiting]\nsnooze = \"nonsense\"\n", "",
			map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
		m = stepCmd(t, m, tea.KeyPressMsg(tea.Key{Code: 'C', Text: "C"}))
		return keys(t, m, kEnter, kDown, kDown) // into Display's keys, then Icons
	case "plan", "plantab": // home-inbox-drawer §4: the drawer (p) and the Plan tab, the second task selected
		snap := shellFixture(shellNow)
		snap.planState = goldenPlan()
		m = step(t, m, pollMsg{snap: snap, at: shellNow})
		if state == "plan" {
			return keys(t, m, 'p', 'j')
		}
		tab := rune('4')
		if m.layout.Tier == TierNarrow {
			tab = '6'
		}
		return keys(t, m, tab, 'j', 'j')
	case "notify-subscribe", "notify-qr", "notify-discord", "notify-desktop", "notify-result", "notify-failed", "notify-targets":
		return notifyGolden(t, m, state)
	case "compose": // a directive to coder, two lines written
		m = typeText(t, keys(t, m, 'I'), "@cod")
		m = typeText(t, keys(t, m, kTab), "Review lot 1 please: the reader, the ask mode and the cache.")
		return typeText(t, keys(t, m, kEnter), "Thanks")
	}
	tab := rune('1') // Activity, the tabbed panel's first tab
	if m.layout.Tier == TierNarrow {
		tab = '3' // the narrow tier's Activity view
	}
	m = keys(t, m, tab)
	m.lists.activity.Select(feedKey(t, m, reportID))
	m = keys(t, m, kEnter)
	if state == "reader" {
		m = keys(t, m, kEnter)
	}
	return m
}

// feedKey is the Activity list key of the entry id.
func feedKey(t testing.TB, m Model, id string) string {
	t.Helper()
	for _, fl := range m.feed.feed {
		if fl.ev.ID == id && !fl.sep {
			return fl.id
		}
	}
	t.Fatalf("no feed line for %s", id)
	return ""
}

// The shell's View in each golden state, at each size, dark and light: the
// text (.txt) and the styled content (.ansi), the same under both renderer
// width methods.
func TestShellGoldens(t *testing.T) {
	pinLocal(t)
	for _, state := range goldenStates {
		for _, pal := range []string{"dark", "light"} {
			for _, size := range goldenSizes {
				t.Run(fmt.Sprintf("%s-%s-%dx%d", state, pal, size[0], size[1]), func(t *testing.T) {
					shellGolden(t, state, pal, size)
				})
			}
		}
	}
}

func shellGolden(t *testing.T, state, pal string, size [2]int) {
	withPalette(t, pal, false, 1<<24)
	m := inState(t, fixtureShell(t, size[0], size[1]), state)
	m.paint = newPainter(th)
	content := m.View().Content
	plain := m.grid().plain()
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		if got := placed(content, size[0], size[1], method); got != plain {
			t.Fatalf("width method %v places the View otherwise:\n%s\nwant:\n%s", method, got, plain)
		}
	}
	base := fmt.Sprintf("%s-%dx%d-%s", state, size[0], size[1], pal)
	checkGolden(t, base+".txt", plain+"\n")
	checkGolden(t, base+".ansi", content+"\n")
}

// Every surviving agent flag, state look and header item is on a golden
// (busmon Charm spec, "Flags and header items that survive unchanged").
func TestShellGoldensCarryEverySurvivingFlag(t *testing.T) {
	pinLocal(t)
	got := fixtureShell(t, 140, 60).grid().plain()
	for _, w := range []string{
		// agent row flags and state looks
		"busy", "prompt", "update", "model" + GlyphNotEqual, "swapping", "fallback kimi-alt", "held", "quota",
		"gate 1", "for you", "deferred", "escalating", "escalated", "paused", "pilot", "armed", "stale 2",
		"1 dormant (a shows)",
		// header items
		"Charlie [human]", "agent-bus-monitor", "bus ok", "dispatch up", "missions up", "resumes in 1h52",
		"grok 5h", "100% held", "claude 5h", "2 unread", GlyphHuman + " 4 decide", "15:04:05",
		// Activity marks
		GlyphDotFull + " 14:56", GlyphIdle + " 14:52", "(+197)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("no %q on the 140x60 dashboard:\n%s", w, got)
		}
	}
}

// At the wide tier Agents beside For you keeps room for the message: its
// column header shows "message", and every row has text under it, not a lone
// ellipsis.
func TestShellWideAgentsKeepTheMessage(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{120, 30}, {140, 40}} {
		m := fixtureShell(t, size[0], size[1])
		g := m.grid()
		r := m.layout.Agents
		row := func(y int) []string {
			var cells []string
			for x := r.X + 1; x < r.X+r.W-1; x++ {
				if c := g.at(x, y); !c.cont {
					cells = append(cells, c.text)
				}
			}
			return cells
		}
		head := row(r.Y + 1)
		at := strings.Index(strings.Join(head, ""), "message")
		if at < 0 {
			t.Fatalf("%v: Agents header lacks the message column: %q", size, strings.Join(head, ""))
		}
		for y := r.Y + 2; y < r.Y+r.H-1; y++ {
			msg := strings.TrimSpace(strings.Join(row(y)[at:], ""))
			if len([]rune(msg)) < 3 {
				t.Errorf("%v: Agents row %d has no room for its message: %q", size, y, strings.Join(row(y), ""))
			}
		}
	}
}

// goldenPlan is the plan the plan goldens show: every section, one task in
// review with its PR, DONE folded.
func goldenPlan() planState {
	p := boardPlan(
		boardRow("notes", plan.Waiting, plan.Person),
		boardRow("strip", plan.InReview, "coder"),
		boardRow("drawer", plan.InProgress, "coder"),
		boardRow("requests", plan.Upcoming, "coder"),
		boardRow("tag", plan.Upcoming, plan.Person, "requests"),
		boardRow("triage", plan.Done, "coder"),
	)
	p.rows[1].Verdict, p.rows[1].PR, p.rows[1].Reason = "approved", 338, "merge PR 338"
	return p
}

// notifyGolden is the guided setup in a golden state (notify-guided-setup
// §5.4): a fixed fake topic and webhook, neither a secret.
func notifyGolden(t testing.TB, m Model, state string) Model {
	t.Helper()
	toml := ""
	if state == "notify-targets" {
		toml = "[ntfy.phone]\ntopic = \"" + goldenTopic + "\"\n[discord.team]\nwebhook = \"https://discord.com/api/webhooks/1/goldenhook\"\n"
	}
	getenv := settingsEnv(t, "[notify]\non = true\ntargets = [\"phone\"]\n", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1", "XDG_STATE_HOME": t.TempDir()})
	if toml != "" {
		if err := os.WriteFile(filepath.Join(filepath.Dir(config.Path(getenv)), "notify.toml"), []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.cfg.getenv = getenv
	m = stepCmd(t, m, tea.KeyPressMsg(tea.Key{Code: 'C', Text: "C"}))
	p := m.rd.top()
	for i, g := range p.set.groups {
		if g.Group == config.GroupNotifications {
			p.set.group, p.set.keys = i, true
		}
	}
	if state == "notify-targets" { // page C: the set-up lines, then a line per target, phone selected
		p.sel, p.dirty = len(p.set.rows())+len(notifyAssistKinds), true
		m.refresh()
		return m
	}
	kind := "ntfy"
	switch state {
	case "notify-discord":
		kind = "discord"
	case "notify-desktop":
		kind = "desktop"
	}
	s := newNotifySetup(kind, nil, nil, state != "notify-desktop")
	s.topic = goldenTopic
	switch state {
	case "notify-subscribe":
		s.step = 3
	case "notify-qr":
		s.step, s.showCode = 3, true
	case "notify-discord": // the webhook just pasted: masked
		s.field("webhook").val = []rune("https://discord.com/api/webhooks/1/goldenhook")
	case "notify-desktop":
		s.err = "install libnotify (notify-send) first"
	case "notify-result":
		s.step, s.written, s.result, s.resultOK = 4, "phone", "delivered: check your phone", true
	case "notify-failed":
		s.step, s.written, s.result = 4, "phone", "the server refused the token (or this topic needs one)"
	}
	m.push(page{kind: ovNotifySetup, ns: s})
	m.refresh()
	return m
}
