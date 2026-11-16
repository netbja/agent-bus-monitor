package bus

import "testing"

// Item 22 (the person, 2026-11-16): an agent whose holder terminal is gone
// kept its last state, 'idle', in busmon and agentbus agents. Shown instead:
// 'no terminal', never idle, as soon as the project's holder is read without
// a live terminal of it; its own state again once one is back. The snapshot
// is never rewritten (putSnapshot): this is a reader's projection.
func TestShownState(t *testing.T) {
	for _, c := range []struct {
		state, pane     string
		termsRead, live bool
		want            string
	}{
		{"idle", "t3", true, false, StateNoTerminal},    // the field case
		{"working", "t3", true, false, StateNoTerminal}, // never a stale working either
		{"done", "t3", true, false, StateNoTerminal},
		{"idle", "t3", true, true, "idle"},   // its terminal is back
		{"idle", "t3", false, false, "idle"}, // the holder not read (another host, or down): nothing known
		{"idle", "", true, false, "idle"},    // never in a holder terminal (a person, a remote agent)
		{"", "t3", true, false, StateNoTerminal},
	} {
		if got := ShownState(c.state, c.pane, c.termsRead, c.live); got != c.want {
			t.Errorf("%+v: %q, want %q", c, got, c.want)
		}
	}
}
