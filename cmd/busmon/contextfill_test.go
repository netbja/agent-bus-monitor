package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// pinAlert sets busmon's own threshold for one test, whatever the shell exports.
func pinAlert(t *testing.T, v float64) {
	t.Helper()
	old := ctxAlert
	ctxAlert = v
	t.Cleanup(func() { ctxAlert = old })
}

func pinUrgent(t *testing.T, v float64) {
	t.Helper()
	old := ctxUrgent
	ctxUrgent = v
	t.Cleanup(func() { ctxUrgent = old })
}

func withAlert(f *bus.ContextFill, v float64) *bus.ContextFill { f.Alert = &v; return f }

func freshFill(pct float64) *bus.ContextFill {
	at := time.Now().UnixMilli()
	return &bus.ContextFill{Pct: pct, Session: "s1", ObservedAt: at - 60_000, At: at}
}

// The fill is read with the dispatcher facts; a failed read keeps the last
// value, an agent no longer published reads unknown.
func TestPollReadsTheContextFill(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{
		"coder": {State: "idle", TS: 1, Pane: "p1", PID: 7}, "codex": {State: "idle", TS: 1, Pane: "p2", PID: 8}},
		contexts: map[string]bus.ContextFill{"coder": *freshFill(85)}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	tick := 0
	poll := func() {
		for i := 0; i < 5; i++ { // the dispatcher facts are read every 5th tick
			pollOnce(context.Background(), f, st, tick)
			tick++
		}
	}
	poll()
	if c := st.agents["coder"].ctxFill; c == nil || c.Pct != 85 || st.agents["codex"].ctxFill != nil {
		t.Fatalf("coder %+v codex %+v", c, st.agents["codex"].ctxFill)
	}
	f.contextErr = errors.New("timeout")
	poll()
	if c := st.agents["coder"].ctxFill; c == nil || c.Pct != 85 {
		t.Fatalf("a failed read dropped the fill: %+v", c)
	}
	f.contextErr, f.contexts = nil, nil
	poll()
	if c := st.agents["coder"].ctxFill; c != nil {
		t.Fatalf("an unpublished fill is kept: %+v", c)
	}
}

// The wide row shows a known fill as a bar on the context ramp and its
// percent (tui-rendering §4.5), the percent in the attention colour at the
// alert threshold and the error colour at the urgent one; an unknown or aged
// fill shows the raw tokens, never a percent.
func TestAgentRowShowsTheContextFill(t *testing.T) {
	pinAlert(t, 80)
	pinUrgent(t, 90)
	withRendering(t, finedraw.ModeFine)
	withPalette(t, "dark", false, 1<<24)
	now := time.Now()
	aged := freshFill(95)
	aged.ObservedAt = aged.At - bus.ContextMaxAge.Milliseconds() - 1
	for _, c := range []struct {
		fill *bus.ContextFill
		raw  string
		want string // the percent after the bar, or the whole cell
		role Role
	}{
		{freshFill(85), "180k", "85%", RoleAttention},
		{freshFill(42.6), "41000", "43%", RoleText},
		{freshFill(85), "", "85%", RoleAttention},
		{freshFill(92), "180k", "92%", RoleError},
		{aged, "180k", "180k", RoleText},
		{nil, "180k", "180k", RoleText},
		{nil, "41000", "41k", RoleText},
		{nil, "", GlyphDash, RoleMuted},
		// The dispatcher's published threshold wins over busmon's own.
		{withAlert(freshFill(85), 90), "180k", "85%", RoleText},
		{withAlert(freshFill(72), 70), "180k", "72%", RoleAttention},
		{withAlert(freshFill(99), 0), "180k", "99%", RoleText}, // the alert is off: no amber, no red
	} {
		a := &agentState{state: "idle", stateAt: now, ctxRaw: c.raw, ctxFill: c.fill}
		cell := Line(ctxCell(a))
		got := cell.text()
		if c.fill != nil && c.fill.Known() {
			bar := Line(rampGauge(c.fill.Pct, ctxBarW, ctxRamp(c.fill.AlertAt(ctxAlert)), false)).text()
			if got != bar+" "+c.want || Width(bar) != ctxBarW || strings.Contains(got, "180k") {
				t.Errorf("%+v: %q, want a %d-cell bar and %q", c.fill, got, ctxBarW, c.want)
			}
		} else if got != c.want {
			t.Errorf("%+v %q: %q, want %q", c.fill, c.raw, got, c.want)
		}
		if cell[len(cell)-1].Role != c.role {
			t.Errorf("%+v: percent in %v, want %v", c.fill, cell[len(cell)-1].Role, c.role)
		}
		row := agentRow(iconsOff, "coder", a, false, TierWide, 160, now).text()
		if !strings.Contains(row, got) {
			t.Errorf("row lacks %q: %q", got, row)
		}
	}
}

