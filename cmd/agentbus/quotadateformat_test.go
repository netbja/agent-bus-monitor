package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/quota"
)

// agentbus quota's RESETS: how long, and the reset dated in the person's
// format, so a seven-day window's reset is never read as today's.
func TestQuotaResetsCell(t *testing.T) {
	now := time.Date(2026, 11, 16, 14, 30, 0, 0, time.Local)
	w := quota.Window{Name: "seven_day", ResetsAt: time.Date(2026, 11, 17, 13, 59, 0, 0, time.Local).Unix()}
	if got := quotaResets(w, now, quota.DateRelative); got != "in 23h29m (tomorrow 13:59)" {
		t.Fatalf("%q", got)
	}
	if got := quotaResets(w, now, quota.DateISO); got != "in 23h29m (2026-11-17 13:59)" {
		t.Fatalf("%q", got)
	}
	w.ResetsAt = now.Add(-time.Minute).Unix()
	if got := quotaResets(w, now, quota.DateRelative); got != "past" {
		t.Fatalf("%q", got)
	}
}
