package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// parseCtx reads UsageSnapshot.Ctx as a token count: an optional trailing
// " ctx", then an integer or decimal with an optional k/K or M suffix. Anything
// else (a percentage, a legacy window string, empty) is not a token count.
func parseCtx(raw string) (tokens int64, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSpace(strings.TrimSuffix(s, "ctx"))
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1e3, s[:len(s)-1]
	case 'M':
		mult, s = 1e6, s[:len(s)-1]
	}
	if s == "" || strings.Trim(s, "0123456789.") != "" { // no sign, exponent, inf or nan
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return int64(v*mult + 0.5), true
}

// formatTokens is the compact form: as is below 1000, whole k below 1M
// (rounded), one decimal M above.
func formatTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 999_500:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}

// agentFlag is one word of the flags column (spec §3.3.2). Group is the sort
// group the flag forces (0 = none).
type agentFlag struct {
	Word  string
	Role  Role
	Prio  int
	Group int
}

// agentFlags lists the agent's flags by priority. None of them changes the
// declared state: they are leases, counts and dispatcher records beside it.
// observedFresh is how long a pane observation counts: the dispatcher
// observes every 15 s, busmon reads every 5 s.
const observedFresh = 60 * time.Second

// busyUndeclared reports whether the dispatcher saw the agent at work while it
// does not declare working: shown beside the declared state, never instead.
// clientOutdated: a fresh observation says a newer client is installed than
// the one this agent's process runs (client reload spec). Observed, never
// declared: the state is untouched.
func clientOutdated(a *agentState, now time.Time) bool {
	return a.clientUpdate != nil && !a.obsAt.IsZero() && now.Sub(a.obsAt) <= observedFresh
}

func busyUndeclared(a *agentState, now time.Time) bool {
	return a.obsBusy && a.state != "working" && !a.obsAt.IsZero() && now.Sub(a.obsAt) <= observedFresh
}

// modelDrift (v0.15.1 §7.2): the last doctor verdict says this process does
// not run the model roles.toml configures, or its model was refused. A
// verdict about another pid (the agent was relaunched since) says nothing.
func modelDrift(a *agentState) bool {
	return a.model != nil && (a.model.Verdict == "drift" || a.model.Verdict == "unknown model" || a.model.Verdict == "gone" || a.model.Verdict == "newer") &&
		a.model.PID != 0 && a.model.PID == a.pid
}

// promptWaiting (v0.15 §13): the agent's own client is asking something in
// its tab (a limit menu, a permission prompt), seen by the dispatcher as a
// blocked pane, while the agent itself declares neither blocked nor waiting
// and has no open ask. Only a person can answer it, in that tab: it reads as
// waiting, beside the declared state, never instead of it.
func promptWaiting(a *agentState) bool {
	return a.obsPrompt && obsFresh(a, time.Now()) && a.state != "blocked" && a.state != "waiting" && a.openAsk == 0
}

// obsFresh: the last observation is younger than its TTL. It is checked when
// shown, not when read, so a busmon whose reads fail stops showing a prompt
// once the reading it holds is too old.
func obsFresh(a *agentState, now time.Time) bool {
	return !a.obsAt.IsZero() && now.Sub(a.obsAt) < time.Duration(bus.ObservedTTL)*time.Millisecond
}

// waitsForPerson: a declared wait, an open ask or a prompt in its tab.
func waitsForPerson(a *agentState) bool {
	return a.openAsk > 0 || a.state == "waiting" && !answered(a) || promptWaiting(a)
}

