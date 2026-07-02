package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestReportsTable(t *testing.T) {
	evs := []bus.Event{
		{ID: "1-0", Agent: "claude2", RKind: "note", Message: "short one"},
		{ID: "2-0", Agent: "claude3", RKind: "auto", Message: "preview…", Full: "the\nfull\ntext"},
	}
	out := reportsTable(evs)
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "1-0") || !strings.Contains(first, "claude2") || !strings.Contains(first, `"short one"`) {
		t.Fatalf("first row wrong: %q", first)
	}
	if strings.Contains(first, "(+") {
		t.Fatalf("short report must have no (+N) marker: %q", first)
	}
	if !strings.Contains(out, "(+13)") { // "the\nfull\ntext" = 13 runes
		t.Fatalf("full report must carry (+13): %q", out)
	}
}

func TestReportDetail(t *testing.T) {
	if got := reportDetail(bus.Event{Message: "preview…", Full: "the\nfull\ntext"}); got != "the\nfull\ntext" {
		t.Fatalf("detail should return Full: %q", got)
	}
	if got := reportDetail(bus.Event{Message: "just a preview"}); got != "just a preview" {
		t.Fatalf("detail should fall back to Message: %q", got)
	}
}
