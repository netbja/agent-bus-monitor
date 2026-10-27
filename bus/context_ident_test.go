package bus

import "testing"

// The session the dispatcher reads a context fill from is bound to the
// identity proof (foureyes' review of ca1): an unverified status may change
// Session, never IdentSession; a verified one sets both.
func TestIdentSessionFollowsTheProofOnly(t *testing.T) {
	old := &AgentSnapshot{PID: 4242, PIDStart: 7, IdentTS: 100, Session: "coder-own-session", IdentSession: "coder-own-session"}
	got := mergeSnapshot(old, AgentSnapshot{TS: 200, Session: "someone-elses-session"}, false)
	if got.PID != 4242 || got.IdentSession != "coder-own-session" || got.Session != "someone-elses-session" {
		t.Fatalf("unverified status: pid %d session %q ident session %q", got.PID, got.Session, got.IdentSession)
	}
	got = mergeSnapshot(&got, AgentSnapshot{TS: 300, PID: 5151, PIDStart: 8, Session: "new-own-session"}, false)
	if got.PID != 5151 || got.IdentSession != "new-own-session" {
		t.Fatalf("verified status: pid %d ident session %q", got.PID, got.IdentSession)
	}
	if got := mergeSnapshot(nil, AgentSnapshot{TS: 1, Session: "s"}, false); got.IdentSession != "" {
		t.Fatalf("no proof, yet an ident session %q", got.IdentSession)
	}
}
