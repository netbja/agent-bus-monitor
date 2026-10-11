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
	if !strings.Contains(row.text(), "Sat 2026-10-10") || row[0].Role != RoleMuted { // spec §3.4: a muted rule; dim never carries information alone
		t.Fatalf("separator %+v", row)
	}
}

func TestPauseCursor(t *testing.T) {
	var p pauseCursor
	p.Recount([]int{5})
	if p.Paused() || p.New() != 0 {
		t.Fatal("live cursor counted")
	}
	p.Pause(10)
	p.Recount([]int{5, 9, 11, 13})
	if p.New() != 2 {
		t.Fatalf("New %d, want 2", p.New())
	}
	p.Pause(20) // pausing again while paused keeps the cursor
	p.Recount([]int{11, 13, 14})
	if p.New() != 3 {
		t.Fatalf("New %d after re-pause, want 3", p.New())
	}
	p.Recount([]int{14}) // a narrower filter shrinks the count
	if p.New() != 1 {
		t.Fatalf("New %d after filter change, want 1", p.New())
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
	p.Recount([]int{4})
	title, label = activityFrame(&p, 4, 10, parseFilter("@kimi"))
	if title.text() != "Activity  paused · 1 new" || label.text() != "4 of 10 events · filter: @kimi" {
		t.Fatalf("paused frame %q / %q", title.text(), label.text())
	}
	if title[len(title)-1].Role != RoleAttention {
		t.Fatalf("paused indicator role %v", title[len(title)-1].Role)
	}
}

// Day separators are not events: they never count in the title nor in "N new",
// with or without a filter.
func TestAdmittedEventsSkipsSeparators(t *testing.T) {
	at := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	feed := []feedLine{
		{id: "1", seq: 1, sep: true, at: at},
		{id: "2", seq: 2, at: at, ev: bus.Event{Kind: "notify", From: "master", Message: "deploy"}},
		{id: "3", seq: 3, sep: true, at: at.Add(24 * time.Hour)},
		{id: "4", seq: 4, at: at, ev: bus.Event{Kind: "status", Agent: "kimi", State: "idle", Message: "tests"}},
	}
	adm, events := admittedEvents(feed, feedFilter{})
	if events != 2 || len(adm) != 2 || adm[0] != 2 || adm[1] != 4 {
		t.Fatalf("no filter: admitted %v events %d, want [2 4] 2", adm, events)
	}
	adm, events = admittedEvents(feed, parseFilter("@kimi"))
	if events != 2 || len(adm) != 1 || adm[0] != 4 {
		t.Fatalf("@kimi: admitted %v events %d, want [4] 2", adm, events)
	}
	var p pauseCursor
	p.Pause(1)
	p.Recount(adm)
	if p.New() != 1 {
		t.Fatalf("N new = %d, want 1 (separator at seq 3 excluded)", p.New())
	}
}
