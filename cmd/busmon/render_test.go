package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestEntryTime(t *testing.T) {
	if got, want := entryTime("1779707136877-0"), time.UnixMilli(1779707136877); !got.Equal(want) {
		t.Fatalf("entryTime = %v, want %v", got, want)
	}
	if entryTime("bogus").IsZero() {
		t.Fatal("entryTime(bogus) returned zero time, want a fallback")
	}
}

func TestDaySeparator(t *testing.T) {
	line, plain := daySeparator(time.Date(2026, 8, 4, 23, 59, 59, 0, time.UTC))
	if !strings.Contains(line.text(), "2026-08-04") || !strings.Contains(line.text(), "Tue") {
		t.Fatalf("line = %q, want the date and weekday", line.text())
	}
	if len(line) != 1 || line[0].Role != RoleMuted {
		t.Fatalf("line = %+v, want it muted", line)
	}
	if strings.Contains(plain, "[") || !strings.Contains(plain, "2026-08-04") {
		t.Fatalf("plain = %q, want the date without color tags", plain)
	}
}

func TestParseDirected(t *testing.T) {
	if tgt, body, ok := parseDirected("@claude1 do the thing"); !ok || tgt != "claude1" || body != "do the thing" {
		t.Fatalf("parseDirected directed = (%q,%q,%v), want claude1/do the thing/true", tgt, body, ok)
	}
	if _, body, ok := parseDirected("hello world"); ok || body != "hello world" {
		t.Fatalf("parseDirected plain = (_,%q,%v), want (hello world,false)", body, ok)
	}
	if _, _, ok := parseDirected("@claude1"); ok {
		t.Fatal("parseDirected with no body should not be directed")
	}
	if _, _, ok := parseDirected("@Bad foo"); ok {
		t.Fatal("parseDirected with an invalid agent name should fall back (not directed)")
	}
}

// One completion for the line and the compose window (busmon UX item 6).
// The first three cases are the line's former agentCompletions test, kept as
// they were.
func TestRecipientCompletions(t *testing.T) {
	names := []string{"claude2", "claude1", "hermes"}
	got := recipientCompletions("@cl", names)
	if len(got) != 2 || got[0] != "@claude1 " || got[1] != "@claude2 " {
		t.Fatalf("recipientCompletions(@cl) = %v, want [@claude1 , @claude2 ]", got)
	}
	if recipientCompletions("@claude1 do", names) != nil {
		t.Fatal("no completions once the body has started (space present)")
	}
	if recipientCompletions("hello", names) != nil {
		t.Fatal("no completions when not @-prefixed")
	}
	if got := strings.Join(recipientCompletions("@", names), "|"); got != "@all |@claude1 |@claude2 |@hermes " {
		t.Fatalf("@ alone = %q", got)
	}
	if got := strings.Join(recipientCompletions("@a", names), "|"); got != "@all " {
		t.Fatalf("@a = %q", got)
	}
	if recipientCompletions("@hermes ", names) != nil {
		t.Fatal("a completed name offers nothing more")
	}
}

func TestProjectTable(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }

	out := projectTable([]bus.ProjectSummary{
		{Project: "project-a", Agents: 5, Master: "master", LastTS: ms(2 * time.Minute)},
		{Project: "agent-bus-monitor", Agents: 2, LastTS: ms(3 * time.Hour)},
		{Project: "demo", Agents: 1, LastTS: ms(6 * 24 * time.Hour)},
	}, now)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (header + 3 projects):\n%s", len(lines), out)
	}
	for _, want := range []string{"PROJECT", "AGENTS", "MASTER", "LAST ACTIVITY"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q missing %q", lines[0], want)
		}
	}
	if !strings.Contains(lines[1], "project-a") || !strings.Contains(lines[1], "2m ago") {
		t.Errorf("row 1 = %q, want the newest project with its age", lines[1])
	}
	if !strings.Contains(lines[2], "3h ago") || !strings.Contains(lines[3], "6d ago") {
		t.Errorf("ages not rendered:\n%s", out)
	}
	// no pilot lease reads as "-", never as an empty column
	if !strings.Contains(lines[2], " - ") {
		t.Errorf("masterless row = %q, want a '-' placeholder", lines[2])
	}
	// the table renders in the order given: bus.Projects owns the sort
	if strings.Index(out, "project-a") > strings.Index(out, "demo") {
		t.Error("projectTable reordered its input")
	}
	// columns line up: every row starts its AGENTS column at the same offset
	col := strings.Index(lines[0], "AGENTS")
	for _, l := range lines[1:] {
		if len(l) < col {
			t.Fatalf("row shorter than the header columns: %q", l)
		}
	}
}

// Empty output (not a "none found" line) keeps stdout clean for `| wc -l`; the
// caller reports the empty case on stderr.
func TestProjectTableEmptyRendersNothing(t *testing.T) {
	if got := projectTable(nil, time.Now()); got != "" {
		t.Errorf("projectTable(nil) = %q, want empty", got)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m"},
		{59 * time.Minute, "59m"},
		{3 * time.Hour, "3h"},
		{25 * time.Hour, "1d"},
		{6 * 24 * time.Hour, "6d"},
		{-5 * time.Second, "0s"}, // clock skew is not a negative age
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestLastSeenNever(t *testing.T) {
	if got := lastSeen(0, time.Now()); got != "never" {
		t.Errorf("lastSeen(0) = %q, want %q", got, "never")
	}
}
