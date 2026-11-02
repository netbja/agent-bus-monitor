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

// A peer's resolved endpoint is kept like its client: a later write that
// does not name one (a bind, a turn flip, a status from elsewhere) leaves
// it; a new one replaces it (R4: health probes that endpoint).
func TestMergeSnapshotEndpoint(t *testing.T) {
	st := mergeSnapshot(nil, AgentSnapshot{State: "idle", TS: 10, Client: "local-llm", Endpoint: "used"}, false)
	if st.Endpoint != "used" {
		t.Fatalf("first status = %q", st.Endpoint)
	}
	st = mergeSnapshot(&st, AgentSnapshot{State: "working", TS: 20}, false)
	st = mergeSnapshot(&st, AgentSnapshot{Pane: "p", PID: 7, PIDStart: 70, IdentTS: 30}, true)
	if st.Endpoint != "used" {
		t.Fatalf("after a status and a bind without one = %q", st.Endpoint)
	}
	if st = mergeSnapshot(&st, AgentSnapshot{State: "idle", TS: 40, Endpoint: "other"}, false); st.Endpoint != "other" {
		t.Fatalf("a new endpoint = %q", st.Endpoint)
	}
}
