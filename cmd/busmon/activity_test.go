package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestLookEventMapping(t *testing.T) {
	cases := []struct {
		name string
		e    bus.Event
		want eventLook
	}{
		{"status", bus.Event{Kind: "status", Agent: "kimi", State: "working", Message: "tests"},
			eventLook{From: "kimi", Type: "status", Role: RoleMuted, Text: "working · tests"}},
		{"status no message", bus.Event{Kind: "status", Agent: "kimi", State: "idle"},
			eventLook{From: "kimi", Type: "status", Role: RoleMuted, Text: "idle"}},
		{"report note", bus.Event{Kind: "report", Agent: "kimi", RKind: "note", Message: "done"},
			eventLook{From: "kimi", Type: "report", Role: RoleText, Text: "done"}},
		{"report full", bus.Event{Kind: "report", Agent: "kimi", RKind: "note", Message: "done", Full: "done\nmore"},
			eventLook{From: "kimi", Type: "report", Role: RoleText, Text: "done (+9)"}},
		{"report auto", bus.Event{Kind: "report", Agent: "kimi", RKind: "auto", Message: "tick"},
			eventLook{From: "kimi", Type: "auto", Role: RoleMuted, Text: "tick"}},
		{"notify", bus.Event{Kind: "notify", From: "master", Message: "v0.8 out"},
			eventLook{From: "master", Type: "notify", Role: RoleAttention, Text: "v0.8 out"}},
		{"request", bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "kimi", Task: "t1", Message: "run"},
			eventLook{From: "master", To: "kimi", Type: "request", Role: RoleAccent, Text: "run"}},
		{"directive", bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "kimi", Message: "run"},
			eventLook{From: "master", To: "kimi", Type: "directive", Role: RoleAccent, Text: "run"}},
		{"reply", bus.Event{Kind: "cmd", Type: "reply", From: "kimi", Target: "master", Message: "ok"},
			eventLook{From: "kimi", To: "master", Type: "reply", Role: RoleAccent, Text: "ok"}},
		{"challenge", bus.Event{Kind: "cmd", Type: "challenge", From: "grok", Target: "kimi", Message: "?"},
			eventLook{From: "grok", To: "kimi", Type: "challenge", Role: RoleAttention, Text: "?"}},
		{"verdict", bus.Event{Kind: "cmd", Type: "verdict", From: "grok", Target: "kimi", Message: "ok"},
			eventLook{From: "grok", To: "kimi", Type: "verdict", Role: RoleAttention, Text: "ok"}},
		{"escalation", bus.Event{Kind: "cmd", Type: "escalation", From: "dispatch", Target: "master", Message: "stuck"},
			eventLook{From: "dispatch", To: "master", Type: "escalation", Role: RoleDispatch, Text: "stuck"}},
		{"blocked", bus.Event{Kind: "cmd", Type: "blocked", From: "kimi", Target: "master", Message: "need key"},
			eventLook{From: "kimi", To: "master", Type: "blocked", Role: RoleError, Text: "need key"}},
		{"unknown cmd", bus.Event{Kind: "cmd", Type: "shutdown", From: "master", Target: "kimi", Message: "bye"},
			eventLook{From: "master", To: "kimi", Type: "shutdown", Role: RoleMuted, Text: "bye"}},
		{"unknown kind", bus.Event{Kind: "weird", Message: "x"},
			eventLook{Type: "weird", Role: RoleMuted, Text: "x"}},
	}
	for _, c := range cases {
		if got := lookEvent(c.e); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func hostileFeed() []feedLine {
	at := time.Date(2026, 10, 10, 18, 23, 44, 0, time.UTC)
	return []feedLine{
		{id: "1", sep: true, at: at},
		{id: "2", at: at, ev: bus.Event{Kind: "cmd", Type: "directive", From: strings.Repeat("m", 32), Target: strings.Repeat("c", 32),
			Message: "日本語👩‍💻\tline\nnext\x1b[31m[red]x[-]‮"}},
		{id: "3", at: at, ev: bus.Event{Kind: "status", Agent: "kimi", State: "working", Message: "é tests"}},
		{id: "4", at: at, ev: bus.Event{Kind: "notify", From: "master", Message: ""}},
	}
}

func TestActivityRowWidth(t *testing.T) {
	for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
		for w := 1; w <= 200; w++ {
			for _, fl := range hostileFeed() {
				if got := activityRow(fl, tier, w).Width(); got != w {
					t.Fatalf("tier %d width %d line %s: row width %d", tier, w, fl.id, got)
				}
			}
			if h := activityHeader(tier, w); tier == TierNarrow && h != nil {
				t.Fatalf("narrow has a header")
			} else if tier != TierNarrow && h.Width() != w {
				t.Fatalf("tier %d header width %d at %d", tier, h.Width(), w)
			}
		}
	}
}

