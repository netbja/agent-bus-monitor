package main

import (
	"fmt"
	"testing"
)

var layoutSizes = []struct {
	w, h int
	tier Tier
}{
	{160, 45, TierWide}, {120, 40, TierWide}, {120, 12, TierNarrow}, {119, 40, TierMedium},
	{119, 12, TierNarrow}, {160, 13, TierNarrow}, {100, 30, TierMedium}, {80, 24, TierMedium},
	{80, 11, TierNarrow}, {79, 24, TierNarrow}, {64, 22, TierNarrow}, {40, 12, TierNarrow},
	{30, 6, TierNarrow}, {30, 5, TierTooSmall}, {1, 40, TierTooSmall}, {10, 40, TierTooSmall},
	{29, 40, TierTooSmall},
}

func TestComputeLayoutTiers(t *testing.T) {
	for _, s := range layoutSizes {
		got := computeLayout(s.w, s.h, Counts{Agents: 14, Board: 4}, PanelActivity)
		if got.Tier != s.tier {
			t.Errorf("%dx%d: tier %v, want %v", s.w, s.h, got.Tier, s.tier)
		}
	}
}

func inside(r Rect, w, h int) bool {
	return r.X >= 0 && r.Y >= 0 && r.W >= 0 && r.H >= 0 && r.X+r.W <= w && r.Y+r.H <= h
}

func overlap(a, b Rect) bool {
	if a.W == 0 || a.H == 0 || b.W == 0 || b.H == 0 {
		return false
	}
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func TestComputeLayoutInvariants(t *testing.T) {
	counts := []Counts{{Agents: 0}, {Agents: 1}, {Agents: 3, Board: 0}, {Agents: 14, Board: 4},
		{Agents: 50, Board: 30}, {Agents: 5, Composing: true}}
	actives := []PanelID{PanelAgents, PanelActivity, PanelBoard, PanelRequests}
	for w := 20; w <= 200; w += 7 {
		for h := 3; h <= 60; h += 3 {
			for _, c := range counts {
				for _, act := range actives {
					name := fmt.Sprintf("%dx%d %+v active=%d", w, h, c, act)
					l := computeLayout(w, h, c, act)
					if l.Tier == TierTooSmall {
						if len(l.Visible) != 0 {
							t.Errorf("%s: too small but panels visible", name)
						}
						continue
					}
					rects := map[string]Rect{"header": l.Header, "tabs": l.Tabs, "agents": l.Agents,
						"activity": l.Activity, "board": l.Board, "requests": l.Requests,
						"input": l.Input, "footer": l.Footer}
					if l.Tier == TierMedium {
						delete(rects, "activity")
						delete(rects, "board")
						delete(rects, "requests")
						rects["lower"] = l.Lower
					}
					for k, r := range rects {
						if !inside(r, w, h) {
							t.Errorf("%s: %s %+v outside screen", name, k, r)
						}
						for k2, r2 := range rects {
							if k < k2 && overlap(r, r2) {
								t.Errorf("%s: %s %+v overlaps %s %+v", name, k, r, k2, r2)
							}
						}
					}
					if len(l.Visible) == 0 {
						t.Errorf("%s: nothing visible", name)
					}
					for _, p := range l.Visible {
						r := l.panelRect(p)
						if r.H < 3 || r.W < 3 {
							t.Errorf("%s: visible panel %d has no data row: %+v", name, p, r)
						}
					}
					if l.Footer.H != 1 {
						t.Errorf("%s: footer %+v", name, l.Footer)
					}
					if l.Tier != TierNarrow || c.Composing {
						if l.Input.H != 1 {
							t.Errorf("%s: input %+v", name, l.Input)
						}
					}
					switch l.Tier {
					case TierWide:
						rw := clamp(w*3/10, 40, 56)
						if l.Board.W != rw || l.Requests.W != rw || l.Activity.W != w-rw {
							t.Errorf("%s: wide widths act %d board %d req %d, want right %d", name, l.Activity.W, l.Board.W, l.Requests.W, rw)
						}
						if l.Board.H < 4 || l.Requests.H < 3 || l.Activity.H < 6 {
							t.Errorf("%s: wide minima board %d req %d act %d", name, l.Board.H, l.Requests.H, l.Activity.H)
						}
						checkAgentRows(t, name, l, c, 3, h-3)
					case TierMedium:
						if l.Lower.H < 6 {
							t.Errorf("%s: medium lower %d", name, l.Lower.H)
						}
						checkAgentRows(t, name, l, c, 2, h-4)
					}
				}
			}
		}
	}
}

func checkAgentRows(t *testing.T, name string, l Layout, c Counts, borders, avail int) {
	t.Helper()
	want := max(c.Agents, 1)
	if limit := max(3, avail/3-borders); want > limit {
		want = limit
	}
	if l.AgentRows != want {
		t.Errorf("%s: AgentRows %d, want %d", name, l.AgentRows, want)
	}
	if l.Agents.H != l.AgentRows+borders {
		t.Errorf("%s: Agents frame %d for %d rows", name, l.Agents.H, l.AgentRows)
	}
}

func TestComputeLayoutVisibleOrder(t *testing.T) {
	wide := computeLayout(160, 45, Counts{Agents: 4}, PanelBoard)
	if fmt.Sprint(wide.Visible) != fmt.Sprint([]PanelID{PanelAgents, PanelActivity, PanelBoard, PanelRequests}) {
		t.Errorf("wide visible %v", wide.Visible)
	}
	med := computeLayout(100, 30, Counts{Agents: 4}, PanelBoard)
	if fmt.Sprint(med.Visible) != fmt.Sprint([]PanelID{PanelAgents, PanelBoard}) || med.Board != med.Lower || med.Activity != (Rect{}) {
		t.Errorf("medium visible %v board %+v lower %+v", med.Visible, med.Board, med.Lower)
	}
	// In medium the lower tab can never be Agents: asking for it keeps Activity there.
	med2 := computeLayout(100, 30, Counts{Agents: 4}, PanelAgents)
	if fmt.Sprint(med2.Visible) != fmt.Sprint([]PanelID{PanelAgents, PanelActivity}) {
		t.Errorf("medium with agents active: %v", med2.Visible)
	}
	nar := computeLayout(64, 22, Counts{Agents: 14}, PanelAgents)
	if fmt.Sprint(nar.Visible) != fmt.Sprint([]PanelID{PanelAgents}) || nar.Tabs.H != 1 || nar.AgentRows != nar.Agents.H-2 {
		t.Errorf("narrow %+v", nar)
	}
	if nar.Input.H != 0 {
		t.Errorf("narrow input shown while not composing: %+v", nar.Input)
	}
	if c := computeLayout(64, 22, Counts{Agents: 14, Composing: true}, PanelAgents); c.Input.H != 1 {
		t.Errorf("narrow composing input %+v", c.Input)
	}
}
