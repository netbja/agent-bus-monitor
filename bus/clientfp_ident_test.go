package bus

import "testing"

// The launched client's fingerprint (client reload spec) is part of a proved
// identity, kept like the launched model: a status without a proof never
// erases it, a proof for the same process keeps it when it carries none, a
// proof for another process takes the caller's, even empty.
func TestMergeSnapshotClientFP(t *testing.T) {
	bound := mergeSnapshot(nil, AgentSnapshot{Pane: "p", PID: 7, PIDStart: 70, IdentTS: 10, ClientFP: "fp1"}, true)
	if bound.ClientFP != "fp1" {
		t.Fatalf("bind: %q", bound.ClientFP)
	}
	st := mergeSnapshot(&bound, AgentSnapshot{State: "idle", TS: 20}, false) // unverified status
	if st.ClientFP != "fp1" {
		t.Fatalf("an unverified status erased it: %q", st.ClientFP)
	}
	same := mergeSnapshot(&st, AgentSnapshot{State: "idle", TS: 30, Pane: "p", PID: 7, PIDStart: 70}, false)
	if same.ClientFP != "fp1" {
		t.Fatalf("a proof for the same process erased it: %q", same.ClientFP)
	}
	other := mergeSnapshot(&same, AgentSnapshot{Pane: "p", PID: 8, PIDStart: 80, IdentTS: 40}, true)
	if other.ClientFP != "" {
		t.Fatalf("another process kept the old fingerprint: %q", other.ClientFP)
	}
}
