package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/personmem"
)

// Person-memory step 4, part b (spec 2026-11-21-person-memory-design r2,
// §4.4): the Memory page's actions, each the person's own act on this
// host: a adds words the person types (busmon's source, §4.3.1), x forgets
// an observation after a confirmation, e opens the topic file in $EDITOR
// and checks it on save.

// memShell is busmon on the Memory page (F7) over getenv's store, on its host.
func memShell(t *testing.T, getenv func(string) string) Model {
	t.Helper()
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st, getenv: getenv, memoryLocal: func() (bool, string) { return true, "" }})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyF7})))
	if p := m.rd.top(); p == nil || p.kind != ovMemory {
		t.Fatalf("F7: %+v", p)
	}
	return m
}

// memPress runs one key's command, as the program would.
func memPress(t *testing.T, m Model, k any) Model {
	t.Helper()
	var key tea.Key
	switch k := k.(type) {
	case rune:
		key = tea.Key{Code: k, Text: string(k)}
	case tea.Key:
		key = k
	}
	return run(t, m, m.update(tea.KeyPressMsg(key)))
}

func memObs(t *testing.T, getenv func(string) string, topic string) []personmem.Observation {
	t.Helper()
	s, err := personmem.Open(getenv)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := s.Load(topic)
	if err != nil {
		return nil
	}
	return tp.Obs
}

func withEnv(getenv func(string) string, k, v string) func(string) string {
	return func(x string) string {
		if x == k {
			return v
		}
		return getenv(x)
	}
}

func TestParseMemoryAdd(t *testing.T) {
	for _, c := range []struct {
		text, topic, typ, imp, quote, supersedes string
		keepBoth                                 bool
		err                                      string
	}{
		{text: "notifications preference standard\nno sound\n at night ", topic: "notifications", typ: "preference", imp: "standard", quote: "no sound at night"},
		{text: "merge-policy decision critical keep-both\nboth hold", topic: "merge-policy", typ: "decision", imp: "critical", quote: "both hold", keepBoth: true},
		{text: "merge-policy decision critical supersedes=20261121-b1\nmain first", topic: "merge-policy", typ: "decision", imp: "critical", quote: "main first", supersedes: "20261121-b1"},
		{text: "notifications preference\nwords", err: "topic type importance"},
		{text: "notifications preference standard", err: "your words"},
		{text: "notifications preference standard extra\nwords", err: "keep-both or supersedes="},
	} {
		r, err := parseMemoryAdd(c.text)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.text, err, c.err)
			}
			continue
		}
		if err != nil || r.Topic != c.topic || r.Type != c.typ || r.Importance != c.imp || r.Quote != c.quote || r.KeepBoth != c.keepBoth || r.Supersedes != c.supersedes {
			t.Errorf("%q: %+v %v", c.text, r, err)
		}
	}
}

