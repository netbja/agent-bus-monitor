package health

import (
	"strings"
	"testing"
	"time"
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

// context-escalation §5: an escalated agent (the dispatcher's mark, no
// hand-off standing) is a warn For you, reason escalated; the others stay
// the plain warn in hand; never a fail.
func TestEvaluateContextEscalated(t *testing.T) {
	ev := []ContextEvidence{
		{Agent: "coder", Pct: 91, Known: true, Escalated: true, SinceAlert: 27 * time.Minute},
		{Agent: "docs", Pct: 85, Known: true},
	}
	out := EvaluateContext(ev, 80)
	if out.Status != Warn || out.Reason != "escalated" || !strings.Contains(out.Detail, "escalated: coder 91% (no hand-off 27 min)") {
		t.Fatalf("escalated: %+v", out)
	}
	d, _ := ByID("context")
	if d.PlaceOf(Record{Status: Warn, Reason: out.Reason}, Warn) != PlaceForYou {
		t.Fatal("an escalation is not For you")
	}
	if plain := EvaluateContext(ev[1:], 80); plain.Reason != "" || d.PlaceOf(Record{Status: Warn}, Warn) == PlaceForYou {
		t.Fatalf("a plain alert went For you: %+v", plain)
	}
	unknownSince := EvaluateContext([]ContextEvidence{{Agent: "coder", Pct: 91, Known: true, Escalated: true}}, 80)
	if !strings.Contains(unknownSince.Detail, "escalated: coder 91% (no hand-off)") {
		t.Fatalf("no alert time: %+v", unknownSince)
	}
}
