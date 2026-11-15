package bus

import "testing"

// The agent's Claude config directory (login-updates §3.2) is set only with
// a proof, like the client's fingerprint: an unverified status neither sets
// nor erases it, a proof for the same process keeps it when it carries none,
// a proof for another process takes the caller's, even empty.
func TestMergeSnapshotClaudeConfigDir(t *testing.T) {
	bound := mergeSnapshot(nil, AgentSnapshot{Pane: "p", PID: 7, PIDStart: 70, IdentTS: 10, ClaudeConfigDir: "/srv/claude-b"}, true)
	if bound.ClaudeConfigDir != "/srv/claude-b" {
		t.Fatalf("bind: %q", bound.ClaudeConfigDir)
	}
	st := mergeSnapshot(&bound, AgentSnapshot{State: "idle", TS: 20, ClaudeConfigDir: "/tmp/elsewhere"}, false) // unverified status
	if st.ClaudeConfigDir != "/srv/claude-b" {
		t.Fatalf("an unverified status moved it: %q", st.ClaudeConfigDir)
	}
	same := mergeSnapshot(&st, AgentSnapshot{State: "idle", TS: 30, Pane: "p", PID: 7, PIDStart: 70}, false)
	if same.ClaudeConfigDir != "/srv/claude-b" {
		t.Fatalf("a proof for the same process erased it: %q", same.ClaudeConfigDir)
	}
	other := mergeSnapshot(&same, AgentSnapshot{Pane: "p", PID: 8, PIDStart: 80, IdentTS: 40}, true)
	if other.ClaudeConfigDir != "" {
		t.Fatalf("another process kept the old directory: %q", other.ClaudeConfigDir)
	}
	fresh := mergeSnapshot(nil, AgentSnapshot{State: "idle", TS: 50, ClaudeConfigDir: "/tmp/x"}, false) // never proved
	if fresh.ClaudeConfigDir != "" {
		t.Fatalf("an unproved status set it: %q", fresh.ClaudeConfigDir)
	}
}
