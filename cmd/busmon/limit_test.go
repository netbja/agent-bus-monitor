package main

import "testing"

func TestResolveLimit(t *testing.T) {
	cases := []struct {
		name    string
		flagSet bool
		flagVal int
		env     string
		want    int
	}{
		{"default", false, 25, "", 25},
		{"flag wins over env", true, 50, "10", 50},
		{"flag zero means all", true, 0, "", 0},
		{"env when no flag", false, 25, "10", 10},
		{"env zero means all", false, 25, "0", 0},
		{"bad env falls to default", false, 25, "abc", 25},
		{"whitespace env", false, 25, "  7 ", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLimit(c.flagSet, c.flagVal, c.env); got != c.want {
				t.Errorf("resolveLimit(%v,%d,%q) = %d, want %d", c.flagSet, c.flagVal, c.env, got, c.want)
			}
		})
	}
}

// Obligation/Wake/DispatchAlive cost a few reads per agent; they are polled
// every 5 s (the dispatcher's own tick), not every 1 s redraw — first tick
// included, so the badges appear at startup.
func TestWakePollEveryFiveTicks(t *testing.T) {
	var due []int
	for tick := 1; tick <= 12; tick++ {
		if wakePollDue(tick) {
			due = append(due, tick)
		}
	}
	if len(due) != 3 || due[0] != 1 || due[1] != 6 || due[2] != 11 {
		t.Fatalf("wake poll ticks = %v, want [1 6 11]", due)
	}
}
