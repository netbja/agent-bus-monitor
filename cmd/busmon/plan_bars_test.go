package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/internal/finedraw"
	"github.com/netbja/agent-bus-monitor/plan"
)

// plan-progress-bars (the person): the Plan view's bars are thin, a heavy ━
// fill over a dim ─ track, an eighth at the leading edge; the release bar
// ~30 cells, a group's 12, every bar ending in one column before its n/m;
// coloured by state from the theme (green done, accent in progress, dim not
// started); ascii = and - without UTF-8. A group with tasks done and none
// in hand is in progress, not "not started".

// barsPlan: "Half" 2 of 3 done, the rest not started; "Going" one done, one in
// progress; "Idle" nothing started; "Over" done.
func barsPlan() planState {
	p := boardPlan(
		inGroup(boardRow("h1", plan.Done, "coder"), "half"),
		inGroup(boardRow("h2", plan.Done, "coder"), "half"),
		inGroup(boardRow("h3", plan.Upcoming, "coder"), "half"),
		inGroup(boardRow("g0", plan.Done, "coder"), "going"),
		inGroup(boardRow("g1", plan.InProgress, "coder"), "going"),
		inGroup(boardRow("g2", plan.Upcoming, "coder"), "going"),
		inGroup(boardRow("i1", plan.Upcoming, "coder"), "idle"),
		inGroup(boardRow("o1", plan.Done, "coder"), "over"),
	)
	p.file.Plan = "v0.35"
	p.file.Groups = []plan.Group{{ID: "half", Title: "Half"}, {ID: "going", Title: "Going somewhere"}, {ID: "idle", Title: "Idle"}, {ID: "over", Title: "Over"}}
	return p
}

// barOf is line i's bar: its cells' text and each cell's role.
func barOf(l Line) (string, []Role) {
	var text string
	var roles []Role
	for _, s := range l {
		if s.Text == "" || strings.Trim(s.Text, finedraw.Thin+finedraw.Line+"=-"+strings.Join(finedraw.Eighths[1:], "")) != "" {
			continue
		}
		text += s.Text
		for range Width(s.Text) {
			roles = append(roles, s.Role)
		}
	}
	return text, roles
}

var countRE = regexp.MustCompile(`\d+/\d+`)

// countEnd is the column after line's first n/m.
func countEnd(l Line) int {
	t := l.text()
	loc := countRE.FindStringIndex(t)
	if loc == nil {
		return -1
	}
	return Width(t[:loc[1]])
}

func TestPlanBarsThinAlignedAndColoured(t *testing.T) {
	withRendering(t, finedraw.ModeFine)
	v := planBoard(barsPlan(), 0, true, shellNow, 140, TierWide, false, nil)
	head := v.lines[0]
	bar, roles := barOf(head)
	if Width(bar) != 30 || !strings.Contains(bar, finedraw.Thin) || !strings.Contains(bar, finedraw.Line) || strings.ContainsAny(bar, GlyphBarFull+GlyphBarEmpty) {
		t.Fatalf("release bar %q (%d cells)", bar, Width(bar))
	}
	if roles[0] != RoleAccent || roles[len(roles)-1] != RoleDim {
		t.Errorf("release bar roles %v", roles)
	}
	end := countEnd(head)
	for _, c := range []struct {
		prefix, word string
		fill         Role // the first cell's role
	}{
		{GlyphDown + " Half", "in progress", RoleAccent},
		{GlyphDown + " Going somewhere", "in progress", RoleAccent},
		{GlyphDown + " Idle", "not started", RoleDim},
		{GlyphDown + " Over", "done", RoleOK},
	} {
		l, i := lineWith(v, c.prefix)
		if i < 0 {
			t.Fatalf("no %q line:\n%s", c.prefix, v.plain)
		}
		bar, roles := barOf(v.lines[i])
		if Width(bar) != 12 || strings.ContainsAny(bar, GlyphBarFull+GlyphBarEmpty) {
			t.Errorf("%s: bar %q (%d cells)", c.prefix, bar, Width(bar))
		}
		if !strings.Contains(l, c.word) {
			t.Errorf("%s: %q lacks %q", c.prefix, l, c.word)
		}
		if roles[0] != c.fill {
			t.Errorf("%s: fill %v, want %v", c.prefix, roles[0], c.fill)
		}
		for j, r := range roles { // the theme's state colours, never the text colour
			if r == RoleText {
				t.Errorf("%s: cell %d in the text colour", c.prefix, j)
			}
		}
		if got := countEnd(v.lines[i]); got != end {
			t.Errorf("%s: count ends at %d, the release's at %d:\n%s", c.prefix, got, end, v.plain)
		}
	}
	// "Half", 2 of 3: eight cells of ━ (66.7 %), the track dim.
	_, i := lineWith(v, GlyphDown+" Half")
	if bar, roles := barOf(v.lines[i]); bar != strings.Repeat(finedraw.Thin, 8)+strings.Repeat(finedraw.Line, 4) || roles[11] != RoleDim {
		t.Errorf("Half: %q %v", bar, roles)
	}
}

// The leading edge takes the remainder's eighth: 1 of 7 done is 1.71 cells
// of 12, one ━ then ▋.
func TestPlanGroupBarEighthEdge(t *testing.T) {
	withRendering(t, finedraw.ModeFine)
	rows := []plan.Row{inGroup(boardRow("d", plan.Done, "coder"), "g")}
	for _, id := range []string{"a", "b", "c", "e", "f", "h"} {
		rows = append(rows, inGroup(boardRow(id, plan.Upcoming, "coder"), "g"))
	}
	p := boardPlan(rows...)
	p.file.Groups = []plan.Group{{ID: "g", Title: "G"}}
	v := planBoard(p, 0, false, shellNow, 140, TierWide, false, nil)
	_, i := lineWith(v, GlyphDown+" G ")
	if bar, _ := barOf(v.lines[i]); bar != finedraw.Thin+finedraw.Eighth6+strings.Repeat(finedraw.Line, 10) {
		t.Errorf("1/7 of 12: %q", bar)
	}
}

// Without UTF-8 the bars are = and -.
func TestPlanBarsInASCII(t *testing.T) {
	withRendering(t, finedraw.ModeASCII)
	v := planBoard(barsPlan(), 0, true, shellNow, 140, TierWide, false, nil)
	_, i := lineWith(v, GlyphDown+" Half")
	for _, l := range []Line{v.lines[0], v.lines[i]} {
		bar, _ := barOf(l)
		if bar == "" || strings.Trim(bar, "=-") != "" {
			t.Errorf("ascii bar %q in %q", bar, l.text())
		}
	}
}
