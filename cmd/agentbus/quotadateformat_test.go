package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/quota"
)

// The dispatch service reads display.date_format from config.toml at each
// message: a change applies with no restart (quota settings §2.3).
func TestServiceQuotaReadsTheDateFormatEachTime(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME", "HOME":
			return dir
		}
		return ""
	}
	q := serviceQuota(env, io.Discard)
	if q.DateFormat == nil || q.DateFormat() != "relative" {
		t.Fatal("no default relative format")
	}
	if err := os.WriteFile(cfg, []byte("[display]\ndate_format = \"clock12\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := q.DateFormat(); got != "clock12" {
		t.Fatalf("after the change: %q", got)
	}
}

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
