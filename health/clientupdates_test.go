package health

import (
	"strings"
	"testing"
)

func TestEvaluateClientUpdates(t *testing.T) {
	up := func(a string) ClientEvidence { return ClientEvidence{Agent: a, Fresh: true, Judged: true} }
	old := func(a string) ClientEvidence {
		return ClientEvidence{Agent: a, Fresh: true, Judged: true, Outdated: true, Client: "claude-code", Running: "2.1.305", Installed: "2.1.306"}
	}
	cases := []struct {
		name   string
		ev     []ClientEvidence
		status Status
		detail string
	}{
		{"nothing observed", nil, Unknown, "no fresh"},
		{"all stale", []ClientEvidence{{Agent: "a", Judged: true}}, Unknown, "no fresh"},
		{"all judged, up to date", []ClientEvidence{up("a"), up("b")}, OK, ""},
		{"one cannot be judged", []ClientEvidence{up("a"), {Agent: "b", Fresh: true}}, Unknown, "b"},
		{"two outdated", []ClientEvidence{old("a"), old("b"), up("c")}, Warn, "2 agent(s): claude-code 2.1.305" + "→" + "2.1.306"},
	}
	for _, c := range cases {
		out := EvaluateClientUpdates(c.ev)
		if out.Status != c.status || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %+v", c.name, out)
		}
	}
	if _, ok := ByID("client-updates"); !ok {
		t.Fatal("client-updates is not in the catalogue")
	}
}

// The remediation is one static text for every reload policy (auto-reload):
// sentinel reloads, master last; propose or no sentinel asks the person; off
// reloads nothing. It no longer forbids the reload sentinel is there to do.
func TestClientUpdatesRemediation(t *testing.T) {
	d, ok := ByID("client-updates")
	if !ok {
		t.Fatal("client-updates is not in the catalogue")
	}
	r := d.Remediation
	for _, want := range []string{"sentinel reloads the outdated agents (agentbus reload)", "master last", "under propose, or with no sentinel registered, the person is asked through master", "under off nothing is reloaded", "Never type into a pane"} {
		if !strings.Contains(r, want) {
			t.Errorf("remediation %q lacks %q", r, want)
		}
	}
	if strings.Contains(r, "never restart a client") || strings.Contains(r, "Propose a reload") {
		t.Errorf("remediation still forbids the reload: %q", r)
	}
}