// a: the compose window prefilled with the selected topic; Ctrl+S remembers
// the person's words from busmon, and the page shows them.
func TestMemoryAdd(t *testing.T) {
	getenv := memStore(t)
	m := memShell(t, getenv)
	m = keys(t, m, kDown, 'a') // notifications
	c := m.ui.compose
	if c == nil || c.kind != composeMemory || c.area.value() != "notifications preference standard\n" {
		t.Fatalf("a: compose %+v", c)
	}
	if g := m.grid().plain(); !strings.Contains(g, "Remember") || !strings.Contains(g, "topic type importance") {
		t.Fatalf("the window\n%s", g)
	}
	m = typeText(t, m, "silent weekends")
	m = memPress(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if m.ui.compose != nil || m.ui.flashErr || !strings.Contains(m.ui.flash, "remembered") {
		t.Fatalf("compose %+v flash %q", m.ui.compose, m.ui.flash)
	}
	obs := memObs(t, getenv, "notifications")
	if len(obs) != 2 || obs[1].Quote != "silent weekends" || !strings.Contains(obs[1].Said, "by charlie from busmon") {
		t.Fatalf("stored %+v", obs)
	}
	if p := m.rd.top(); !strings.Contains(plainLines(p.body), "silent weekends") {
		t.Fatalf("the page is not read again:\n%s", plainLines(p.body))
	}
}

// A refused remember keeps the window and its text, and says why.
func TestMemoryAddRefusedKeepsTheWindow(t *testing.T) {
	getenv := memStore(t)
	m := memShell(t, getenv)
	m = keys(t, m, 'a') // merge-policy holds a decision
	m.ui.compose.area.set("merge-policy decision critical\nmain first")
	m = memPress(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if m.ui.compose == nil || !m.ui.flashErr || !strings.Contains(m.ui.flash, "already holds the decision") {
		t.Fatalf("compose %+v flash %q", m.ui.compose, m.ui.flash)
	}
	if len(memObs(t, getenv, "merge-policy")) != 2 {
		t.Fatal("a refused remember wrote")
	}
	m.ui.compose.area.set("merge-policy preference\nno importance")
	m = memPress(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if m.ui.compose == nil || !m.ui.flashErr || !strings.Contains(m.ui.flash, "topic type importance") {
		t.Fatalf("compose %+v flash %q", m.ui.compose, m.ui.flash)
	}
	// Esc keeps the draft for the next a.
	m = keys(t, m, kEsc)
	if m.ui.compose != nil {
		t.Fatal("esc kept the window")
	}
	m = keys(t, m, 'a')
	if c := m.ui.compose; c == nil || c.area.value() != "merge-policy preference\nno importance" {
		t.Fatalf("the draft is lost: %+v", m.ui.compose)
	}
}

// x on a topic's page: the selected observation, after y; any other key keeps it.
func TestMemoryForget(t *testing.T) {
	getenv := memStore(t)
	m := memShell(t, getenv)
	m = keys(t, m, kEnter) // merge-policy, two observations
	p := m.rd.top()
	if p.kind != ovMemoryTopic || len(p.oitems) != 2 {
		t.Fatalf("topic page %+v", p)
	}
	second := memObs(t, getenv, "merge-policy")[1]
	m = keys(t, m, kDown, 'x')
	if !strings.Contains(m.ui.flash, second.ID) || !strings.Contains(m.ui.flash, "y forgets") {
		t.Fatalf("x asks nothing: %q", m.ui.flash)
	}
	m = memPress(t, m, 'n')
	if len(memObs(t, getenv, "merge-policy")) != 2 || !strings.Contains(m.ui.flash, "kept") {
		t.Fatalf("n forgot, or said %q", m.ui.flash)
	}
	m = keys(t, m, 'x')
	m = memPress(t, m, 'y')
	obs := memObs(t, getenv, "merge-policy")
	if len(obs) != 1 || obs[0].ID == second.ID || !strings.Contains(m.ui.flash, "forgot "+second.ID) {
		t.Fatalf("after y: %+v flash %q", obs, m.ui.flash)
	}
	if body := plainLines(m.rd.top().body); strings.Contains(body, "squash merges") {
		t.Fatalf("the page is not read again:\n%s", body)
	}
}

// Every action is the person's: refused in an agent's environment, and on a
// machine that is not the memory's host; nothing is written.
func TestMemoryActionsRefused(t *testing.T) {
	getenv := memStore(t)
	agent := withEnv(getenv, "AGENT_BUS_AGENT", "coder")
	for _, k := range []rune{'a', 'e'} {
		m := memShell(t, agent)
		m = memPress(t, m, k)
		if m.ui.compose != nil || !m.ui.flashErr || !strings.Contains(m.ui.flash, "the person's act") {
			t.Fatalf("%c in an agent's env: compose %+v flash %q", k, m.ui.compose, m.ui.flash)
		}
	}
	m := memShell(t, agent)
	m = keys(t, m, kEnter)
	m = memPress(t, m, 'x')
	if !strings.Contains(m.ui.flash, "the person's act") {
		t.Fatalf("x in an agent's env: %q", m.ui.flash)
	}
	m = memPress(t, m, 'y')
	if len(memObs(t, getenv, "merge-policy")) != 2 {
		t.Fatal("forgot in an agent's env")
	}

	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	m = newShell(shellConfig{project: "demo", self: "charlie", st: st, getenv: getenv, memoryLocal: func() (bool, string) { return false, "elsewhere-host" }})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyF7})))
	m = memPress(t, m, 'a')
	if m.ui.compose != nil || !strings.Contains(m.ui.flash, "elsewhere-host") {
		t.Fatalf("a off the host: compose %+v flash %q", m.ui.compose, m.ui.flash)
	}
}

// e: the editor is $VISUAL, else $EDITOR, else vi, with its own words, on the topic's file.
func TestMemoryEditorCommand(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := editorArgv(getenv, "/m/x.md"); strings.Join(got, " ") != "vi /m/x.md" {
		t.Fatalf("none: %v", got)
	}
	env["EDITOR"] = "nano"
	if got := editorArgv(getenv, "/m/x.md"); strings.Join(got, " ") != "nano /m/x.md" {
		t.Fatalf("EDITOR: %v", got)
	}
	env["VISUAL"] = "code -w"
	if got := editorArgv(getenv, "/m/x.md"); strings.Join(got, " ") != "code -w /m/x.md" {
		t.Fatalf("VISUAL: %v", got)
	}
}

// e hands the terminal to the editor; on its return the check runs: clean,
// the edit is committed; not, the findings are said and the file stays.
func TestMemoryEditDone(t *testing.T) {
	getenv := memStore(t)
	m := memShell(t, getenv)
	if cmd := m.update(tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"})); cmd == nil {
		t.Fatal("e started no editor")
	}
	s, _ := personmem.Open(getenv)
	path := s.Dir + "/notifications.md"
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "importance: standard", "importance: critical", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	m = run(t, m, m.update(memoryEditMsg{topic: "notifications"}))
	if m.ui.flashErr || !strings.Contains(m.ui.flash, "notifications edited") {
		t.Fatalf("clean edit: %q", m.ui.flash)
	}
	if obs := memObs(t, getenv, "notifications"); obs[0].Importance != "critical" {
		t.Fatalf("the edit is not read: %+v", obs)
	}
	if body := plainLines(m.rd.top().body); !strings.Contains(body, "notifications") {
		t.Fatalf("page:\n%s", body)
	}
	broken := strings.Replace(string(b), "  quote: ", "  supersedes: 20200101-1-0\n  quote: ", 1)
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	m = run(t, m, m.update(memoryEditMsg{topic: "notifications"}))
	if !m.ui.flashErr || !strings.Contains(m.ui.flash, "not committed") || !strings.Contains(m.ui.flash, "notifications.md") {
		t.Fatalf("broken edit: %q", m.ui.flash)
	}
	if after, _ := os.ReadFile(path); string(after) != broken {
		t.Fatal("the file is not left as saved")
	}
	m = run(t, m, m.update(memoryEditMsg{topic: "notifications", err: os.ErrNotExist}))
	if !m.ui.flashErr || !strings.Contains(m.ui.flash, "editor") {
		t.Fatalf("editor failed: %q", m.ui.flash)
	}
	_ = context.Background
}
