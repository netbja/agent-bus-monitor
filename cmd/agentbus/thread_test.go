package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestThreadReport(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	root := "1700000000000-0"
	evs := []bus.Event{
		{ID: root, Kind: "cmd", Type: "directive", From: "hermes", Target: "claude2", Message: "review the migration"},
		{ID: "1700000000005-0", Kind: "cmd", Type: "reply", From: "claude2", Target: "hermes", Message: "on it", Ref: root},
	}
	out := threadReport(root, evs, now)
	if !strings.Contains(out, "(2 entries)") {
		t.Fatalf("header missing count: %q", out)
	}
	if !strings.Contains(out, "directive") || !strings.Contains(out, "hermes→claude2") || !strings.Contains(out, `"review the migration"`) {
		t.Fatalf("root line wrong: %q", out)
	}
	if !strings.Contains(out, "(root)") {
		t.Fatalf("root not marked: %q", out)
	}
	if strings.Index(out, "review the migration") > strings.Index(out, "on it") {
		t.Fatalf("not chronological: %q", out)
	}
}

func TestThreadReportEmpty(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	if out := threadReport("x-0", nil, now); !strings.Contains(out, "(no entries)") {
		t.Fatalf("empty thread should say no entries: %q", out)
	}
	evs := []bus.Event{{ID: "1-0", Type: "directive", From: "a", Target: "b", Message: ""}}
	if out := threadReport("1-0", evs, now); strings.Contains(out, `""`) {
		t.Fatalf("empty message should not render quotes: %q", out)
	}
}
