package health

import (
	"strings"
	"testing"
)

// The context check warns when an agent's known fill is at or above the
// alert threshold, is ok when every known fill is below it, and unknown when
// no fill is known; an alert turned off reads ok and says so.
func TestEvaluateContext(t *testing.T) {
	cases := []struct {
		name      string
		ev        []ContextEvidence
		threshold float64
		status    Status
		detail    string
	}{
		{"nothing read", nil, 80, Unknown, "no context fill"},
		{"only unknown fills", []ContextEvidence{{Agent: "codex"}}, 80, Unknown, "no context fill"},
		{"all below", []ContextEvidence{{Agent: "coder", Pct: 79.9, Known: true}, {Agent: "codex"}}, 80, OK, ""},
		{"at the threshold", []ContextEvidence{{Agent: "coder", Pct: 80, Known: true}}, 80, Warn, "coder 80%"},
		{"two above, sorted", []ContextEvidence{{Agent: "master", Pct: 91.4, Known: true}, {Agent: "coder", Pct: 85, Known: true}, {Agent: "docs", Pct: 10, Known: true}}, 80, Warn, "coder 85%, master 91%"},
		{"alert off", []ContextEvidence{{Agent: "coder", Pct: 99, Known: true}}, 0, OK, "alert off"},
	}
	for _, c := range cases {
		out := EvaluateContext(c.ev, c.threshold)
		if out.Status != c.status || !strings.Contains(out.Detail, c.detail) || !SafeText(out.Detail) {
			t.Errorf("%s: %+v", c.name, out)
		}
	}
	if d, ok := ByID("context"); !ok || !strings.Contains(d.Remediation, "hand-off") {
		t.Fatalf("context is not in the catalogue: %+v", d)
	}
}