func TestActivityRowSanitises(t *testing.T) {
	row := activityRow(hostileFeed()[1], TierWide, 160).text()
	for _, bad := range []string{"\t", "\n", "\x1b", "‮"} {
		if strings.Contains(row, bad) {
			t.Fatalf("row keeps %q: %q", bad, row)
		}
	}
	if !strings.Contains(row, "[red]x[-]") {
		t.Fatalf("tag look-alike must stay literal text in a Line: %q", row)
	}
	if !strings.HasPrefix(row, "18:23") {
		t.Fatalf("row starts %q", row[:10])
	}
}

func TestNarrowFoldsTarget(t *testing.T) {
	fl := feedLine{id: "x", at: time.Now(), ev: bus.Event{Kind: "cmd", Type: "directive", From: "master", Target: "codex", Message: "review"}}
	row := activityRow(fl, TierNarrow, 64).text()
	if !strings.Contains(row, GlyphArrowR+" codex review") {
		t.Fatalf("narrow row %q does not fold the target", row)
	}
	wide := activityRow(fl, TierWide, 120).text()
	if !strings.Contains(wide, GlyphArrowR+" codex") || strings.Contains(wide, GlyphArrowR+" codex review") {
		t.Fatalf("wide row %q should keep the target in its column", wide)
	}
}

func TestDaySeparatorRow(t *testing.T) {
	fl := hostileFeed()[0]
	row := activityRow(fl, TierWide, 40)
	if !strings.Contains(row.text(), "Sat 2026-10-10") || row[0].Role != RoleDim {
		t.Fatalf("separator %+v", row)
	}
}

func TestPauseCursor(t *testing.T) {
	var p pauseCursor
	p.Arrive(5, true)
	if p.Paused() || p.New() != 0 {
		t.Fatal("live cursor counted")
	}
	p.Pause(10)
	p.Arrive(11, true)
	p.Arrive(12, false)
	p.Arrive(13, true) // a re-delivery of an older seq never counts twice
	p.Arrive(9, true)
	if p.New() != 2 {
		t.Fatalf("New %d, want 2", p.New())
	}
	p.Pause(20) // pausing again while paused keeps the cursor
	p.Arrive(14, true)
	if p.New() != 3 {
		t.Fatalf("New %d after re-pause, want 3", p.New())
	}
	p.Resume()
	if p.Paused() || p.New() != 0 {
		t.Fatal("Resume did not reset")
	}
}

func TestActivityFrameKeepsFilter(t *testing.T) {
	var p pauseCursor
	title, label := activityFrame(&p, 10, 10, feedFilter{})
	if title.text() != "Activity  live" || label.text() != "10 events · filter: none" {
		t.Fatalf("live frame %q / %q", title.text(), label.text())
	}
	p.Pause(3)
	p.Arrive(4, true)
	title, label = activityFrame(&p, 4, 10, parseFilter("@kimi"))
	if title.text() != "Activity  paused · 1 new" || label.text() != "4 of 10 events · filter: @kimi" {
		t.Fatalf("paused frame %q / %q", title.text(), label.text())
	}
	if title[len(title)-1].Role != RoleAttention {
		t.Fatalf("paused indicator role %v", title[len(title)-1].Role)
	}
}
