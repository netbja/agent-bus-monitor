package mission

import (
	"context"
	"slices"
	"testing"
)

// HeldBy lists the missions an agent holds now (requested, accepted,
// delivered or checking, its current attempt), across every loaded change
// (dispatchsvc P1, moved from cmd/agentbus swap.go's missionsOf unchanged).
func TestHeldBy(t *testing.T) {
	ctx := context.Background()
	s, _ := leasedStore(t)
	for _, c := range []string{"alpha", "beta"} {
		if err := s.PutChange(ctx, ChangeRecord{Change: c}); err != nil {
			t.Fatal(err)
		}
	}
	put := func(c, id string, st State, agent string) {
		t.Helper()
		if err := s.PutMission(ctx, c, MissionRecord{ID: id, State: st, Current: Attempt{Agent: agent}}); err != nil {
			t.Fatal(err)
		}
	}
	put("alpha", "m1", Requested, "coder")
	put("alpha", "m2", Accepted, "coder")
	put("alpha", "m3", Pending, "coder")
	put("alpha", "m4", Integrated, "coder")
	put("alpha", "m5", Accepted, "coder2")
	put("beta", "m6", Delivered, "coder")
	put("beta", "m7", Checking, "coder")
	put("beta", "m8", InReview, "coder")
	got, err := HeldBy(ctx, s.B, "coder")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"mission alpha/m1 (requested)", "mission alpha/m2 (accepted)", "mission beta/m6 (delivered)", "mission beta/m7 (checking)"}
	if !slices.Equal(got, want) {
		t.Errorf("HeldBy = %q, want %q", got, want)
	}
	if got, err := HeldBy(ctx, s.B, "nobody"); err != nil || len(got) != 0 {
		t.Errorf("an agent with none: %q, %v", got, err)
	}
}
