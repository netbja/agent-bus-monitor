package bus

import "testing"

func TestEffectiveStateReadsAStuckFlipAsIdle(t *testing.T) {
	const min = int64(60_000)
	now := int64(1_000_000_000)
	flipped := AgentSnapshot{State: "working", Auto: AutoTurn, TS: now - 60*min}
	ready := &Observation{At: now - 5_000, Since: now - 3*min, State: "idle"}
	if st, since := EffectiveState(flipped, ready, now, 2*min); st != "idle" || since != now-3*min {
		t.Fatalf("a flip, ready for 3 min: %s since %d", st, since)
	}
	// The since is bounded below by the flip's ts (reviewer-codex).
	recent := flipped
	recent.TS = now - 2*min - 30_000
	old := &Observation{At: now - 5_000, Since: now - 30*min, State: "idle"}
	if st, since := EffectiveState(recent, old, now, 2*min); st != "idle" || since != recent.TS {
		t.Fatalf("bounded by the flip: %s since %d (ts %d)", st, since, recent.TS)
	}
	cases := []struct {
		name string
		snap AgentSnapshot
		obs  *Observation
	}{
		{"declared working", AgentSnapshot{State: "working", TS: now - 60*min}, ready},
		{"under the threshold", flipped, &Observation{At: now - 5_000, Since: now - min, State: "idle"}},
		{"stale observation", flipped, &Observation{At: now - ObservedTTL - 1, Since: now - 30*min, State: "idle"}},
		{"busy", flipped, &Observation{At: now - 5_000, Since: now - 30*min, Busy: true, State: "working"}},
		{"blocked", flipped, &Observation{At: now - 5_000, Since: now - 30*min, State: "blocked"}},
		{"no observation", flipped, nil},
		{"0 = off", flipped, ready},
	}
	for _, c := range cases {
		after := 2 * min
		if c.name == "0 = off" {
			after = 0
		}
		if st, since := EffectiveState(c.snap, c.obs, now, after); st != "working" || since != c.snap.TS {
			t.Errorf("%s: %s since %d", c.name, st, since)
		}
	}
}
