package bus

import "testing"

// cleaning-up-agents §3 (spec docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md):
// one judge, pure: running with a live terminal or no holder read; parked
// with a park record; else stopped when optional and owing nothing, lost
// otherwise, its reason first (§10); an unknown input fails closed (lost).
func TestPresence(t *testing.T) {
	known := Owes{} // owes nothing, every item read
	gone := func(o Owes) PresenceIn { return PresenceIn{Pane: "k/t1", TermsRead: true, Owes: o} }
	for name, c := range map[string]struct {
		in            PresenceIn
		state, reason string
	}{
		"live":                 {PresenceIn{Pane: "k/t1", TermsRead: true, Live: true, Owes: Owes{Tasks: []string{"a (accepted)"}}}, PresenceRunning, ""},
		"holder not read":      {PresenceIn{Pane: "k/t1", Owes: Owes{Unknown: []string{"board"}}}, PresenceRunning, ""},
		"no terminal ever":     {PresenceIn{TermsRead: true}, PresenceRunning, ""},
		"parked":               {PresenceIn{Pane: "k/t1", TermsRead: true, Parked: true, Owes: Owes{Messages: 2}}, PresenceParked, ""},
		"park record unread":   {PresenceIn{Pane: "k/t1", TermsRead: true, ParkUnknown: true}, PresenceLost, "unknown: park record"},
		"owes nothing":         {gone(known), PresenceStopped, ""},
		"required":             {PresenceIn{Pane: "k/t1", TermsRead: true, Required: true, SelfRepair: true}, PresenceLost, "required: self-repair relaunches it"},
		"required, repair off": {PresenceIn{Pane: "k/t1", TermsRead: true, Required: true}, PresenceLost, "required: agentbus up brings it back"},
		"owns one":             {gone(Owes{Tasks: []string{"a (accepted)"}}), PresenceLost, "owns a (accepted)"},
		"owns three":           {gone(Owes{Tasks: []string{"a (accepted)", "b (review)", "c (blocked)"}, Messages: 1}), PresenceLost, "owns a (accepted), b (review) +1"},
		"unpushed":             {gone(Owes{Unpushed: []string{"coder/x"}, Messages: 1}), PresenceLost, "unpushed coder/x"},
		"stash":                {gone(Owes{Stash: true}), PresenceLost, "stash"},
		"one message":          {gone(Owes{Messages: 1, Ask: true}), PresenceLost, "1 message(s) waiting"},
		"an ask":               {gone(Owes{Ask: true, Gates: 1}), PresenceLost, "an open ask"},
		"a gate":               {gone(Owes{Gates: 2, Handoff: true}), PresenceLost, "a gate"},
		"a hand-off":           {gone(Owes{Handoff: true}), PresenceLost, "a hand-off not taken"},
		"unknown item":         {gone(Owes{Unknown: []string{"losable work"}}), PresenceLost, "unknown: losable work"},
		"owes before unknown":  {gone(Owes{Tasks: []string{"a (accepted)"}, Unknown: []string{"asks"}}), PresenceLost, "owns a (accepted)"},
		"required before owes": {PresenceIn{Pane: "k/t1", TermsRead: true, Required: true, Owes: Owes{Tasks: []string{"a (accepted)"}}}, PresenceLost, "required: agentbus up brings it back"},
	} {
		state, reason := Presence(c.in)
		if state != c.state || reason != c.reason {
			t.Errorf("%s: %s %q, want %s %q", name, state, reason, c.state, c.reason)
		}
	}
}

// Shown is the state a reader prints: the declared one while running, the
// presence otherwise; "no terminal" is shown nowhere.
func TestPresenceShown(t *testing.T) {
	if s := Shown("working", PresenceRunning); s != "working" {
		t.Errorf("running: %q", s)
	}
	if s := Shown("working", PresenceLost); s != "lost" {
		t.Errorf("lost: %q", s)
	}
}
