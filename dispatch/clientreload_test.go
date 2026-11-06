package dispatch

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

type updateEntry struct{ Target, Command string }

// setState declares state for agent, keeping its verified identity.
func setState(t *testing.T, b *bus.Bus, agent, state string) {
	t.Helper()
	snaps, err := b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pane := snaps[agent].Pane
	if _, err := b.Status(context.Background(), agent, state, "", bus.AgentIdent{Pane: pane, PID: testPID, PIDStart: testStart}); err != nil {
		t.Fatalf("Status(%s): %v", agent, err)
	}
}

// updateWorld: dev and doc run claude-code 2.1.305 while installed (an
// atomic, so a test opens a new episode) is newer; sentinel is up to date.
func updateWorld(t *testing.T, withSentinel bool, policy *atomic.Value, busy ...string) (*bus.Bus, func() []updateEntry, *atomic.Value) {
	t.Helper()
	b, r := dialRun(t)
	register(t, b, "dev", "p1")
	register(t, b, "doc", "p2")
	for _, a := range busy { // not ready when the episode's directive goes out
		setState(t, b, a, "working")
	}
	if withSentinel {
		register(t, b, "sentinel", "p3")
	}
	var installed atomic.Value
	installed.Store("2.1.306")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	cfg.ClientProbeEvery = 10 * time.Millisecond
	cfg.ReloadPolicy = func() string { return policy.Load().(string) }
	cfg.ClientProbe = func(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
		if s.Pane == "p3" {
			return nil, true
		}
		v := installed.Load().(string)
		return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: v, Hash: "h-" + v, Source: "layout"}, true
	}
	start(t, b, newFakeRT(alwaysReady), cfg)
	updates := func() []updateEntry {
		var out []updateEntry
		for _, m := range entries(t, r, b, "cmd") {
			cmd, _ := m.Values["command"].(string)
			if m.Values["from"] == "dispatch" && strings.HasPrefix(cmd, "client update:") {
				target, _ := m.Values["target"].(string)
				out = append(out, updateEntry{Target: target, Command: cmd})
			}
		}
		return out
	}
	return b, updates, &installed
}

// auto-reload (the person, 2026-11-06): the episode's directive follows the
// reload policy, read at each send: on tells sentinel to reload the ready
// agents itself, propose keeps the proposal, off reloads nothing.
func TestProposeReloadsFollowsThePolicy(t *testing.T) {
	var policy atomic.Value
	policy.Store("on")
	_, updates, installed := updateWorld(t, true, &policy)
	waitFor(t, 3*time.Second, "the on directive", func() bool { return len(updates()) >= 1 })
	for i, c := range []struct {
		policy, version string
		want, not       []string
	}{
		{"on", "2.1.306", []string{"Reload the ready ones now", "agentbus reload <agent>", "master last", "never type into a pane", "dev (2.1.305)"}, []string{"Propose", "propose only"}},
		{"propose", "2.1.307", []string{"Propose a reload", "propose only, never restart or type"}, []string{"Reload the ready ones now"}},
		{"off", "2.1.308", []string{"Reload nothing (the reload policy is off)", "report the agents to master once"}, []string{"Reload the ready ones now", "Propose"}},
	} {
		if i > 0 { // the policy changes, then a new episode: read at this send
			policy.Store(c.policy)
			installed.Store(c.version)
			waitFor(t, 3*time.Second, c.policy+" directive", func() bool { return len(updates()) >= i+1 })
		}
		var got updateEntry
		for _, u := range updates() {
			if strings.Contains(u.Command, c.version+" installed") {
				got = u
			}
		}
		if got.Target != "sentinel" {
			t.Fatalf("%s: target %q (%q)", c.policy, got.Target, got.Command)
		}
		for _, w := range c.want {
			if !strings.Contains(got.Command, w) {
				t.Errorf("%s: lacks %q: %q", c.policy, w, got.Command)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got.Command, w) {
				t.Errorf("%s: says %q: %q", c.policy, w, got.Command)
			}
		}
	}
}

// No sentinel registered: the directive goes to the escalation target, which
// may reload nobody but sentinel, so under on it asks the person, and no
// retry hint ever follows (reviewer-codex on the spec).
func TestProposeReloadsWithoutSentinelAsksThePerson(t *testing.T) {
	var policy atomic.Value
	policy.Store("on")
	b, updates, _ := updateWorld(t, false, &policy, "doc")
	waitFor(t, 3*time.Second, "the directive", func() bool { return len(updates()) >= 1 })
	setState(t, b, "doc", "idle") // ready now: still no hint without sentinel
	time.Sleep(300 * time.Millisecond)
	us := updates()
	if len(us) != 1 {
		t.Fatalf("%d update directives, want 1: %q", len(us), us)
	}
	u := us[0]
	if u.Target != "master" || !strings.Contains(u.Command, "No sentinel registered, so nothing reloads by itself") ||
		!strings.Contains(u.Command, "ask the person") || strings.Contains(u.Command, "Reload the ready ones now") {
		t.Fatalf("directive %+v", u)
	}
}

