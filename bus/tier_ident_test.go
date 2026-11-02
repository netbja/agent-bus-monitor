package bus

import "testing"

// The launched role's tier (pop-auto-retire §6) is part of a proved identity,
// kept like the launched model: only a proof sets it, a proof for the same
// process keeps it when it carries none, another process takes the caller's.
func TestMergeSnapshotTier(t *testing.T) {
	bound := mergeSnapshot(nil, AgentSnapshot{Pane: "p", PID: 7, PIDStart: 70, IdentTS: 10, Tier: "pop"}, true)
	if bound.Tier != "pop" {
		t.Fatalf("bind: %q", bound.Tier)
	}
	st := mergeSnapshot(&bound, AgentSnapshot{State: "idle", TS: 20, Tier: "boot"}, false) // unverified status
	if st.Tier != "pop" {
		t.Fatalf("an unverified status changed it: %q", st.Tier)
	}
	same := mergeSnapshot(&st, AgentSnapshot{State: "idle", TS: 30, Pane: "p", PID: 7, PIDStart: 70}, false)
	if same.Tier != "pop" {
		t.Fatalf("a proof for the same process erased it: %q", same.Tier)
	}
	other := mergeSnapshot(&same, AgentSnapshot{Pane: "p", PID: 8, PIDStart: 80, IdentTS: 40}, true)
	if other.Tier != "" {
		t.Fatalf("another process kept the old tier: %q", other.Tier)
	}
	fresh := mergeSnapshot(nil, AgentSnapshot{State: "idle", TS: 10, Tier: "pop"}, false)
	if fresh.Tier != "" {
		t.Fatalf("an unverified first status set it: %q", fresh.Tier)
	}
}
