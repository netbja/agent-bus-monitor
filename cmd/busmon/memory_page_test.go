package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/personmem"
)

// Person-memory step 4, part a (spec 2026-11-21-person-memory-design r2,
// §4.4): busmon's Memory page, read only: the topics with their
// observations, Enter a topic, and on a machine that is not the host, what
// it cannot show and where it lives.

// memStore writes two topics through personmem (busmon's source) under a
// fresh XDG_DATA_HOME and returns its getenv.
func memStore(t *testing.T) func(string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	env := map[string]string{"XDG_DATA_HOME": t.TempDir(), "HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	s, err := personmem.Open(getenv)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	for _, r := range []personmem.Request{
		{Topic: "merge-policy", Type: "decision", Importance: "critical", Busmon: true, At: at, By: "charlie", Quote: "dev first, main by promotion", Kind: "practice"},
		{Topic: "merge-policy", Type: "preference", Importance: "minor", Busmon: true, At: at.Add(time.Minute), By: "charlie", Quote: "squash merges"},
		{Topic: "notifications", Type: "preference", Importance: "standard", Busmon: true, At: at.Add(2 * time.Minute), By: "charlie", Quote: "no sound at night"},
	} {
		if _, err := s.Remember(context.Background(), nil, r); err != nil {
			t.Fatal(err)
		}
	}
	return getenv
}

func TestMemoryPageLists(t *testing.T) {
	v := readMemory(memStore(t), true, "")
	title, body, items := memoryPage(v, 0, 100)
	text := plainLines(body)
	if !strings.Contains(title, "Memory") || !strings.Contains(title, "2 topics") || len(items) != 2 {
		t.Fatalf("title %q items %+v", title, items)
	}
	for _, want := range []string{"merge-policy", "practice", "2", "notifications", "1", "squash merges", "no sound at night"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	if items[0].topic != "merge-policy" || items[1].topic != "notifications" {
		t.Fatalf("order %+v", items)
	}
}

func TestMemoryTopicPage(t *testing.T) {
	v := readMemory(memStore(t), true, "")
	title, body, _ := memoryTopicPage(v, "merge-policy", 0, 100)
	text := plainLines(body)
	if !strings.Contains(title, "merge-policy") {
		t.Fatalf("title %q", title)
	}
	for _, want := range []string{"decision", "critical", "dev first, main by promotion", "squash merges", "by charlie from busmon"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	if _, body, _ := memoryTopicPage(v, "gone", 0, 100); !strings.Contains(plainLines(body), "no topic gone") {
		t.Fatalf("a missing topic: %s", plainLines(body))
	}
}

// Not the host (§4.4, the person's answer 3): the page says where the memory
// lives and offers nothing.
func TestMemoryPageNotTheHost(t *testing.T) {
	v := readMemory(memStore(t), false, "vdr")
	if len(v.topics) != 0 || v.err != "" {
		t.Fatalf("read off the host: %+v", v)
	}
	title, body, items := memoryPage(v, 0, 100)
	if len(items) != 0 || !strings.Contains(plainLines(body), "lives on vdr") || strings.Contains(plainLines(body), "merge-policy") || !strings.Contains(title, "Memory") {
		t.Fatalf("%q\n%s", title, plainLines(body))
	}
	if _, body, _ := memoryPage(readMemory(memStore(t), false, ""), 0, 100); !strings.Contains(plainLines(body), "the bus's host") {
		t.Fatalf("no host name: %s", plainLines(body))
	}
}

// An empty store says how a fact gets there; a file that does not parse is
// listed as such.
func TestMemoryPageEmptyAndBroken(t *testing.T) {
	env := map[string]string{"XDG_DATA_HOME": t.TempDir(), "HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	if _, body, items := memoryPage(readMemory(getenv, true, ""), 0, 100); len(items) != 0 || !strings.Contains(plainLines(body), "nothing remembered yet") {
		t.Fatalf("empty: %s", plainLines(body))
	}
	getenv = memStore(t)
	s, _ := personmem.Open(getenv)
	if err := os.WriteFile(s.Dir+"/broken.md", []byte("not a topic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, body, _ := memoryPage(readMemory(getenv, true, ""), 0, 100); !strings.Contains(plainLines(body), "broken") || !strings.Contains(plainLines(body), "does not parse") {
		t.Fatalf("broken: %s", plainLines(body))
	}
}

// Which machine is the host: the one the project's services report, else the
// one holding the project's checkout.
func TestMemoryHost(t *testing.T) {
	recs := map[string]bus.ServiceRecord{"dispatch": {State: "running", Host: "vdr"}}
	for _, c := range []struct {
		recs     map[string]bus.ServiceRecord
		me       string
		checkout bool
		local    bool
		host     string
	}{
		{recs, "vdr", false, true, "vdr"},
		{recs, "laptop", true, false, "vdr"},
		{nil, "laptop", true, true, ""},
		{nil, "laptop", false, false, ""},
	} {
		if local, host := memoryHere(c.recs, c.me, c.checkout); local != c.local || host != c.host {
			t.Errorf("%+v: %v %q", c, local, host)
		}
	}
}

// F7 opens the page; Enter on a topic opens it.
func TestMemoryPageKeys(t *testing.T) {
	getenv := memStore(t)
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st, getenv: getenv, memoryLocal: func() (bool, string) { return true, "" }})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyF7})))
	p := m.rd.top()
	if p == nil || p.kind != ovMemory || !strings.Contains(plainLines(p.body), "merge-policy") {
		t.Fatalf("F7: %+v", p)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEnter})
	if p = m.rd.top(); p.kind != ovMemoryTopic || p.key != "merge-policy" || !strings.Contains(plainLines(p.body), "squash merges") {
		t.Fatalf("enter: %+v", p)
	}
}

// foureyes: the production rule, with no injected answer: the services'
// record names another machine, so the page reads nothing and names it; the
// same record naming this machine reads the store.
func TestMemoryPageHostRuleInProduction(t *testing.T) {
	getenv := memStore(t)
	me, _ := os.Hostname()
	open := func(host string) *page {
		t.Helper()
		st := &shared{agents: map[string]*agentState{}, person: "charlie", svcRecs: map[string]bus.ServiceRecord{"dispatch": {State: "running", Host: host}}}
		m := newShell(shellConfig{project: "demo", self: "charlie", st: st, getenv: getenv})
		m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
		m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
		m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyF7})))
		return m.rd.top()
	}
	if p := open("elsewhere-host"); p == nil || len(p.mem.topics) != 0 || !strings.Contains(plainLines(p.body), "lives on elsewhere-host") {
		t.Fatalf("another host: %+v", p)
	}
	if p := open(me); p == nil || len(p.mem.topics) != 2 || !strings.Contains(plainLines(p.body), "merge-policy") {
		t.Fatalf("this host: %+v", p)
	}
}
