package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/quota"
)

// quota-per-provider: agentbus quota shows, on each row, the thresholds that
// apply to its provider (warn/drain/hold) and where they come from: the
// defaults, the provider's own [quota.<provider>], or the defaults because
// the provider's resolved order is broken.
func TestQuotaShowsTheThresholdsPerRow(t *testing.T) {
	now := time.Now()
	win := func(p string, pct float64) quota.Reading {
		return quota.Reading{Provider: p, ObservedAt: now.UnixMilli(), Source: "test",
			Windows: []quota.Window{{Name: "5h", UsedPct: pct, ResetsAt: now.Add(time.Hour).Unix(), Minutes: 300}}}
	}
	rs := []quota.Reading{win("anthropic", 10), win("openai", 40), win("xai", 50), {Provider: "moonshot", Unknown: true, Source: "kimi-log"}}
	th := dispatch.QuotaThresholds{Warn: 85, Drain: 85, Hold: 95,
		Providers: map[string]dispatch.QuotaLevels{"openai": {Warn: 70, Hold: 80}, "xai": {Hold: 50}}}
	var out bytes.Buffer
	printQuota(&out, rs, now, "", th)
	text := out.String()
	header := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(header, "WARN/DRAIN/HOLD") {
		t.Fatalf("no thresholds column:\n%s", text)
	}
	for p, want := range map[string]string{
		"anthropic": "85/85/95 (default)",
		"openai":    "70/70/80 (quota.openai)",
		"xai":       "85/85/95 (quota.xai: order broken, default)",
		"moonshot":  "85/85/95 (default)",
	} {
		row := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, p+" ") {
				row = l
			}
		}
		if !strings.Contains(row, want) {
			t.Errorf("%s row %q, want %q", p, row, want)
		}
	}
}