// The context ramp (tui-rendering §4.5): green, amber from the alert
// threshold, red from the urgent one, cell by cell in RGB in fine from 256
// colours up; in 16 colours the whole fill in the band's role; without
// colour, none.
func TestContextBarRamp(t *testing.T) {
	pinUrgent(t, 90)
	withRendering(t, finedraw.ModeFine)
	inks := func(l Line) (n int, roles map[Role]bool) {
		roles = map[Role]bool{}
		for _, s := range l {
			if s.Ink != colorDefault {
				n++
			}
			if s.Text != "" && !s.Track && s.Role != RoleDim {
				roles[s.Role] = true
			}
		}
		return n, roles
	}
	// An urgent threshold not above the alert is no red (the escalation's rule).
	pinUrgent(t, 70)
	if r := ctxRamp(80); r.role(85) != RoleAttention || r.role(75) != RoleGauge {
		t.Errorf("urgent under the alert: %v at 85, %v at 75", r.role(85), r.role(75))
	}
	pinUrgent(t, 90)
	withPalette(t, "dark", false, 1<<24)
	l := rampGauge(95, ctxBarW, ctxRamp(80), false)
	if n, _ := inks(l); n != ctxBarW { // 95 of 6 cells: 5 whole and an eighth, each its own colour
		t.Fatalf("true colour: %d inked cells: %+v", n, l)
	}
	if l[0].Ink != newRGBColor(int32(th.Hue(RoleGauge, 100).R), int32(th.Hue(RoleGauge, 100).G), int32(th.Hue(RoleGauge, 100).B)) && l[0].Ink == l[len(l)-2].Ink {
		t.Errorf("the ramp does not move along the bar: %+v", l)
	}
	if first, last := l[0].Ink, l[ctxBarW-1].Ink; first == last {
		t.Errorf("first and last cell alike: %v", first)
	}
	withPalette(t, "dark", false, 256)
	if n, _ := inks(rampGauge(95, ctxBarW, ctxRamp(80), false)); n == 0 {
		t.Error("256 colours: no ramp (colorprofile downsamples it)")
	}
	withPalette(t, "dark", false, 16)
	for pct, want := range map[float64]Role{50: RoleGauge, 85: RoleAttention, 95: RoleError} {
		n, roles := inks(rampGauge(pct, ctxBarW, ctxRamp(80), false))
		if n != 0 || len(roles) != 1 || !roles[want] {
			t.Errorf("16 colours at %v: %d inked, roles %v, want %v", pct, n, roles, want)
		}
	}
	withPalette(t, "dark", true, 1<<24)
	if n, _ := inks(rampGauge(95, ctxBarW, ctxRamp(80), false)); n != 0 {
		t.Error("NO_COLOR: inked")
	}
	withPalette(t, "dark", false, 1<<24)
	withRendering(t, finedraw.ModeBasic)
	if n, _ := inks(rampGauge(95, ctxBarW, ctxRamp(80), false)); n != 0 {
		t.Error("basic: inked")
	}
	// The ink reaches the terminal: a cell's style takes it, but not under NO_COLOR.
	withPalette(t, "dark", false, 1<<24)
	red := newRGBColor(200, 0, 0)
	if th.termStyle(cellStyle{Role: RoleGauge, Ink: red}) == th.termStyle(cellStyle{Role: RoleGauge}) {
		t.Error("the ink is not drawn")
	}
	withPalette(t, "dark", true, 1<<24)
	if th.termStyle(cellStyle{Role: RoleGauge, Ink: red}) != th.termStyle(cellStyle{Role: RoleGauge}) {
		t.Error("the ink is drawn under NO_COLOR")
	}
}

// The agent page states the fill, or that it is unknown, never 0.
func TestAgentDetailShowsTheContextFill(t *testing.T) {
	pinAlert(t, 80)
	now := time.Now()
	body := func(a *agentState) string {
		_, lines := agentDetail("coder", a, false, nil, nil, now)
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l.text() + "\n")
		}
		return b.String()
	}
	if got := body(&agentState{state: "idle", stateAt: now, ctxFill: freshFill(85)}); !strings.Contains(got, "85%\n") || !strings.Contains(got, "of the context window") || !strings.Contains(got, "at or above the 80% alert") {
		t.Errorf("known fill:\n%s", got)
	}
	if got := body(&agentState{state: "idle", stateAt: now, ctxFill: withAlert(freshFill(72), 70)}); !strings.Contains(got, "at or above the 70% alert") {
		t.Errorf("the dispatcher's threshold:\n%s", got)
	}
	if got := body(&agentState{state: "idle", stateAt: now}); !strings.Contains(got, "Fill") || !strings.Contains(got, "unknown") || strings.Contains(got, "0%") {
		t.Errorf("unknown fill:\n%s", got)
	}
}

// The account cards' window gauges take the usage ramp (tui-rendering
// §4.5): in true colour each filled cell has its own colour, green towards
// red; in 16 colours the fill keeps budgetRole's colour.
func TestAccountCardGaugesTakeTheRamp(t *testing.T) {
	pinLocal(t)
	withRendering(t, finedraw.ModeFine)
	now := shellNow
	b := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 97, ResetsAt: now.Unix() + 3600, ObservedAt: now.UnixMilli()}}}
	gauge := func() (inks map[Color]bool, roles map[Role]bool) {
		inks, roles = map[Color]bool{}, map[Role]bool{}
		for _, l := range accountCard(iconsOff, "anthropic", b, nil, now, 80, barView{}) {
			for _, s := range l {
				if strings.Contains(s.Text, GlyphThinBar) && s.Fade == 0 {
					inks[s.Ink] = true
					roles[s.Role] = true
				}
			}
		}
		return inks, roles
	}
	withPalette(t, "dark", false, 1<<24)
	if inks, _ := gauge(); len(inks) < 3 || inks[colorDefault] {
		t.Errorf("true colour: %d colours %v", len(inks), inks)
	}
	// The track (the fill's colour faded) takes no ink: only the fill ramps.
	b.Windows[0].UsedPct = 40
	for _, l := range accountCard(iconsOff, "anthropic", b, nil, now, 80, barView{}) {
		for _, s := range l {
			if s.Fade > 0 && s.Ink != colorDefault {
				t.Errorf("a track cell inked: %+v", s)
			}
		}
	}
	b.Windows[0].UsedPct = 97
	withPalette(t, "dark", false, 16)
	if inks, roles := gauge(); len(inks) != 1 || !inks[colorDefault] || !roles[budgetRole(97)] {
		t.Errorf("16 colours: inks %v roles %v", inks, roles)
	}
}
