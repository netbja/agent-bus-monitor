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
	project string, now time.Time, width int, pending bool, bars barView, all bool) []Line {
	var b body
	// pending: the first reading is on its way (tokensSkeleton): a shimmering
	// skeleton row stands where the open project's numbers will be.
	skeleton := func() {
		b.text("  ").seg(RoleAccent, Fit(Preview(project), 20)).text(" ").
			segs(Seg{Text: strings.Repeat(GlyphBarEmpty, 24), Role: RoleMuted, Fade: fadeLevel(now)}).
			seg(RoleMuted, "  first reading pending").text("\n")
	}
	head := func(s string) { b.segs(Seg{Text: s, Role: RoleText, Bold: true}).text("\n") }
	muted := func(s string) { b.seg(RoleMuted, s).text("\n") }
	barW := 20 // a zone's bars, against its largest row (busmon pages, Tokens)
	if width < 100 {
		barW = 8
	}
	relBar := func(v, max int64) Line {
		if max <= 0 {
			return Line{S(RoleDim, strings.Repeat(" ", barW))}
		}
		return gaugeSegs(float64(v)*100/float64(max), barW, RoleOK, false)
	}

	head("Accounts")
	provs := make([]string, 0, len(budgets))
	for p := range budgets {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	if len(provs) == 0 {
		muted("  ? no account reading (no dispatch service collecting quotas)")
	}
	// One row per provider, its windows side by side in columns of one width,
	// so the same window lines up down the zone: the richest form that fits
	// (the 24-hour spark, then the reset), else one window per row.
	most := 0
	for _, p := range provs {
		most = max(most, len(budgets[p].Windows))
	}
	form := accountForms[len(accountForms)-1]
	for _, f := range accountForms {
		if 2+10+1+most*f.width()+(most-1)*3 <= width {
			form = f
			break
		}
	}
	perRow := most
	if 2+10+1+most*form.width()+(most-1)*3 > width {
		perRow = 1
	}
	for _, p := range provs {
		ws := budgets[p].Windows
		for i, w := range ws {
			if i%max(perRow, 1) == 0 {
				if i > 0 {
					b.text("\n")
				}
				b.text("  ").seg(providerRole(p, RoleText), Fit(Preview(p), 10)).text(" ")
			} else {
				b.text("   ")
			}
			cell := accountCell(form, st, p, w, holds, now, bars)
			if i%max(perRow, 1) < perRow-1 && i < len(ws)-1 {
				cell = append(cell, S(RoleText, strings.Repeat(" ", max(0, form.width()-cell.Width()))))
			}
			b.segs(cell...)
		}
		if until, isHeld := held(holds, p, now); isHeld && !form.reset { // a hold is never dropped for room
			b.seg(RoleError, "  held "+compactDur(until.Sub(now)))
		}
		b.text("\n")
	}

	days := tokens.LastDays(now, time.Local, 7)
	today := days[len(days)-1]
	joined := width < 90 // one in+out column
	cols := fmt.Sprintf(" %8s %8s", "in", "out")
	if joined {
		cols = fmt.Sprintf(" %8s", "in+out")
	}
	b.segs(Seg{Text: "Projects", Role: RoleText, Bold: true}).seg(RoleMuted, strings.Repeat(" ", 22-Width("Projects"))+cols+"  7 days").text("\n")
	// unknownRow keeps the open project visible, unknown, with its reason,
	// once no reading is pending any more (never folded, never 0).
	unknownRow := func(reason string) {
		b.text("  ").seg(RoleAccent, Fit(Preview(project), 20)).seg(RoleMuted, fmt.Sprintf(" %8s %8s  ", "?", "?")).seg(RoleMuted, reason).text("\n")
	}
	if !st.polled && pending {
		skeleton()
	} else if !st.polled {
		unknownRow("not read yet")
	} else {
		ps := append([]tokProject(nil), st.projects...)
		sort.SliceStable(ps, func(i, j int) bool {
			if (ps[i].name == project) != (ps[j].name == project) {
				return ps[i].name == project
			}
			a, c := ps[i].days[today], ps[j].days[today]
			return a.In+a.Write+a.Out > c.In+c.Write+c.Out
		})
		var maxDay, maxTotal, maxToday int64
		top := ""
		for _, p := range ps {
			var total int64
			for _, d := range days {
				u := p.days[d]
				t := u.In + u.Write + u.Out
				total += t
				maxDay = max(maxDay, t)
			}
			maxTotal = max(maxTotal, total)
			if u := p.days[today]; p.known && u.In+u.Write+u.Out > maxToday {
				maxToday, top = u.In+u.Write+u.Out, p.name
			}
		}
		var none []string
		for _, p := range ps {
			if !p.known && p.name == project {
				if pending {
					skeleton()
				} else {
					unknownRow("no tokens recorded (no dispatch service counting them)")
				}
				continue
			}
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
			num := func(s string) Seg { return Seg{Text: s, Role: RoleText, Bold: p.name == top} } // the zone's top consumer
			b.text("  ").seg(role, Fit(Preview(p.name), 20))
			if joined {
				b.segs(num(fmt.Sprintf(" %8s", mark+tokCount(u.In+u.Write+u.Out))))
			} else {
				b.segs(num(fmt.Sprintf(" %8s", mark+tokCount(u.In+u.Write)))).text(fmt.Sprintf(" %8s", mark+tokCount(u.Out)))
			}
			b.text("  ")
			var total int64
			for _, d := range days {
				v := p.days[d]
				total += v.In + v.Write + v.Out
				b.seg(RoleOK, sparkOf(float64(v.In+v.Write+v.Out), float64(maxDay)))
			}
			b.text("  ").segs(relBar(total, maxTotal)...).text(fmt.Sprintf("  %8s\n", mark+tokCount(total)))
		}
		if len(none) > 0 {
			b.text("  ").seg(RoleMuted, Preview(strings.Join(none, " "))+"  no data (no dispatch service counting tokens)").text("\n")
		}
	}

	agentsHead := "Agents, " + Preview(project)
	b.segs(Seg{Text: agentsHead, Role: RoleText, Bold: true}). // the columns over in and out: name 16, provider 10
									seg(RoleMuted, strings.Repeat(" ", max(1, 2+16+1+10-Width(agentsHead)))+fmt.Sprintf(" %8s %8s", "in", "out")).text("\n")
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
	if pending {
		reason = "first reading pending"
	}
	rows := tokens.AgentRows(open.rows, open.known, reason, snaps, today)
	var maxAgent int64
	top := ""
	for _, r := range rows {
		if r.In != nil && *r.In+*r.Out > maxAgent {
			maxAgent, top = *r.In+*r.Out, r.Agent
		}
	}
	var folded []string // the short reasons of the rows a folds, in order
	foldedN := map[string]int{}
	for _, r := range rows {
		if r.In == nil {
			why := shortTokReason(r.Reason)
			if !all {
				if foldedN[why] == 0 {
					folded = append(folded, why)
				}
				foldedN[why]++
				continue
			}
			b.text("  ").text(Fit(Preview(r.Agent), 16)).text(" ").seg(providerRole(r.Provider, RoleMuted), Fit(Preview(r.Provider), 10)).
				seg(RoleMuted, fmt.Sprintf(" %8s %8s  ", "?", "?")).seg(RoleMuted, Preview(why)).text("\n")
			continue
		}
		bold := r.Agent == top
		b.text("  ").text(Fit(Preview(r.Agent), 16)).text(" ").seg(providerRole(r.Provider, RoleMuted), Fit(Preview(r.Provider), 10)).
			segs(Seg{Text: fmt.Sprintf(" %8s", tokCount(*r.In)), Role: RoleText, Bold: bold}).text(fmt.Sprintf(" %8s", tokCount(*r.Out))).
			text("  ").segs(relBar(*r.In+*r.Out, maxAgent)...).text("\n")
	}
	for _, why := range folded {
		muted(fmt.Sprintf("  %d more %s %s  (a lists them)", foldedN[why], GlyphNone, why))
	}
	b.text("\n")
	if width < 100 {
		muted("in: input + cache writes " + GlyphNone + " ~ counting " + GlyphNone + " no cost")
	} else {
		muted("in = input + cache writes " + GlyphNone + " out = output " + GlyphNone + " ~ still counting " + GlyphNone + " no cost, by rule")
	}
	lines := b.Lines()
	for i := range lines {
		lines[i] = FitLine(lines[i], width)
	}
	return lines
}

// accountForm is how much of an account window a cell shows: its gauge and
// figure always, then the 24-hour spark, then the reset or the hold.
type accountForm struct{ spark, reset bool }

var accountForms = []accountForm{{true, true}, {false, true}, {false, false}}

// width is the cell's cells: name 3, gauge 10, figure 5, spark 6+24, reset 15.
func (f accountForm) width() int {
	w := 4 + 10 + 5
	if f.spark {
		w += 6 + 24
	}
	if f.reset {
		w += 15
	}
	return w
}

// accountWindowName is a window's name in the 3-cell column: a window named
// in words (xai's billing periods) gets a short label, so weekly reads wk,
// not a cut "we…"; any other name is shown as it is, fitted.
func accountWindowName(name string) string {
	switch name {
	case "weekly":
		return "wk"
	case "monthly":
		return "mo"
	case "daily":
		return "day"
	}
	return name
}

// accountCell is one account window in a form: never observed reads ?,
// never 0%.
func accountCell(f accountForm, st tokState, p string, w bus.BudgetWindow, holds map[string]int64, now time.Time, bars barView) Line {
	var c body
	c.text(Fit(Preview(accountWindowName(w.Name)), 3)).text(" ")
	if windowUnobserved(w) {
		c.seg(RoleDim, strings.Repeat(GlyphBarEmpty, 10)).seg(RoleMuted, "    ?")
	} else {
		c.segs(bars.gauge(barKey(p, w.Name), w.UsedPct, 10, budgetRole(w.UsedPct))...)
		c.text(fmt.Sprintf(" %3.0f%%", w.UsedPct))
	}
	if f.spark {
		c.seg(RoleMuted, "  24h ").seg(RoleAccent, Fit(hourSpark(st.hist, p, w.Name, now), 24))
	}
	if f.reset {
		if until, isHeld := held(holds, p, now); isHeld {
			c.seg(RoleError, Fit("  held "+compactDur(until.Sub(now)), 15))
		} else if w.ResetsAt > 0 {
			if left := time.Unix(w.ResetsAt, 0).Sub(now); left > 0 {
				c.seg(RoleMuted, Fit("  resets "+compactDur(left), 15))
			}
		}
	}
	var l Line
	for _, line := range c.Lines() {
		l = append(l, line...)
	}
	return l
}

// shortTokReason is an unknown agent's reason as its row reads it: grey and
// short, the long sentence in the CLI (agentbus tokens).
func shortTokReason(r string) string {
	if r == "no session proved with its process" {
		return "no proved session"
	}
	return r
}

// tokensTitle is the U page's counter summary: the period, the fullest
// window of a provider not held, and each held provider with its time left.
func tokensTitle(budgets map[string]bus.BudgetSnapshot, holds map[string]int64, now time.Time) string {
	parts := []string{"Tokens", "today"}
	provs := make([]string, 0, len(budgets))
	for p := range budgets {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	best, bestPct := "", -1.0
	for _, p := range provs {
		if _, isHeld := held(holds, p, now); isHeld {
			continue
		}
		for _, w := range budgets[p].Windows {
			if !windowUnobserved(w) && w.UsedPct > bestPct {
				best, bestPct = fmt.Sprintf("%s %.0f%% %s", p, w.UsedPct, w.Name), w.UsedPct
			}
		}
	}
	if best != "" {
		parts = append(parts, best)
	}
	for _, p := range provs {
		if until, isHeld := held(holds, p, now); isHeld {
			parts = append(parts, p+" held "+compactDur(until.Sub(now)))
		}
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

// tokensOpen: the U page is on the reader's stack.
func (m Model) tokensOpen() bool {
	if m.rd == nil {
		return false
	}
	for _, p := range m.rd.pages {
		if p.kind == ovTokens {
			return true
		}
	}
	return false
}

// skeletonFor is how long after busmon starts a pending first reading
// shimmers; after it, an absent reading is unknown ('?'), not pending.
const skeletonFor = 2 * time.Minute

// tokensSkeleton: the U page is open, the dispatcher is up, the open
// project's tokens have no reading yet, and busmon started less than
// skeletonFor ago. Only then does the page shimmer.
func (m Model) tokensSkeleton() bool {
	if !m.tokensOpen() || m.sig.started.IsZero() || m.now.Sub(m.sig.started) >= skeletonFor {
		return false
	}
	switch m.snap.dispatch {
	case "", "down", "stopped", "failed", "gave-up":
		return false
	}
	for _, p := range m.snap.tok.projects {
		if p.name == m.cfg.project {
			return !p.known
		}
	}
	return true // not read yet
}