func agentFlags(a *agentState, pilot bool) []agentFlag {
	var fs []agentFlag
	add := func(w string, r Role, prio, group int) { fs = append(fs, agentFlag{w, r, prio, group}) }
	if a.wake != nil {
		switch a.wake.Escalated {
		case "true":
			add("escalated", RoleError, 1, 1)
		case "pending":
			add("escalating", RoleError, 2, 1)
		}
	}
	if _, word, _, _ := stateLook(a); a.held && word != "quota" { // the state column already says quota
		add("held", RoleAttention, 2, 2)
	}
	if promptWaiting(a) {
		add("prompt", RoleAttention, 1, 0)
	}
	if a.forYou {
		add("for you", RoleAttention, 2, 0)
	}
	if a.swapping {
		add("swapping", RoleDispatch, 1, 1)
	}
	if a.fallback != nil {
		add("fallback "+Preview(a.fallback.Profile), RoleDispatch, 3, 0)
	}
	if modelDrift(a) {
		add("model"+GlyphNotEqual, RoleAttention, 4, 0)
	}
	if busyUndeclared(a, time.Now()) {
		add("busy", RoleOK, 3, 2)
	}
	if clientOutdated(a, time.Now()) {
		add("update", RoleAttention, 5, 0)
	}
	if a.gated > 0 {
		add(fmt.Sprintf("gate %d", a.gated), RoleError, 3, 1)
	}
	if a.wake != nil {
		switch st := a.wake.State; st {
		case "deferred":
			add("deferred", RoleAttention, 4, 2)
		case "unobserved":
			add("unobserved", RoleAttention, 5, 2)
		case "prompting":
			add("waking", RoleDispatch, 6, 2)
		case "prompted":
			add("woken", RoleDispatch, 7, 2)
		case "", "clear":
		default:
			add("wake:"+Preview(st), RoleMuted, 11, 0)
		}
	}
	if pilot {
		add("pilot", RoleAccent, 8, 0)
	}
	if a.armed {
		add("armed", RoleMuted, 9, 0)
	}
	if a.stale > 0 {
		add(fmt.Sprintf("stale %d", a.stale), RoleMuted, 10, 0)
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Prio < fs[j].Prio })
	return fs
}

// stateLook is the declared state's glyph, word, colour and sort group (spec
// §3.3.1). A report-only or armed-only agent declared nothing: "—", group 6.
// Bus facts that stop an agent whatever it declared come first: its open
// questions (waiting), then, for an agent that declared working or idle, its
// provider at its limit (quota) or the project pause (paused). The declared
// state stays in the detail; blocked, waiting and done are never covered.
func stateLook(a *agentState) (glyph, word string, role Role, group int) {
	state := a.state
	if answered(a) && !promptWaiting(a) {
		return GlyphIdle, "answered", RoleMuted, 4
	}
	if a.openAsk > 0 || promptWaiting(a) { // its open questions, or its client's, make it waiting
		state = "waiting"
	}
	if state == "working" || state == "idle" || state == "" {
		switch {
		case a.held:
			return GlyphIdle, "quota", RoleAttention, 4
		case a.paused && a.registered:
			return GlyphIdle, "paused", RoleAttention, 4
		}
	}
	switch state {
	case "":
		return GlyphNone, GlyphDash, RoleMuted, 6
	case "waiting": // a person's decision: above everything (group 0)
		return GlyphHuman, "waiting", RoleAttention, 0
	case "blocked":
		return GlyphBlocked, "blocked", RoleError, 1
	case "working":
		return GlyphWorking, "working", RoleOK, 3
	case "idle":
		return GlyphIdle, "idle", RoleMuted, 4
	case "done":
		return GlyphDone, "done", RoleDone, 5
	}
	return GlyphIdle, Preview(a.state), RoleMuted, 4
}

// queueMeasured reports a queue value that may move an agent between groups:
// measured, and its latest read did not fail.
func queueMeasured(a *agentState) bool { return a.queueKnown && !a.queueFailed }

// agentGroup is the comparator group (spec §3.3.4): 1 escalated/gated/blocked,
// 2 a wake in progress or a measured queue, 3 working, 4 idle and other
// states, 5 done, 6 no state declared. Silence never changes it.
func agentGroup(a *agentState, pilot bool) int {
	_, _, _, g := stateLook(a)
	best := g
	for _, f := range agentFlags(a, pilot) {
		if f.Group != 0 && f.Group < best {
			best = f.Group
		}
	}
	if queueMeasured(a) && a.waiting > 0 && best > 2 {
		best = 2
	}
	return best
}

