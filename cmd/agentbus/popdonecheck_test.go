package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

func TestPopDoneCheck(t *testing.T) {
	now := time.UnixMilli(10_000_000_000)
	idle := now.Add(-50 * time.Minute).UnixMilli()
	d := popDoneDeps{
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) {
			return map[string]bus.AgentSnapshot{
				"security": {State: "idle", TS: idle, Tier: "pop"},
				"docs":     {State: "idle", TS: idle, Tier: "pop"},  // not told
				"coder":    {State: "idle", TS: idle, Tier: "boot"}, // never judged
			}, nil
		},
		Marks: func(context.Context) (map[string]string, error) {
			return map[string]string{bus.PopDoneMark("security"): itoa64(idle), bus.PopDoneMark("coder"): itoa64(idle)}, nil
		},
		Now:    func(context.Context) (int64, error) { return now.UnixMilli(), nil },
		Getenv: withConfig(t, "", nil),
	}
	out, err := popDoneCheck(d)(context.Background())
	if err != nil || out.Status != health.Warn || !strings.Contains(out.Detail, "security done 50m ago") || strings.Contains(out.Detail, "docs") || strings.Contains(out.Detail, "coder") {
		t.Fatalf("pop-done = %+v, %v", out, err)
	}
	d.Getenv = withConfig(t, "[dispatch]\npop_retire = false\n", nil)
	if out, _ := popDoneCheck(d)(context.Background()); out.Status != health.Unknown {
		t.Fatalf("off = %+v", out)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
