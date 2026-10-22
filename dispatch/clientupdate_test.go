package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
)

// An installed client newer than the one an agent runs is observed (the
// additive update field), and sentinel gets one directive per update episode
// (client + installed fingerprint), not one per agent or per pass.
func TestObserveClientUpdateOncePerEpisode(t *testing.T) {
	b, r := dialRun(t)
	ctx := context.Background()
	register(t, b, "dev", "p1")
	register(t, b, "doc", "p2")
	register(t, b, "sentinel", "p3")
	var installed atomic.Value
	installed.Store("2.1.306")
	cfg := fastConfig()
	cfg.Observe = 50 * time.Millisecond
	cfg.ClientProbeEvery = 100 * time.Millisecond
	cfg.ClientProbe = func(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
		if s.Pane == "p3" {
			return nil, true // judged, up to date
		}
		v := installed.Load().(string)
		return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: v, Hash: "h-" + v, Source: "layout"}, true
	}
	start(t, b, newFakeRT(alwaysReady), cfg)

	directives := func() []string {
		var out []string
		for _, m := range entries(t, r, b, "cmd") {
			if cmd, _ := m.Values["command"].(string); m.Values["from"] == "dispatch" && m.Values["target"] == "sentinel" && strings.Contains(cmd, "installed") {
				out = append(out, cmd)
			}
		}
		return out
	}
	var obs map[string]bus.Observation
	waitFor(t, 5*time.Second, "updates observed", func() bool {
		var err error
		obs, err = b.Observed(ctx)
		return err == nil && obs["dev"].Update != nil && obs["doc"].Update != nil
	})
	if u := obs["dev"].Update; u.Running != "2.1.305" || u.Installed != "2.1.306" || u.Since == 0 {
		t.Fatalf("dev update %+v", u)
	}
	if obs["sentinel"].Update != nil || !obs["sentinel"].ClientJudged || !obs["dev"].ClientJudged {
		t.Fatalf("judged: sentinel %+v dev %+v", obs["sentinel"], obs["dev"])
	}
	since := obs["dev"].Update.Since
	waitFor(t, 3*time.Second, "one directive", func() bool { return len(directives()) == 1 })
	time.Sleep(300 * time.Millisecond) // several passes
	ds := directives()
	if len(ds) != 1 || !strings.Contains(ds[0], "dev") || !strings.Contains(ds[0], "doc") || !strings.Contains(ds[0], "2.1.306") {
		t.Fatalf("directives %q", ds)
	}
	if o, _ := b.Observed(ctx); o["dev"].Update.Since != since {
		t.Fatalf("since moved: %d then %d", since, o["dev"].Update.Since)
	}
	installed.Store("2.1.307") // a second update: a new episode
	waitFor(t, 3*time.Second, "a second directive", func() bool { return len(directives()) == 2 })
}

// The probe is throttled per agent: within the interval, no second look.
func TestClientProbeThrottle(t *testing.T) {
	calls := 0
	l := &loop{cfg: Config{ClientProbe: func(bus.AgentSnapshot) (*bus.ClientUpdate, bool) { calls++; return nil, true }}}
	s := bus.AgentSnapshot{PID: 7}
	for i := 0; i < 3; i++ {
		l.probeClient("dev", s)
	}
	if calls != 1 {
		t.Fatalf("%d probes within a minute", calls)
	}
	s.PID = 8 // another process: looked at at once
	l.probeClient("dev", s)
	if calls != 2 {
		t.Fatalf("a new process was not probed: %d", calls)
	}
}

// The real probe on a launch fingerprint: a switched symlink is an update
// with both labels; an agent without a fingerprint and not on Claude Code
// cannot be judged.
func TestRealClientProbeFingerprint(t *testing.T) {
	dir := t.TempDir()
	v := filepath.Join(dir, "versions")
	os.MkdirAll(v, 0o755)
	os.WriteFile(filepath.Join(v, "2.1.305"), []byte("a"), 0o755)
	os.WriteFile(filepath.Join(v, "2.1.306"), []byte("b"), 0o755)
	link := filepath.Join(dir, "claude")
	os.Symlink(filepath.Join(v, "2.1.305"), link)
	fp, err := clientfp.Take(link)
	if err != nil {
		t.Fatal(err)
	}
	s := bus.AgentSnapshot{Client: "claude-code", PID: 1, ClientFP: fp.Encode()}
	if u, known := realClientProbe(s); u != nil || !known {
		t.Fatalf("up to date: %+v %v", u, known)
	}
	os.Remove(link)
	os.Symlink(filepath.Join(v, "2.1.306"), link)
	u, known := realClientProbe(s)
	if !known || u == nil || u.Running != "2.1.305" || u.Installed != "2.1.306" || u.Source != "fingerprint" || u.Hash == "" {
		t.Fatalf("updated: %+v %v", u, known)
	}
	if u, known := realClientProbe(bus.AgentSnapshot{Client: "codex", PID: 1}); u != nil || known {
		t.Fatalf("codex without a fingerprint: %+v %v", u, known)
	}
}

// A mixed team at rollout: one agent judged by its launch fingerprint, one by
// the Claude Code layout, a third through another path (another fingerprint
// hash), all on the same installed version: one update, one directive, over
// many passes.
func TestClientUpdateMixedSourcesOneDirective(t *testing.T) {
	b, r := dialRun(t)
	register(t, b, "dev", "p1")
	register(t, b, "doc", "p2")
	register(t, b, "ops", "p3")
	register(t, b, "sentinel", "p4")
	cfg := fastConfig()
	cfg.Observe = 30 * time.Millisecond
	cfg.ClientProbeEvery = 10 * time.Millisecond
	cfg.ClientProbe = func(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
		switch s.Pane {
		case "p1":
			return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: "2.1.306", Hash: "fp-aaa", Source: "fingerprint"}, true
		case "p2":
			return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.305", Installed: "2.1.306", Hash: "layout-2.1.306", Source: "layout"}, true
		case "p3":
			return &bus.ClientUpdate{Client: "claude-code", Running: "2.1.304", Installed: "2.1.306", Hash: "fp-bbb", Source: "fingerprint"}, true
		}
		return nil, true
	}
	start(t, b, newFakeRT(alwaysReady), cfg)
	count := func() int {
		n := 0
		for _, m := range entries(t, r, b, "cmd") {
			if cmd, _ := m.Values["command"].(string); m.Values["from"] == "dispatch" && strings.HasPrefix(cmd, "client update:") {
				n++
			}
		}
		return n
	}
	waitFor(t, 3*time.Second, "the directive", func() bool { return count() >= 1 })
	time.Sleep(500 * time.Millisecond) // well over ten passes
	if n := count(); n != 1 {
		t.Fatalf("%d directives for one update", n)
	}
	for _, m := range entries(t, r, b, "cmd") {
		if cmd, _ := m.Values["command"].(string); strings.HasPrefix(cmd, "client update:") {
			for _, want := range []string{"2.1.306 installed", "dev (2.1.305)", "doc (2.1.305)", "ops (2.1.304)"} {
				if !strings.Contains(cmd, want) {
					t.Fatalf("directive lacks %q: %q", want, cmd)
				}
			}
		}
	}
}
