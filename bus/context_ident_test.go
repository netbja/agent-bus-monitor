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

// A session proved after /clear is never reverted by a status of the same
// process naming an older session (foureyes' review of lifecycle-1): for the
// same pid and start a status only fills an empty IdentSession; a new
// process takes the status's session.
func TestStatusOfTheSameProcessNeverMovesAProvedSession(t *testing.T) {
	proved := &AgentSnapshot{PID: 4242, PIDStart: 7, IdentTS: 100, Session: "before-clear-0001", IdentSession: "after-clear-0002"}
	got := mergeSnapshot(proved, AgentSnapshot{TS: 200, PID: 4242, PIDStart: 7, Session: "before-clear-0001"}, false)
	if got.IdentSession != "after-clear-0002" {
		t.Fatalf("same process, older session: ident session %q", got.IdentSession)
	}
	empty := &AgentSnapshot{PID: 4242, PIDStart: 7, IdentTS: 100}
	if got := mergeSnapshot(empty, AgentSnapshot{TS: 200, PID: 4242, PIDStart: 7, Session: "first-own-0003"}, false); got.IdentSession != "first-own-0003" {
		t.Fatalf("same process, none proved yet: ident session %q", got.IdentSession)
	}
	if got := mergeSnapshot(proved, AgentSnapshot{TS: 200, PID: 5151, PIDStart: 8, Session: "relaunched-0004"}, false); got.IdentSession != "relaunched-0004" {
		t.Fatalf("new process: ident session %q", got.IdentSession)
	}
}
