package bus

import "testing"

func TestRollUpMoved(t *testing.T) {
	mk := func(reviewer, author, decision string) Verdict {
		return Verdict{Reviewer: reviewer, Author: author, Decision: decision}
	}
	cases := []struct {
		name    string
		vs      []Verdict
		want    string
		wantIdx int
	}{
		{"independent approve", []Verdict{mk("claude2", "claude1", "approve")}, "APPROVED", 0},
		{"self approve only", []Verdict{mk("claude1", "claude1", "approve")}, "PENDING", -1},
		{"reject after approve", []Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "reject")}, "REJECTED", 1},
		{"reject then approve", []Verdict{mk("claude3", "claude1", "reject"), mk("claude2", "claude1", "approve")}, "APPROVED", 1},
		{"empty", nil, "PENDING", -1},
		{"two independent approves", []Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "approve")}, "APPROVED", 1},
		{"multiple approves then reject", []Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "approve"), mk("claude4", "claude1", "reject")}, "REJECTED", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, idx := RollUp(c.vs); got != c.want || idx != c.wantIdx {
				t.Errorf("RollUp = %q,%d want %q,%d", got, idx, c.want, c.wantIdx)
			}
		})
	}
}
