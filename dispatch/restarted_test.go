package dispatch

import (
	"testing"
	"time"
)

// codex-identity-recovery: herdr restored a pane after a reboot, its client
// a new process; the registered pid is gone. When a process of the pane
// carries this very agent's AGENT_BUS_PROJECT and AGENT_BUS_AGENT, it is the
// agent restarted in place: the dispatcher wakes it (the fixed line), and
// its turn proves the new pid. Any other process still escalates.
func TestRestartedInPlaceIsWoken(t *testing.T) {
	restored := func(env map[int]string) Inspection {
		return Inspection{State: "idle", Client: "claude", PIDs: []int{7, 300}, Starts: map[int]uint64{7: 1, 300: 9},
			Env: env, Ready: true}
	}
	base := Input{Obligation: owed(), Snap: snap(), OldestAge: time.Hour, Who: "demo/reviewer-codex"}
	for name, c := range map[string]struct {
		in     Inspection
		who    string
		act    Action
		reason string
	}{
		"same agent restarted": {restored(map[int]string{300: "demo/reviewer-codex"}), "demo/reviewer-codex", ActPrompt, "ready; restarted in place"},
		"another agent":        {restored(map[int]string{300: "demo/coder"}), "demo/reviewer-codex", ActEscalate, "identity mismatch, escalating"},
		"another project":      {restored(map[int]string{300: "other/reviewer-codex"}), "demo/reviewer-codex", ActEscalate, "identity mismatch, escalating"},
		"no environment read":  {restored(nil), "demo/reviewer-codex", ActEscalate, "identity mismatch, escalating"},
		"no name to compare":   {restored(map[int]string{300: "demo/reviewer-codex"}), "", ActEscalate, "identity mismatch, escalating"},
		// the registered process is still there with its start: never "restarted"
		"the registered process lives": {Inspection{State: "idle", Client: "claude", PIDs: []int{100}, Starts: map[int]uint64{100: 5},
			Env: map[int]string{100: "demo/reviewer-codex"}, Ready: true}, "demo/reviewer-codex", ActPrompt, "ready"},
		// a restarted agent still waits for its pane to be ready
		"restarted but working": {Inspection{State: "working", Client: "claude", PIDs: []int{300}, Starts: map[int]uint64{300: 9},
			Env: map[int]string{300: "demo/reviewer-codex"}}, "demo/reviewer-codex", ActDefer, "agent working"},
	} {
		x := base
		x.In, x.Who = c.in, c.who
		if c.act == ActDefer {
			x.OldestAge = time.Minute
		}
		d := Decide(testPolicy, x)
		if d.Action != c.act || d.Reason != c.reason {
			t.Errorf("%s: %v %q, want %v %q", name, d.Action, d.Reason, c.act, c.reason)
		}
	}
}

// The environment of a process names its agent only with both variables,
// valid names, read from its NUL-separated environ.
func TestAgentFromEnviron(t *testing.T) {
	for raw, want := range map[string]string{
		"HOME=/h\x00AGENT_BUS_PROJECT=demo\x00AGENT_BUS_AGENT=reviewer-codex\x00": "demo/reviewer-codex",
		"AGENT_BUS_AGENT=coder\x00":                          "",
		"AGENT_BUS_PROJECT=demo\x00AGENT_BUS_AGENT=Bad*\x00": "",
		"AGENT_BUS_PROJECT=demo\x00AGENT_BUS_AGENT=coder":    "demo/coder", // no trailing NUL
		"": "",
	} {
		if got := agentFromEnviron([]byte(raw)); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

// The loop on a restored session (codex-identity-recovery): the registered
// pid is gone, a new process of the same agent runs the pane: the fixed wake
// line is typed; a process of another agent gets nothing.
func TestRunWakesAnAgentRestartedInPlace(t *testing.T) {
	for _, c := range []struct {
		name, env string
		woken     bool
	}{{"same agent", "coder", true}, {"another agent", "foureyes", false}} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := dialRun(t)
			register(t, b, "coder", "p-coder")
			send(t, b, "coder", "review it")
			who := b.Project() + "/" + c.env
			rt := newFakeRT(func(string, int) Inspection {
				return Inspection{State: "idle", Client: "claude", PIDs: []int{424242}, Starts: map[int]uint64{424242: 77},
					Env: map[int]string{424242: who}, Ready: true}
			})
			start(t, b, rt, fastConfig())
			if c.woken {
				waitFor(t, 3*time.Second, "a wake line", func() bool { return len(rt.Prompts()) == 1 })
				return
			}
			waitFor(t, 3*time.Second, "an inspection", func() bool { return rt.Inspections("p-coder") > 0 })
			time.Sleep(200 * time.Millisecond)
			if n := len(rt.Prompts()); n != 0 {
				t.Fatalf("woke a pane where another agent runs: %d prompts", n)
			}
		})
	}
}
