package main

import (
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
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
	ev := contextEvidence(fills)
	out := health.EvaluateContext(ev, 80)
	if len(ev) != 2 || out.Status != health.Warn || out.Detail != "at or above 80%: coder 86%" {
		t.Fatalf("evidence %+v, outcome %+v", ev, out)
	}
}
