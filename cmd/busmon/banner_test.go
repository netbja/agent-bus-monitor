package main

import (
	"strings"
	"testing"
)

// The stopped-project banner (project-switch-ux item 1): a project reached
// by a switch whose services are down says so under the header, with the
// keys out; the screen keeps its footer on the last row. No banner on a
// running project, on an unknown state, or without a switch to go back from.
func TestStoppedBanner(t *testing.T) {
	for _, c := range []struct {
		from, dispatch string
		want           bool
	}{
		{"trading", "down", true}, {"trading", "stopped", true}, {"trading", "failed", true},
		{"trading", "up", false}, {"trading", "", false}, {"", "down", false},
	} {
		for _, w := range []int{140, 100, 60} {
			m := shellWith(t, w, 40, shellConfig{from: c.from}, false)
			m.snap.dispatch = c.dispatch
			m.refresh()
			rows := strings.Split(m.grid().plain(), "\n")
			got := strings.Contains(rows[m.layout.Header.H], "stopped project")
			if got != c.want {
				t.Errorf("from %q dispatch %q width %d: banner %v, want %v\n%s", c.from, c.dispatch, w, got, c.want, strings.Join(rows[:4], "\n"))
				continue
			}
			if m.layout.Footer.Y != 39 {
				t.Errorf("width %d: footer on row %d", w, m.layout.Footer.Y)
			}
			if c.want && w >= 100 {
				for _, part := range []string{"stopped project · read-only", "M projects", "B back to trading"} {
					if !strings.Contains(rows[m.layout.Header.H], part) {
						t.Errorf("width %d: banner %q lacks %q", w, rows[m.layout.Header.H], part)
					}
				}
			}
		}
	}
}