// The retry hint is only for agents not ready when the episode's directive
// went out: dev, ready then, gets none; doc, working then, gets exactly one
// once it is idle; sentinel never gets one about itself.
func TestRetryOnlyAgentsLaterAtTheEpisode(t *testing.T) {
	var policy atomic.Value
	policy.Store("on")
	b, r := dialRun(t)
	register(t, b, "dev", "p1")
	register(t, b, "doc", "p2")
	register(t, b, "sentinel", "p3")
	setState(t, b, "doc", "working")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	cfg.ClientProbeEvery = 10 * time.Millisecond
	cfg.ReloadPolicy = func() string { return policy.Load().(string) }
	cfg.ClientProbe = func(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
		return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: "2.1.306", Hash: "h", Source: "layout"}, true
	}
	start(t, b, newFakeRT(alwaysReady), cfg)
	hints := func(agent string) int {
		n := 0
		for _, m := range entries(t, r, b, "cmd") {
			cmd, _ := m.Values["command"].(string)
			if m.Values["from"] == "dispatch" && m.Values["target"] == "sentinel" && strings.HasPrefix(cmd, "client update: "+agent+" is ready now") {
				n++
			}
		}
		return n
	}
	waitFor(t, 3*time.Second, "the episode directive", func() bool {
		m, _ := b.QuotaMarks(context.Background())
		return m["update:claude-code:v2.1.306"] == "sent"
	})
	time.Sleep(200 * time.Millisecond)
	if hints("dev") != 0 || hints("doc") != 0 || hints("sentinel") != 0 {
		t.Fatalf("hints before doc is ready: dev %d doc %d sentinel %d", hints("dev"), hints("doc"), hints("sentinel"))
	}
	setState(t, b, "doc", "idle")
	waitFor(t, 3*time.Second, "doc's hint", func() bool { return hints("doc") == 1 })
	time.Sleep(300 * time.Millisecond)
	if hints("doc") != 1 || hints("dev") != 0 || hints("sentinel") != 0 {
		t.Fatalf("hints: dev %d doc %d sentinel %d", hints("dev"), hints("doc"), hints("sentinel"))
	}
	for _, m := range entries(t, r, b, "cmd") {
		if cmd, _ := m.Values["command"].(string); strings.HasPrefix(cmd, "client update: doc is ready now") {
			for _, w := range []string{"claude-code 2.1.306 installed", "it runs 2.1.305", "agentbus reload doc", "never type into a pane"} {
				if !strings.Contains(cmd, w) {
					t.Errorf("hint lacks %q: %q", w, cmd)
				}
			}
		}
	}
}

// Under propose or off, no hint is ever sent.
func TestNoRetryHintUnlessOn(t *testing.T) {
	for _, p := range []string{"propose", "off"} {
		var policy atomic.Value
		policy.Store(p)
		b, updates, _ := updateWorld(t, true, &policy, "doc")
		waitFor(t, 3*time.Second, "the directive", func() bool { return len(updates()) >= 1 })
		setState(t, b, "doc", "idle")
		time.Sleep(300 * time.Millisecond)
		if n := len(updates()); n != 1 {
			t.Fatalf("%s: %d update directives: %q", p, n, updates())
		}
	}
}

// reloadReady is the hint's readiness: what the reload itself would refuse,
// as far as the dispatcher can read it without running anything.
func TestReloadReady(t *testing.T) {
	ok := ReloadFacts{State: "idle"}
	if !reloadReady(ok) {
		t.Fatal("an idle agent with nothing open")
	}
	for name, f := range map[string]ReloadFacts{
		"working":       {State: "working"},
		"observed busy": {State: "idle", Busy: true},
		"accepted task": {State: "idle", Accepted: true},
		"open gate":     {State: "idle", Gates: []string{"g1"}},
		"swapping":      {State: "idle", Swapping: true},
		"on a fallback": {State: "idle", Fallback: true},
		"unknown":       {State: "idle", Unknown: true},
	} {
		if reloadReady(f) {
			t.Errorf("%s: ready", name)
		}
	}
}
