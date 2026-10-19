package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
)

func TestRunawayThresholdConfiguration(t *testing.T) {
	values := map[string]string{
		"AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER":   "45m",
		"AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH": "200000",
		"AGENTBUS_HEALTH_RUNAWAY_GROWTH_SPAN":    "5m",
		"AGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS": "6",
	}
	got, ok := runawayThresholdsFromEnv(func(key string) string { return values[key] })
	want := health.RunawayThresholds{SilentAfter: 45 * time.Minute, FastGrowth: 200000,
		GrowthSpan: 5 * time.Minute, RepeatedScreens: 6}
	if !ok || got != want {
		t.Fatalf("runawayThresholdsFromEnv()=(%+v,%v), want (%+v,true)", got, ok, want)
	}
	got, ok = runawayThresholdsFromEnv(func(string) string { return "" })
	if !ok || got != health.DefaultRunawayThresholds() {
		t.Fatalf("empty configuration=(%+v,%v), want defaults", got, ok)
	}
	got, ok = runawayThresholdsFromEnv(func(key string) string {
		if key == "AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH" {
			return "not-a-number"
		}
		return ""
	})
	if ok || health.EvaluateRunawayWithThresholds(0, 0, 0, 0, ok, got).Status != health.Unknown {
		t.Fatalf("invalid configuration must produce unknown, thresholds=%+v valid=%v", got, ok)
	}
}

func TestContextTokenCountRejectsMalformedEvidence(t *testing.T) {
	for _, value := range []string{"", "unknown", "NaN", "Inf", "-3k", "1e999m"} {
		if _, ok := contextTokenCount(value); ok {
			t.Errorf("contextTokenCount(%q) unexpectedly accepted malformed evidence", value)
		}
	}
	if got, ok := contextTokenCount("1.5m ctx"); !ok || got != 1_500_000 {
		t.Fatalf("contextTokenCount()=(%d,%v), want (1500000,true)", got, ok)
	}
}
