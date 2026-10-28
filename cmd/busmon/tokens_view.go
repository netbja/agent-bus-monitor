package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// The U page (spec docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md):
// the account windows with a 24-hour sparkline, every project side by side
// with a 7-day bar chart, and the open project by agent today. Read every
// requestPoll tick through the optional tokensReader; a failed read keeps the
// last value. No cost, by rule; pending, unknown and zero never share a glyph.

// tokensReader is what the U page reads (a *bus.Bus).
type tokensReader interface {
	ProjectNames(ctx context.Context) ([]string, error)
	TokensOf(ctx context.Context, project string) (map[string]bus.TokenRow, bus.TokensMeta, bool, error)
	QuotaHist(ctx context.Context, since int64) ([]bus.QuotaSample, error)
}

// tokProject is one project's {p}:tokens as last read.
type tokProject struct {
	name    string
	known   bool // the key exists
	partial bool
	days    map[string]bus.TokenRow
	rows    map[string]bus.TokenRow // the open project's rows (per agent)
}

// tokState is the U page's facts.
type tokState struct {
	polled   bool
	projects []tokProject
	hist     []bus.QuotaSample
	failed   bool // the last read failed: the values are the kept ones
}

// readTokens reads every project's tokens and the open project's account
// history; prev is kept on a failure.
func readTokens(ctx context.Context, b busReader, project string, now time.Time, prev tokState) tokState {
	tr, ok := b.(tokensReader)
	if !ok {
		return prev
	}
	names, err := tr.ProjectNames(ctx)
	if err != nil {
		prev.failed = true
		return prev
	}
	if !contains(names, project) {
		names = append(names, project)
	}
	out := tokState{polled: true}
	for _, n := range names {
		rows, meta, known, err := tr.TokensOf(ctx, n)
		if err != nil {
			prev.failed = true
			return prev
		}
		p := tokProject{name: n, known: known, partial: meta.Partial, days: tokens.ProjectDays(rows)}
		if n == project {
			p.rows = rows
		}
		out.projects = append(out.projects, p)
	}
	if out.hist, err = tr.QuotaHist(ctx, now.Add(-24*time.Hour).UnixMilli()); err != nil {
		out.hist, out.failed = prev.hist, true
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// sparkSteps are the eight blocks, lowest first.
var sparkSteps = []string{GlyphSpark1, GlyphSpark2, GlyphSpark3, GlyphSpark4, GlyphSpark5, GlyphSpark6, GlyphGauge, GlyphBarFull}

// sparkOf is v on a scale to max: a space for none, the lowest block for a
// little.
func sparkOf(v, max float64) string {
	if v <= 0 || max <= 0 {
		return " "
	}
	i := int(v / max * float64(len(sparkSteps)))
	return sparkSteps[clamp(i, 0, len(sparkSteps)-1)]
}

// hourSpark is a window's last 24 hours, one cell per hour (the hour's last
// sample, 0-100 %), oldest first; an hour with no sample is a space.
func hourSpark(hist []bus.QuotaSample, provider, window string, now time.Time) string {
	cells := make([]string, 24)
	for i := range cells {
		cells[i] = " "
	}
	start := now.Add(-24 * time.Hour).UnixMilli()
	for _, s := range hist { // oldest first: a later sample of the hour wins
		if s.Provider != provider || s.Window != window || s.At < start {
			continue
		}
		h := int((s.At - start) / time.Hour.Milliseconds())
		if h >= 0 && h < 24 {
			cells[h] = sparkSteps[clamp(int(s.Pct/100*float64(len(sparkSteps))), 0, len(sparkSteps)-1)]
		}
	}
	return strings.Join(cells, "")
}

// tokCount: 0, 873, 206k, 5.1M.
func tokCount(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}

// tokensPage is the U page's body at width cells.
func tokensPage(st tokState, budgets map[string]bus.BudgetSnapshot, holds map[string]int64, agents map[string]*agentState,
	project string, now time.Time, width int) []Line {
	var b body
	head := func(s string) { b.seg(RoleAccent, s).text("\n") }
	muted := func(s string) { b.seg(RoleMuted, s).text("\n") }

	head("Account windows")
	provs := make([]string, 0, len(budgets))
	for p := range budgets {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	if len(provs) == 0 {
		muted("  ? no account reading (no dispatch service collecting quotas)")
	}
	for _, p := range provs {
		for _, w := range budgets[p].Windows {
			b.text("  ").text(Fit(Preview(p), 10)).text(" ").text(Fit(Preview(w.Name), 3)).text(" ")
			if w.ObservedAt == 0 && w.UsedPct == 0 { // never observed: unknown, never 0%
				b.seg(RoleDim, strings.Repeat(GlyphBarEmpty, 10)).seg(RoleMuted, "    ?")
			} else {
				n := clamp(int(w.UsedPct/10+0.5), 0, 10)
				b.seg(budgetRole(w.UsedPct), strings.Repeat(GlyphGauge, n)).seg(RoleDim, strings.Repeat(GlyphBarEmpty, 10-n))
				b.text(fmt.Sprintf(" %3.0f%%", w.UsedPct))
			}
			if width >= 70 {
				b.seg(RoleMuted, "  24h ").seg(RoleAccent, hourSpark(st.hist, p, w.Name, now))
			}
			if until, isHeld := held(holds, p, now); isHeld {
				b.seg(RoleError, "  held "+compactDur(until.Sub(now)))
			} else if w.ResetsAt > 0 {
				if left := time.Unix(w.ResetsAt, 0).Sub(now); left > 0 {
					b.seg(RoleMuted, "  resets "+compactDur(left))
				}
			}
			b.text("\n")
		}
	}
	b.text("\n")

	days := tokens.LastDays(now, time.Local, 7)
	today := days[len(days)-1]
	head("Projects, today and 7 days")
	if !st.polled {
		muted("  " + GlyphEllipsis + " not read yet")
	} else {
		cache := width >= 110
		joined := width < 90 // one in+out column
		ps := append([]tokProject(nil), st.projects...)
		sort.SliceStable(ps, func(i, j int) bool {
			if (ps[i].name == project) != (ps[j].name == project) {
				return ps[i].name == project
			}
			a, c := ps[i].days[today], ps[j].days[today]
			return a.In+a.Write+a.Out > c.In+c.Write+c.Out
		})
		var max int64
		for _, p := range ps {
			for _, d := range days {
				u := p.days[d]
				if t := u.In + u.Write + u.Out; t > max {
					max = t
				}
			}
		}
		hdr := "  " + Fit("PROJECT", 20)
		if joined {
			hdr += fmt.Sprintf(" %8s", "IN+OUT")
		} else {
			hdr += fmt.Sprintf(" %8s %8s", "IN", "OUT")
		}
		if cache {
			hdr += fmt.Sprintf(" %10s", "CACHE RD")
		}
		muted(hdr + "  7 DAYS   7D TOTAL")
		var none []string
		for _, p := range ps {
			if !p.known {
				none = append(none, p.name)
				continue
			}
			u := p.days[today]
			mark := ""
			if p.partial {
				mark = "~"
			}
			role := RoleText
			if p.name == project {
				role = RoleAccent
			}
			b.text("  ").seg(role, Fit(Preview(p.name), 20))
			if joined {
				b.text(fmt.Sprintf(" %8s", mark+tokCount(u.In+u.Write+u.Out)))
			} else {
				b.text(fmt.Sprintf(" %8s %8s", mark+tokCount(u.In+u.Write), mark+tokCount(u.Out)))
			}
			if cache {
				b.seg(RoleMuted, fmt.Sprintf(" %10s", mark+tokCount(u.Read)))
			}
			b.text("  ")
			var total int64
			for _, d := range days {
				v := p.days[d]
				total += v.In + v.Write + v.Out
				b.seg(RoleOK, sparkOf(float64(v.In+v.Write+v.Out), float64(max)))
			}
			b.text(fmt.Sprintf("  %8s\n", mark+tokCount(total)))
		}
		if len(none) > 0 {
			b.text("  ").seg(RoleMuted, Preview(strings.Join(none, " "))+"  no data (no dispatch service counting tokens)").text("\n")
		}
	}
	b.text("\n")

	head(Preview(project) + " by agent, today")
	var open tokProject
	for _, p := range st.projects {
		if p.name == project {
			open = p
		}
	}
	snaps := map[string]bus.AgentSnapshot{}
	for n, a := range agents {
		if a.registered {
			snaps[n] = bus.AgentSnapshot{PID: a.pid, Provider: a.provider, Client: a.client, IdentSession: a.identSession}
		}
	}
	reason := "no tokens recorded (no dispatch service counting them)"
	if !st.polled {
		reason = "not read yet"
	}
	for _, r := range tokens.AgentRows(open.rows, open.known, reason, snaps, today) {
		b.text("  ").text(Fit(Preview(r.Agent), 16)).text(" ").seg(RoleMuted, Fit(Preview(r.Provider), 10))
		if r.In == nil {
			b.seg(RoleMuted, fmt.Sprintf(" %8s %8s  ", "?", "?")).seg(RoleMuted, Preview(r.Reason)).text("\n")
			continue
		}
		b.text(fmt.Sprintf(" %8s %8s", tokCount(*r.In), tokCount(*r.Out))).text("\n")
	}
	b.text("\n")
	muted("IN = input + cache writes, OUT = output; cache reads apart; ~ still counting; no cost, by rule.")
	muted("not attributed to any project: agentbus tokens --unattributed")
	lines := b.Lines()
	for i := range lines {
		lines[i] = FitLine(lines[i], width)
	}
	return lines
}
