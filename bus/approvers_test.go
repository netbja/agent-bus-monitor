package bus

import (
	"reflect"
	"testing"
)

// CurrentApprovers is single-approval §4.5's count: the reviewers whose latest
// verdict is an independent approve naming h. Its example: A approves H, then
// H2; B approves H; H's current approvers are B alone.
func TestCurrentApprovers(t *testing.T) {
	ap := func(reviewer, decision, head string) Verdict {
		return Verdict{Author: "coder", Reviewer: reviewer, Decision: decision, Head: head}
	}
	cases := []struct {
		name string
		vs   []Verdict
		want []string
	}{
		{"spec example", []Verdict{ap("a", "approve", "H"), ap("a", "approve", "H2"), ap("b", "approve", "H")}, []string{"b"}},
		{"two, first-verdict order", []Verdict{ap("b", "approve", "H"), ap("a", "approve", "H"), ap("b", "approve", "H")}, []string{"b", "a"}},
		{"a later reject removes", []Verdict{ap("a", "approve", "H"), ap("a", "reject", "H")}, nil},
		{"self-authored never counts", []Verdict{ap("coder", "approve", "H")}, nil},
		{"a later self verdict hides an earlier approve", []Verdict{ap("a", "approve", "H"), {Author: "a", Reviewer: "a", Decision: "approve", Head: "H"}}, nil},
		{"none", nil, nil},
	}
	for _, c := range cases {
		if got := CurrentApprovers(c.vs, "H"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
