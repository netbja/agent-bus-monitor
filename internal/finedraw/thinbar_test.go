package finedraw

import (
	"strings"
	"testing"
)

// ThinBar is the plan's bar (plan-progress-bars, the person): a heavy ━
// fill over a light ─ track; fine puts the remainder's eighth (▏ to ▉) at
// the leading edge, basic keeps whole cells, ascii draws = then -.
func TestThinBar(t *testing.T) {
	text := func(s []Seg) string {
		var b strings.Builder
		for _, x := range s {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	for _, c := range []struct {
		pct   float64
		cells int
		mode  Mode
		want  string
	}{
		{0, 4, ModeFine, "────"},
		{50, 4, ModeFine, "━━──"},
		{30, 4, ModeFine, "━▎──"}, // 9.6 eighths: 1 cell and 2 eighths
		{3, 4, ModeFine, "▏───"},  // under one cell: the edge alone
		{99, 4, ModeFine, "━━━━"}, // rounds to the full 32 eighths
		{100, 4, ModeFine, "━━━━"},
		{66.7, 12, ModeFine, "━━━━━━━━────"},
		{30, 4, ModeBasic, "━───"},
		{70, 4, ModeBasic, "━━━─"},
		{30, 4, ModeASCII, "=---"},
		{100, 4, ModeASCII, "===="},
	} {
		s := ThinBar(c.pct, c.cells, c.mode)
		if got := text(s); got != c.want {
			t.Errorf("%v%% of %d in %v: %q, want %q", c.pct, c.cells, c.mode, got, c.want)
		}
		for _, x := range s {
			if x.Text == "" {
				continue
			}
			want := PartFill
			switch {
			case strings.ContainsAny(x.Text, "─-"):
				want = PartEmpty
			case strings.ContainsAny(x.Text, strings.Join(Eighths[1:], "")):
				want = PartEdge
			}
			if x.Part != want {
				t.Errorf("%v%% in %v: %q is part %v, want %v", c.pct, c.mode, x.Text, x.Part, want)
			}
		}
	}
}
