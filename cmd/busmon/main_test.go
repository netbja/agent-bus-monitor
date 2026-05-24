package main

import "testing"

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
	want := " ACTIVITY  [yellow][↑ pause · 50 plus bas — Fin/G pour le direct][-] "
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