// sortedAgentNames is the Agents panel order: attention first, names inside.
func sortedAgentNames(m map[string]*agentState, pilot string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		gi, gj := agentGroup(m[names[i]], names[i] == pilot), agentGroup(m[names[j]], names[j] == pilot)
		if gi != gj {
			return gi < gj
		}
		return names[i] < names[j]
	})
	return names
}

// queueCell: "N msg" (wide) or "N", "·" for a measured zero, "—" before the
// first measurement, "N?" muted when the latest read failed.
func queueCell(a *agentState, tier Tier) Seg {
	if !a.queueKnown {
		return Seg{Text: GlyphDash, Role: RoleMuted}
	}
	if a.queueFailed {
		return Seg{Text: fmt.Sprintf("%d?", a.waiting), Role: RoleMuted}
	}
	if a.waiting == 0 {
		return Seg{Text: GlyphNone, Role: RoleDim}
	}
	if tier == TierWide {
		return Seg{Text: fmt.Sprintf("%d msg", a.waiting), Role: RoleAttention}
	}
	return Seg{Text: strconv.FormatInt(a.waiting, 10), Role: RoleAttention}
}

const updatedBarCells = 10

// dormantAfter: an agent silent this long, not listening, not the pilot and
// owed nothing is left out of the Agents panel and of @all (a shows it). Its
// declared state is never rewritten: it is hidden, not called offline.
const dormantAfter = 24 * time.Hour

func dormant(a *agentState, isPilot bool, now time.Time) bool {
	return !isPilot && !a.armed && a.waiting == 0 && !waitsForPerson(a) && !a.lastSeen.IsZero() && now.Sub(a.lastSeen) > dormantAfter
}

// visibleAgents is the Agents panel's roster: every agent with all, else the
// ones that are not dormant. It also returns how many were left out.
func visibleAgents(m map[string]*agentState, pilot string, all bool, now time.Time) ([]string, int) {
	names := sortedAgentNames(m, pilot)
	if all {
		return names, 0
	}
	out := names[:0:0]
	for _, n := range names {
		if !dormant(m[n], n == pilot, now) {
			out = append(out, n)
		}
	}
	return out, len(names) - len(out)
}

// updatedCell is the declaration's freshness: in wide a bar that empties over
// staleAfter, then the age; elsewhere the age alone.
func updatedCell(a *agentState, tier Tier, now time.Time) []Seg {
	if a.stateAt.IsZero() {
		if tier != TierWide {
			return []Seg{{Text: GlyphDash, Role: RoleMuted}}
		}
		return []Seg{{Text: strings.Repeat(GlyphNone, updatedBarCells), Role: RoleDim}, {Text: " " + GlyphDash, Role: RoleMuted}}
	}
	at := a.stateAt
	if answered(a) {
		at = a.answeredAt
	}
	age := max(0, now.Sub(at))
	ageSeg := Seg{Text: humanAge(age), Role: RoleMuted}
	if tier != TierWide {
		return []Seg{ageSeg}
	}
	full := updatedBarCells - int(int64(age)*updatedBarCells/int64(staleAfter))
	full = clamp(full, 0, updatedBarCells)
	ageSeg.Text = " " + ageSeg.Text
	return []Seg{{Text: strings.Repeat(GlyphDotFull, full), Role: RoleAccent},
		{Text: strings.Repeat(GlyphDotEmpty, updatedBarCells-full), Role: RoleDim}, ageSeg}
}

// agentCols are the column widths per tier (trailing gap included); the
// message takes the rest.
type agentCols struct{ glyph, name, state, updated, queue, ctx, flags int }

func agentColsFor(tier Tier) agentCols {
	switch tier {
	case TierWide:
		return agentCols{glyph: 2, name: 10, state: 10, updated: 20, queue: 9, ctx: 8, flags: 20}
	case TierMedium:
		return agentCols{glyph: 2, name: 10, state: 9, updated: 5, queue: 4, flags: 12}
	}
	return agentCols{glyph: 2, name: 10, state: 9, updated: 5, queue: 4} // narrow: the flag joins the message
}

