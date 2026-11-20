package dispatchsvc

import (
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"strings"
	"testing"
)

func TestHealthCheckParsingHelpers(t *testing.T) {
	for _, tc := range []struct {
		version, minimum string
		want             bool
	}{
		{"v1.2.3", "1.2.2", true}, {"1.2.3", "1.2.3", true}, {"1.2.2", "1.2.3", false}, {"development", "1.0.0", false},
	} {
		if got := versionAtLeast(tc.version, tc.minimum); got != tc.want {
			t.Errorf("versionAtLeast(%q,%q)=%v", tc.version, tc.minimum, got)
		}
	}
	for _, tc := range []struct {
		value string
		want  int
	}{{"12k ctx", 12000}, {"1.5m", 1500000}, {"unknown", 0}, {"250", 250}} {
		if got := parseContextTokens(tc.value); got != tc.want {
			t.Errorf("parseContextTokens(%q)=%d want %d", tc.value, got, tc.want)
		}
	}
}

func TestClientEvidence(t *testing.T) {
	now := int64(1_000_000)
	snaps := map[string]bus.AgentSnapshot{"a": {Pane: "p1", PID: 1}, "b": {Pane: "p2", PID: 2}, "unbound": {}}
	obs := map[string]bus.Observation{
		"a": {At: now - 1000, PID: 1, ClientJudged: true, Update: &bus.ClientUpdate{Client: "claude-code", Running: "1", Installed: "2"}},
		"b": {At: now - 1000, PID: 99, ClientJudged: true}, // another process: not fresh for b
	}
	ev := clientEvidence(snaps, obs, now)
	if len(ev) != 2 {
		t.Fatalf("evidence %+v", ev)
	}
	for _, e := range ev {
		switch e.Agent {
		case "a":
			if !e.Fresh || !e.Outdated || e.Running != "1" {
				t.Fatalf("a %+v", e)
			}
		case "b":
			if e.Fresh {
				t.Fatalf("b read fresh from another process: %+v", e)
			}
		}
	}
}

// The context check's evidence: every published fill, known only when it is
// not aged; the verdict follows the threshold.
func TestContextEvidence(t *testing.T) {
	at := int64(10_000_000)
	fills := map[string]bus.ContextFill{
		"coder":  {Pct: 86, Session: "s1", ObservedAt: at, At: at},
		"master": {Pct: 95, Session: "s2", ObservedAt: at - bus.ContextMaxAge.Milliseconds() - 1, At: at}, // aged
	}
	ev := contextEvidence(fills, nil, nil, nil)
	out := health.EvaluateContext(ev, 80)
	if len(ev) != 2 || out.Status != health.Warn || out.Detail != "at or above 80%: coder 86%" {
		t.Fatalf("evidence %+v, outcome %+v", ev, out)
	}
	// context-escalation §5: the dispatcher's escalation of this session,
	// no hand-off standing: escalated, with the alert's age; a hand-off
	// standing lifts it; another session's escalation does not count.
	marks := map[string]string{bus.ContextMark("coder"): "s1", bus.ContextMark("coder") + bus.MarkAtSuffix: fmt.Sprintf("%d-0", at-27*60000),
		bus.ContextPersonMark("coder"): "s1"}
	snaps := map[string]bus.AgentSnapshot{"coder": {PID: 7, PIDStart: 9, IdentSession: "s1"}}
	out = health.EvaluateContext(contextEvidence(fills, marks, nil, snaps), 80)
	if out.Reason != "escalated" || !strings.Contains(out.Detail, "coder 86% (no hand-off 27 min)") {
		t.Fatalf("escalated: %+v", out)
	}
	stands := map[string]bus.Handoff{"coder": {Report: "1-1", Session: "s1", PID: 7, PIDStart: 9, At: at}}
	if out := health.EvaluateContext(contextEvidence(fills, marks, stands, snaps), 80); out.Reason != "" {
		t.Fatalf("a hand-off stands: %+v", out)
	}
	marks[bus.ContextPersonMark("coder")] = "s0"
	if out := health.EvaluateContext(contextEvidence(fills, marks, nil, snaps), 80); out.Reason != "" {
		t.Fatalf("another session's escalation: %+v", out)
	}
}

// vt-osc-title: screen-unknown's evidence: the declared state, the
// dispatcher's observation (fresh or aged), and whether the agent's client
// has a ready signature to read a screen with.
func TestScreenEvidence(t *testing.T) {
	now := int64(10_000_000_000)
	snaps := map[string]bus.AgentSnapshot{
		"coder": {State: "idle", Client: "claude-code"},
		"kimi":  {State: "idle", Client: "kimi-cli"},
		"x":     {State: "idle", Client: "nonesuch"},
	}
	obs := map[string]bus.Observation{
		"coder": {State: "unknown", Since: now - 14*60_000, At: now - 1000},
		"kimi":  {State: "unknown", Since: now - 30*60_000, At: now - 1000},
		"x":     {State: "unknown", Since: now - 30*60_000, At: now - 1000},
	}
	ev := screenEvidence(snaps, obs, now)
	out := health.EvaluateScreenUnknown(ev, now)
	if out.Status != health.Warn || !strings.Contains(out.Detail, "coder 14 min") || strings.Contains(out.Detail, "x ") {
		t.Fatalf("%+v %+v", ev, out)
	}
	obs["coder"] = bus.Observation{State: "unknown", Since: now - 14*60_000, At: now - bus.ObservedTTL - 1}
	if out := health.EvaluateScreenUnknown(screenEvidence(snaps, obs, now), now); strings.Contains(out.Detail, "coder") {
		t.Fatalf("an aged observation: %+v", out)
	}
}
