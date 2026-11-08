package health

import (
	"strings"
	"testing"
	"time"
)

// idle-implementer (spec docs/superpowers/specs/2026-11-08-idle-implementer-design.md §2,
// the person's answers: 15 minutes, the implement skill, requests only).

var iiNow = time.Date(2026, 11, 8, 9, 30, 0, 0, time.UTC)

func iiInput(agents ...StallAgent) IdleImplementerInput {
	return IdleImplementerInput{Now: iiNow, After: 15 * time.Minute, Implementers: []string{"coder", "coder2"},
		Agents: agents, Held: map[string]bool{}, Requested: map[string][]string{"coder": {"agy-client-impl"}}}
}

func idleFor(name string, d time.Duration) StallAgent {
	return StallAgent{Name: name, State: "idle", DeclaredAt: iiNow.Add(-d), Pane: PaneIdle}
}

// The case the rule names: an implementer idle past the threshold, its pane
// not busy, not held, with a request addressed to it in state requested.
func TestIdleImplementerWarns(t *testing.T) {
	out := EvaluateIdleImplementer(iiInput(idleFor("coder", 22*time.Minute), StallAgent{Name: "master", State: "working", DeclaredAt: iiNow}))
	if out.Status != Warn || out.Detail != "coder idle 22m; requested: agy-client-impl" {
		t.Fatalf("%+v", out)
	}
}

// Every other case of §2 is OK, with the reason; a failed read is Unknown;
// 0 is off.
func TestIdleImplementerOK(t *testing.T) {
	for name, c := range map[string]struct {
		in   IdleImplementerInput
		want string
	}{
		"working":      {iiInput(StallAgent{Name: "coder", State: "working", DeclaredAt: iiNow.Add(-time.Hour)}), "coder working"},
		"blocked":      {iiInput(StallAgent{Name: "coder", State: "blocked", DeclaredAt: iiNow.Add(-time.Hour)}), "coder blocked"},
		"waiting":      {iiInput(StallAgent{Name: "coder", State: "waiting", DeclaredAt: iiNow.Add(-time.Hour)}), "coder waiting"},
		"idle only 6m": {iiInput(idleFor("coder", 6*time.Minute)), "coder idle only 6m"},
		"busy pane":    {func() IdleImplementerInput { a := idleFor("coder", time.Hour); a.Pane = PaneBusy; return iiInput(a) }(), "coder busy (not declared)"},
		"dormant":      {func() IdleImplementerInput { a := idleFor("coder", time.Hour); a.Dormant = true; return iiInput(a) }(), "no implementer registered"},
		"held": {func() IdleImplementerInput {
			in := iiInput(idleFor("coder", time.Hour))
			in.Held["coder"] = true
			return in
		}(), "coder held by its quota"},
		"nothing for it": {func() IdleImplementerInput { in := iiInput(idleFor("coder", time.Hour)); in.Requested = nil; return in }(), "nothing requested for coder"},
		"another's": {func() IdleImplementerInput {
			in := iiInput(idleFor("coder", time.Hour))
			in.Requested = map[string][]string{"foureyes": {"x"}}
			return in
		}(), "nothing requested for coder"},
		"not implementer": {func() IdleImplementerInput {
			in := iiInput(idleFor("master", time.Hour))
			in.Requested = map[string][]string{"master": {"x"}}
			return in
		}(), "no implementer registered"},
		"no implementer": {func() IdleImplementerInput {
			in := iiInput(idleFor("coder", time.Hour))
			in.Implementers = nil
			return in
		}(), "no role has the implement skill"},
	} {
		out := EvaluateIdleImplementer(c.in)
		if out.Status != OK || out.Detail != c.want {
			t.Errorf("%s: %+v, want OK %q", name, out, c.want)
		}
	}
	in := iiInput(idleFor("coder", time.Hour))
	in.ReadErr = "board unavailable"
	if out := EvaluateIdleImplementer(in); out.Status != Unknown || out.Detail != "board unavailable" {
		t.Errorf("read error: %+v", out)
	}
	in = iiInput(idleFor("coder", time.Hour))
	in.After = 0
	if out := EvaluateIdleImplementer(in); out.Status != Unknown || out.Detail != "off (idle_after = 0)" {
		t.Errorf("off: %+v", out)
	}
}

// Two implementers passed over: both named, each with its requests (three at
// most, then +N); one at work and one passed over is still a warn.
func TestIdleImplementerTwo(t *testing.T) {
	in := iiInput(idleFor("coder", 30*time.Minute), idleFor("coder2", 3*time.Hour))
	in.Requested = map[string][]string{"coder": {"a"}, "coder2": {"d", "b", "c", "e"}}
	out := EvaluateIdleImplementer(in)
	if out.Status != Warn || out.Detail != "coder idle 30m; requested: a; coder2 idle 3h; requested: b, c, d +1" {
		t.Fatalf("%+v", out)
	}
	in.Agents[0].State = "working"
	if out := EvaluateIdleImplementer(in); out.Status != Warn || strings.Contains(out.Detail, "coder idle") {
		t.Fatalf("one at work: %+v", out)
	}
}

// §3: one waking directive to master per episode: the implementer and its
// sorted requested ids. The same facts on the next pass are the same
// episode; a new request, or another implementer, is a new one.
func TestIdleImplementerWakes(t *testing.T) {
	in := iiInput(idleFor("coder", 22*time.Minute))
	w := IdleImplementerWakes(in, "master")
	if len(w) != 1 || w[0].Target != "master" || w[0].Mark != "stall-implementer:coder" ||
		w[0].Text != "Stall guard: coder idle 22m while requested for it: agy-client-impl. Dispatch the next independent item (no stacked PRs: one PR per step from origin/main), or tell coder why it waits." {
		t.Fatalf("%+v", w)
	}
	in.Agents[0].DeclaredAt = iiNow.Add(-40 * time.Minute) // later pass, same facts
	if again := IdleImplementerWakes(in, "master"); again[0].Episode != w[0].Episode {
		t.Fatal("the same facts are a new episode")
	}
	in.Requested["coder"] = []string{"agy-client-impl", "notify-impl"}
	if again := IdleImplementerWakes(in, "master"); again[0].Episode == w[0].Episode {
		t.Fatal("a new request is the same episode")
	}
	in = iiInput(idleFor("coder", 22*time.Minute), idleFor("coder2", 22*time.Minute))
	in.Requested["coder2"] = []string{"agy-client-impl"}
	two := IdleImplementerWakes(in, "master")
	// each its own mark: one {p}:quota field holds one episode, so a shared
	// mark would alternate and wake master on every pass
	if len(two) != 2 || two[0].Episode == two[1].Episode || two[0].Mark == two[1].Mark {
		t.Fatalf("two implementers: %+v", two)
	}
	in.Agents[0].State, in.Agents[1].State = "working", "working"
	if none := IdleImplementerWakes(in, "master"); len(none) != 0 {
		t.Fatalf("no warn, yet %+v", none)
	}
}

// Off, or a failed read: no wake, even with an implementer passed over in
// the facts (the warn guards the wake, not the facts).
func TestIdleImplementerNoWakeOffOrUnread(t *testing.T) {
	in := iiInput(idleFor("coder", time.Hour))
	in.After = 0
	if w := IdleImplementerWakes(in, "master"); len(w) != 0 {
		t.Errorf("off: %+v", w)
	}
	in = iiInput(idleFor("coder", time.Hour))
	in.ReadErr = "board unavailable"
	if w := IdleImplementerWakes(in, "master"); len(w) != 0 {
		t.Errorf("unread: %+v", w)
	}
}