// cell fits a multi-segment value to a column of n cells with one trailing space.
func cell(segs []Seg, n int) Line {
	if n <= 0 {
		return nil
	}
	return append(FitLine(segs, n-1), Seg{Text: " "})
}

// agentHeader is the column header row, wide only.
func agentHeader(tier Tier, width int) Line {
	if tier != TierWide {
		return nil
	}
	c := agentColsFor(tier)
	l := Line{col("", c.glyph, RoleDim), col("agent", c.name, RoleDim), col("state", c.state, RoleDim),
		col("updated", c.updated, RoleDim), col("queue", c.queue, RoleDim), col("ctx", c.ctx, RoleDim),
		col("flags", c.flags, RoleDim), {Text: "message", Role: RoleDim}}
	return FitLine(l, width)
}

// agentRow is one agent in exactly width cells (the list adds its gutter).
func agentRow(name string, a *agentState, pilot bool, tier Tier, width int, now time.Time) Line {
	c := agentColsFor(tier)
	glyph, word, role, _ := stateLook(a)
	w := col(word, c.state, role)
	if waitsForPerson(a) {
		w.Fade = fadeLevel(now) // only the word fades; the glyph and the header stay still
	}
	l := Line{col(glyph, c.glyph, role), col(Preview(name), c.name, RoleText), w}
	l = append(l, cell(updatedCell(a, tier, now), c.updated)...)
	l = append(l, cell([]Seg{queueCell(a, tier)}, c.queue)...)
	if c.ctx > 0 {
		ctx := Seg{Text: GlyphDash, Role: RoleMuted}
		if n, ok := parseCtx(a.ctxRaw); ok {
			ctx = Seg{Text: formatTokens(n), Role: RoleText}
		}
		l = append(l, cell([]Seg{ctx}, c.ctx)...)
	}
	keep := 1
	if tier == TierWide {
		keep = 2
	}
	var flags []Seg
	for i, f := range agentFlags(a, pilot) {
		if i == keep {
			break
		}
		if i > 0 {
			flags = append(flags, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
		}
		flags = append(flags, Seg{Text: f.Word, Role: f.Role})
	}
	if c.flags > 0 {
		l = append(l, cell(flags, c.flags)...)
	} else if len(flags) > 0 {
		l = append(l, append(flags, Seg{Text: " "})...)
	}
	l = append(l, Seg{Text: Preview(a.message), Role: RoleText})
	return FitLine(l, width)
}

// fadePeriod is one slow breath of the waiting word: faint, full, faint.
const fadePeriod = 3 * time.Second

// fadeFrame is how often the fade redraws: ten steps a second is smooth enough.
const fadeFrame = 100 * time.Millisecond

// fadeLevel is the waiting word's intensity at now, 20 to 100 percent, eased
// in and out (a raised cosine, like CSS ease-in-out between 0.2 and 1).
func fadeLevel(now time.Time) int {
	phase := float64(now.UnixMilli()%fadePeriod.Milliseconds()) / float64(fadePeriod.Milliseconds())
	return 20 + int(math.Round(80*(1-math.Cos(2*math.Pi*phase))/2))
}

// asksFor lists the agents waiting for a person's decision, sorted: the
// header badge and the bell count them.
func asksFor(m map[string]*agentState) []string {
	var out []string
	for n, a := range m {
		if waitsForPerson(a) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// waitingQuestions is the header's "N waiting for you": the open questions of
// every agent's open ask, and one for an agent waiting with no ask (a plain
// waiting declaration, as before v0.14).
func waitingQuestions(m map[string]*agentState) int {
	n := 0
	for _, a := range m {
		switch {
		case a.openAsk > 0:
			n += a.openAsk
		case a.state == "waiting" && !answered(a), promptWaiting(a):
			n++
		}
	}
	return n
}

// openQuestionCount is the open questions across open asks.
func openQuestionCount(asks []bus.Ask) int {
	n := 0
	for _, a := range asks {
		n += a.Open()
	}
	return n
}
