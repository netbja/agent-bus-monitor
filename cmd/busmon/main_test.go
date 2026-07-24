package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestActivityTitleLive(t *testing.T) {
	// At the bottom: every line fits above or within the viewport (below <= 0).
	for _, tc := range []struct{ total, top, height int }{
		{0, 0, 0},    // empty feed, no layout yet
		{10, 0, 20},  // fewer lines than the viewport
		{50, 30, 20}, // top=30, height=20 → last line exactly visible
		{50, 35, 20}, // scrolled past the end (clamped in practice) → still live
	} {
		if got := activityTitle(tc.total, tc.top, tc.height); got != " ACTIVITY  [green][live][-] " {
			t.Errorf("activityTitle(%d,%d,%d) = %q, want live", tc.total, tc.top, tc.height, got)
		}
	}
}

func TestActivityTitlePaused(t *testing.T) {
	// Scrolled up: total - top - height = lines hidden below the viewport.
	got := activityTitle(100, 30, 20) // 100-30-20 = 50 below
	want := " ACTIVITY  [yellow][↑ pause · 50 below — End/G for live][-] "
	if got != want {
		t.Errorf("activityTitle(100,30,20) = %q, want %q", got, want)
	}
}

func TestClip(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 48, "short"},
		{"exactly-five", 12, "exactly-five"}, // len == n: untouched
		{"truncate me please", 8, "truncate…"},
		{"trailing space pad", 9, "trailing…"}, // cut lands on a space → trimmed before ellipsis
		{"héllo wörld", 5, "héllo…"},           // rune-counted, not bytes
	} {
		if got := clip(tc.in, tc.n); got != tc.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestConfirmDeleteAccepts(t *testing.T) {
	s := bus.ProjectSummary{Project: "demo", Agents: 2}
	cases := map[string]bool{
		"y\n":    true,
		"yes\n":  true,
		"Y\n":    true,
		" yes ":  true,
		"n\n":    false,
		"\n":     false,
		"":       false, // EOF from a pipe declines, as --reset does
		"demo\n": false, // the project name is not a yes
	}
	for in, want := range cases {
		got := confirmDelete(s, 4, time.Now(), io.Discard, strings.NewReader(in))
		if got != want {
			t.Errorf("confirmDelete(%q) = %v, want %v", in, got, want)
		}
	}
}

// The prompt is the whole safety mechanism for --delete: the operator judges on
// what it says, so it must carry the scale and the recency, not just the slug.
func TestConfirmDeletePromptShowsWhatGoes(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	s := bus.ProjectSummary{
		Project: "ai-tradex-solana",
		Agents:  6,
		LastTS:  now.Add(-2 * time.Minute).UnixMilli(),
	}
	var out strings.Builder
	confirmDelete(s, 31, now, &out, strings.NewReader("n\n"))

	for _, want := range []string{"31 keys", "ai-tradex-solana", "6 agents", "2m ago", "DEL", "[y/N]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt missing %q:\n%s", want, out.String())
		}
	}
}

func TestConfirmDeletePromptSaysNeverForAnUndatedProject(t *testing.T) {
	var out strings.Builder
	confirmDelete(bus.ProjectSummary{Project: "p"}, 1, time.Now(), &out, strings.NewReader("n\n"))
	if !strings.Contains(out.String(), "never") {
		t.Errorf("prompt for an undated project should read 'never':\n%s", out.String())
	}
}

func TestConfirmDeletePromptPluralisesKeys(t *testing.T) {
	var one, many strings.Builder
	confirmDelete(bus.ProjectSummary{Project: "p"}, 1, time.Now(), &one, strings.NewReader("n\n"))
	confirmDelete(bus.ProjectSummary{Project: "p"}, 2, time.Now(), &many, strings.NewReader("n\n"))
	if !strings.Contains(one.String(), "1 key ") {
		t.Errorf("want '1 key' (singular):\n%s", one.String())
	}
	if !strings.Contains(many.String(), "2 keys ") {
		t.Errorf("want '2 keys' (plural):\n%s", many.String())
	}
}
