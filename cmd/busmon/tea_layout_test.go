package main

import "testing"

// Every zone of the shell's layout lies on the screen, no two overlap, and
// every row of the screen belongs to a zone.
func TestShellLayoutTiles(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 30}, {140, 40}, {30, 6}, {79, 24}, {119, 30}, {120, 14}, {200, 60}} {
		for _, c := range []shellCounts{{ForYou: 0, Agents: 0}, {ForYou: 3, Agents: 6, ForYouWidth: 40}, {ForYou: 9, Agents: 30, ForYouWidth: 90}, {ForYou: 2, Agents: 4, Composing: true}} {
			for _, view := range narrowPanels {
				l := shellLayoutFor(size[0], size[1], c, view, PanelBoard)
				if l.Tier == TierTooSmall {
					t.Fatalf("%v: too small", size)
				}
				zones := []Rect{l.Header, l.Tabs, l.ForYou, l.Agents, l.Lower, l.Input, l.Footer}
				cover := make([][]bool, size[1])
				for y := range cover {
					cover[y] = make([]bool, size[0])
				}
				for _, z := range zones {
					for y := z.Y; y < z.Y+z.H; y++ {
						for x := z.X; x < z.X+z.W; x++ {
							if x < 0 || y < 0 || x >= size[0] || y >= size[1] {
								t.Fatalf("%v %+v: zone %+v off screen", size, c, z)
							}
							if cover[y][x] {
								t.Fatalf("%v %+v view %d: zones overlap at %d,%d: %+v", size, c, view, x, y, l)
							}
							cover[y][x] = true
						}
					}
				}
				for y := range cover {
					if !cover[y][0] {
						t.Errorf("%v %+v view %d: row %d belongs to no zone: %+v", size, c, view, y, l)
						break
					}
				}
			}
		}
	}
}

func TestShellLayoutTiers(t *testing.T) {
	c := shellCounts{ForYou: 3, Agents: 6}
	for _, tc := range []struct {
		w, h int
		tier Tier
	}{{60, 20, TierNarrow}, {80, 24, TierMedium}, {120, 30, TierWide}, {140, 40, TierWide}} {
		l := shellLayoutFor(tc.w, tc.h, c, PanelForYou, PanelActivity)
		if l.Tier != tc.tier {
			t.Errorf("%dx%d: tier %d, want %d", tc.w, tc.h, l.Tier, tc.tier)
		}
		if tc.tier != TierNarrow && l.Header.H != 2 {
			t.Errorf("%dx%d: header %d lines, want 2", tc.w, tc.h, l.Header.H)
		}
	}
	// Wide: For you takes its content width, capped, Agents the rest; the
	// tabbed panel keeps 8 rows.
	for _, tc := range []struct{ content, want int }{{20, forYouMinWidth}, {44, 44}, {90, forYouMaxWidth}} {
		c.ForYouWidth = tc.content
		l := shellLayoutFor(140, 30, c, PanelForYou, PanelBoard)
		if l.ForYou.Y != l.Agents.Y || l.ForYou.H != l.Agents.H || l.ForYou.W != tc.want || l.Agents.W != 140-tc.want ||
			l.Lower.H < minShellLower || l.Active != PanelBoard {
			t.Errorf("wide 140x30, content %d: %+v", tc.content, l)
		}
	}
	// Medium: For you shows 4 items, then "+N more".
	if l := shellLayoutFor(80, 24, shellCounts{ForYou: 9, Agents: 2}, PanelForYou, PanelActivity); l.ForYouRows != 5 {
		t.Errorf("medium For you rows = %d, want 5 (4 + more)", l.ForYouRows)
	}
	if l := shellLayoutFor(80, 24, shellCounts{ForYou: 0, Agents: 2}, PanelForYou, PanelActivity); l.ForYouRows != 1 {
		t.Errorf("medium empty For you rows = %d, want 1", l.ForYouRows)
	}
	// Narrow: one view at a time, For you first; an unknown view falls back to it.
	if l := shellLayoutFor(60, 20, c, PanelID(99), PanelActivity); l.Active != PanelForYou || l.ForYou.H == 0 {
		t.Errorf("narrow default view: %+v", l)
	}
}
