package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// Layout A (idle view spec §4, the person's choice): three frames, you (the
// big count of what needs the person), team (the agents not idle, then the
// idle count) and next (the plan's current and next task), over one strip:
// tokens, the plan's progress, the clock. Everything is drawn from the
// snapshot the dashboard already polls: no new read.

// idleDigits is the big count's font: five rows, five cells per digit.
var idleDigits = [10][5]string{
	{"█████", "█   █", "█   █", "█   █", "█████"},
	{"  █  ", " ██  ", "  █  ", "  █  ", " ███ "},
	{"█████", "    █", "█████", "█    ", "█████"},
	{"█████", "    █", " ████", "    █", "█████"},
	{"█   █", "█   █", "█████", "    █", "    █"},
	{"█████", "█    ", "█████", "    █", "█████"},
	{"█████", "█    ", "█████", "█   █", "█████"},
	{"█████", "    █", "   █ ", "  █  ", "  █  "},
	{"█████", "█   █", "█████", "█   █", "█████"},
	{"█████", "█   █", "█████", "    █", "█████"},
}

// bigCount is n in idleDigits, at most 999, digits two cells apart.
func bigCount(n int) []string {
	s := strconv.Itoa(min(max(n, 0), 999))
	rows := make([]string, 5)
	for i, r := range s {
		for row := range rows {
			if i > 0 {
				rows[row] += "  "
			}
			rows[row] += idleDigits[r-'0'][row]
		}
	}
	return rows
}

// idleData is what the frames show, computed from the model.
type idleData struct {
	need    int      // decide + merge: the header chips' count
	parts   Line     // "♙ 2 decide   ◆ 1 merge"
	team    []Line   // the agents not idle, attention first
	idle    int      // the agents counted as idle (idle, answered, done, none)
	allIdle bool     // no agent in any other state: the row above the strip breathes (idle-breathing)
	counts  []string // "3 waiting", "1 working", "4 idle"… (narrow), the idle one at idleAt
	idleAt  int
	next    []Line     // the plan's current task, then "then: <next>"
	strip   stripParts // the bottom row
}

type stripParts struct {
	tokens                     []string // one per provider, the most used first (idleTokenItems)
	planName, planCount, clock string
	project                    string // before the clock (idle-view-project-name), dropped first when the strip is tight
}

func (m Model) idleData() idleData {
	q := m.work.strip(0)
	d := idleData{need: q.decide + q.merge}
	if q.decide > 0 {
		d.parts = append(d.parts, Seg{Text: GlyphHuman + " " + strconv.Itoa(q.decide) + " decide", Role: RoleMuted})
	}
	if q.merge > 0 {
		if len(d.parts) > 0 {
			d.parts = append(d.parts, Seg{Text: "   ", Role: RoleMuted})
		}
		d.parts = append(d.parts, Seg{Text: GlyphGate + " " + strconv.Itoa(q.merge) + " merge", Role: RoleMuted})
	}
	names, _ := visibleAgents(m.snap.agents, m.snap.pilot, false, m.now)
	byWord, groupOf := map[string]int{}, map[string]int{}
	var order []string
	for _, n := range names {
		a := m.snap.agents[n]
		glyph, word, role, group := stateLook(a)
		if group > 3 && role != RoleAttention {
			d.idle++
			if bg := backgroundText(a, m.now); a.bg != nil { // shown with its work, still counted idle (progress-line §4.3); a failed read adds no row
				d.team = append(d.team, Line{{Text: glyph, Role: role}, {Text: " " + padRight(n, 10) + " " + padRight(word, 8), Role: RoleMuted}, {Text: " " + bg, Role: RoleDim}})
			}
			continue
		}
		if byWord[word] == 0 {
			order, groupOf[word] = append(order, word), group
		}
		byWord[word]++
		row := Line{{Text: glyph, Role: role}, {Text: " " + padRight(n, 10) + " " + padRight(word, 8), Role: RoleMuted}}
		if msg := Preview(a.message); msg != "" {
			row = append(row, Seg{Text: " " + msg, Role: RoleDim})
		}
		d.team = append(d.team, row)
	}
	// The counts in the panel's order, the idle count after working and
	// before the other resting words (quota, paused), so it stays in view.
	sort.SliceStable(order, func(i, j int) bool { return groupOf[order[i]] < groupOf[order[j]] })
	var counts []string
	idleAt := len(order)
	for i, w := range order {
		if groupOf[w] > 3 && idleAt == len(order) {
			idleAt = i
		}
	}
	for _, w := range order {
		counts = append(counts, fmt.Sprintf("%d %s", byWord[w], w))
	}
	d.counts, d.idleAt = slicesInsert(counts, idleAt, fmt.Sprintf("%d idle", d.idle)), idleAt
	d.allIdle = len(order) == 0
	d.next = idleNext(m.snap.planState)
	present := map[string]bool{}
	for _, name := range m.names(false) {
		if a := m.snap.agents[name]; a != nil && a.provider != "" {
			present[a.provider] = true
		}
	}
	d.strip = stripParts{tokens: idleTokenItems(m.snap.budgets, m.snap.holds, present, m.now), clock: m.now.Local().Format("15:04"),
		project: Preview(m.cfg.project)}
	if p := m.snap.planState; p.loaded {
		d.strip.planName = Preview(p.file.Plan)
		d.strip.planCount = planProgress(p, 0, TierNarrow).text()
	}
	return d
}

// idleNext is the plan's task in hand (in progress or in review, else the
// first waiting for the person or blocked), then the first upcoming one.
func idleNext(p planState) []Line {
	if !p.loaded {
		return []Line{{{Text: "no plan loaded", Role: RoleDim}}}
	}
	task := func(r plan.Row, lead string) Line {
		l := Line{{Text: lead + Preview(r.Task.Title), Role: RoleMuted}}
		if r.Task.Owner != "" {
			l = append(l, Seg{Text: " " + GlyphNone + " " + r.Task.Owner, Role: RoleDim})
		}
		return l
	}
	var out []Line
	now, next, _ := planInHand(p)
	if now != nil {
		out = append(out, task(*now, ""))
	}
	if next != nil {
		lead := ""
		if len(out) > 0 {
			lead = "then: "
		}
		out = append(out, task(*next, lead))
	}
	if len(out) == 0 {
		return []Line{{{Text: "every task is done", Role: RoleDim}}}
	}
	return out
}

func indexOfState(ss []plan.State, s plan.State) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func padRight(s string, n int) string {
	if w := Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// idleDrift is the block's place after k minutes (§6): a diagonal walk from
// the middle, shifted by the session's seed, that turns back at each edge of
// a free area of fx by fy cells; one cell per axis per minute at most.
func idleDrift(k, sx, sy, fx, fy int) (int, int) {
	return bounce(fx/2+sx+k, fx), bounce(fy/2+sy+k, fy)
}

// bounce folds n into 0..r, back and forth.
func bounce(n, r int) int {
	if r <= 0 {
		return 0
	}
	p := n % (2 * r)
	if p < 0 {
		p += 2 * r
	}
	if p > r {
		return 2*r - p
	}
	return p
}

// idleBox is one frame: its title and inner lines.
type idleBox struct {
	title  string
	lines  []Line
	w      int  // inner width it wants
	centre bool // its lines in the middle of the frame, both ways
}

func (b idleBox) h() int { return len(b.lines) + 2 }

// draw paints the frame at x, y, w cells wide and h rows tall (h >= b.h()).
func (b idleBox) draw(g *grid, x, y, w, h int) {
	drawFrame(g, x, y, w, h, Frame{Title: Line{{Text: b.title, Role: RoleMuted}}, Hue: RoleDim})
	top := 0
	if b.centre {
		top = (h - b.h()) / 2
	}
	for i, l := range b.lines {
		if b.centre {
			l = centred(l, w-4)
		}
		drawLine(g, x+2, y+1+top+i, w-4, l, false)
	}
}

// centred pads l to sit in the middle of w cells.
func centred(l Line, w int) Line {
	if pad := (w - l.Width()) / 2; pad > 0 {
		return append(Line{{Text: strings.Repeat(" ", pad), Role: RoleText}}, l...)
	}
	return l
}

// youBox is the big count, or "nothing needs you".
func youBox(d idleData) idleBox {
	b := idleBox{title: "you", centre: true}
	var body []Line
	if d.need == 0 {
		body = []Line{{{Text: GlyphIdle, Role: RoleMuted}}, nil, {{Text: "nothing needs you", Role: RoleMuted}}}
	} else {
		for _, r := range bigCount(d.need) {
			body = append(body, Line{{Text: r, Role: RoleAccent}})
		}
		body = append(body, nil, d.parts)
	}
	for _, l := range body {
		b.w = max(b.w, l.Width())
	}
	b.w, b.lines = max(b.w+4, 30), body
	return b
}

// teamBox lists at most rows agents; past them, "+N more".
func teamBox(d idleData, rows int) idleBox {
	b := idleBox{title: "team"}
	team := d.team
	more := 0
	if rows < len(team)+1 { // the idle line takes one
		keep := max(rows-1, 0)
		more, team = len(team)-keep, team[:keep]
	}
	b.lines = append(b.lines, team...)
	tail := Line{{Text: GlyphIdle + " " + strconv.Itoa(d.idle) + " idle", Role: RoleDim}}
	if more > 0 {
		tail = append(tail, Seg{Text: "  +" + strconv.Itoa(more) + " more", Role: RoleDim})
	}
	b.lines = append(b.lines, tail)
	for _, l := range b.lines {
		b.w = max(b.w, l.Width())
	}
	b.w = min(max(b.w+4, 40), 64)
	return b
}

func nextBox(d idleData) idleBox {
	b := idleBox{title: "next", lines: d.next}
	for _, l := range b.lines {
		b.w = max(b.w, l.Width())
	}
	b.w = min(max(b.w+4, 40), 64)
	return b
}

// idleTokenItems is the strip's quota, one item per provider with a known
// reading or a present agent (idle-view-providers): its tightest fresh window
// ("codex 23% 7d"), else its tightest window with the reading's age; "held"
// while the dispatcher holds it; "no data (why)" for a present agent's
// provider without a reading. An unknown reading no present agent uses and an
// unlimited provider are left out. Fresh readings first, the most used
// first; then stale, held, no data. Nothing the bus does not hold is shown.
func idleTokenItems(budgets map[string]bus.BudgetSnapshot, holds map[string]int64, present map[string]bool, now time.Time) []string {
	type item struct {
		text  string
		rank  int // 0 fresh, 1 stale, 2 held, 3 no data
		pct   float64
		order string
	}
	provs := map[string]bool{}
	for p := range budgets {
		provs[p] = true
	}
	for p := range present {
		provs[p] = true
	}
	var items []item
	for p := range provs {
		b, has := budgets[p]
		name := Preview(providerLabel(p))
		if until, isHeld := held(holds, p, now); isHeld {
			items = append(items, item{text: name + " held " + compactDur(until.Sub(now)), rank: 2, order: name})
			continue
		}
		switch {
		case has && b.Unlimited:
			continue
		case has && !b.Unknown && len(b.Windows) > 0:
			if w, ok := tightestWindow(freshWindows(b, now), now); ok {
				items = append(items, item{text: fmt.Sprintf("%s %.0f%% %s", name, w.UsedPct, windowLabel(w)), pct: w.UsedPct, order: name})
				continue
			}
			if w, ok := tightestWindow(b.Windows, now); ok {
				items = append(items, item{text: fmt.Sprintf("%s %.0f%% %s (%s old)", name, w.UsedPct, windowLabel(w), windowAge(w, b, now)), rank: 1, pct: w.UsedPct, order: name})
				continue
			}
		}
		if !present[p] {
			continue // an unknown or expired reading nobody here draws on
		}
		why := "no reading published"
		if has {
			why = "unknown"
			if b.Source != "" {
				why = Preview(b.Source) + ": unknown"
			}
		}
		items = append(items, item{text: name + ": no data (" + why + ")", rank: 3, order: name})
	}
	sort.Slice(items, func(i, j int) bool {
		a, c := items[i], items[j]
		if a.rank != c.rank {
			return a.rank < c.rank
		}
		if a.pct != c.pct {
			return a.pct > c.pct
		}
		return a.order < c.order
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.text
	}
	return out
}

// idleStripLine is the bottom row in w cells: tokens · plan, the project and
// the clock at the right. Too narrow, the project goes first, then the clock,
// then the plan's name, then every provider but the first ("claude 65% 7d +2").
func idleStripLine(s stripParts, w int) Line {
	build := func(project, clock, name, all bool) Line {
		var parts []string
		switch {
		case len(s.tokens) == 0:
		case all || len(s.tokens) == 1:
			parts = append(parts, strings.Join(s.tokens, " "+GlyphNone+" "))
		default:
			parts = append(parts, fmt.Sprintf("%s +%d", s.tokens[0], len(s.tokens)-1))
		}
		if s.planCount != "" {
			p := s.planCount
			if name && s.planName != "" {
				p = "plan " + s.planName + " " + p
			}
			parts = append(parts, p)
		}
		l := Line{{Text: strings.Join(parts, " "+GlyphNone+" "), Role: RoleDim}}
		if clock {
			right := Line{{Text: s.clock, Role: RoleMuted}}
			if project && s.project != "" {
				right = Line{{Text: s.project + " " + GlyphNone + " ", Role: RoleDim}, {Text: s.clock, Role: RoleMuted}}
			}
			if gap := w - l.Width() - right.Width(); gap >= 2 {
				l = append(append(l, Seg{Text: strings.Repeat(" ", gap), Role: RoleText}), right...)
			} else {
				return nil
			}
		}
		return l
	}
	for _, try := range [][4]bool{{true, true, true, true}, {false, true, true, true}, {false, false, true, true}, {false, false, false, true}, {false, false, false, false}} {
		if l := build(try[0], try[1], try[2], try[3]); l != nil && l.Width() <= w {
			return l
		}
	}
	return FitLine(build(false, false, false, false), w)
}

// idleThreadLen is the thread's length in cells.
const idleThreadLen = 3

// idleThread is the row above the strip (§4, the person's animation A): a
// dotted line w cells wide with a thread sliding one cell right per step; it
// enters at the left edge and leaves at the right, so over a pass (w plus its
// length) every cell carries it. Never a blink: only its place changes.
func idleThread(w, step int) Line {
	if w <= 0 {
		return nil
	}
	pass := w + idleThreadLen
	start := (step%pass+pass)%pass - idleThreadLen // -len .. w-1
	a, b := max(start, 0), min(start+idleThreadLen, w)
	var l Line
	if a > 0 {
		l = append(l, Seg{Text: strings.Repeat(GlyphNone, min(a, w)), Role: RoleDim})
	}
	if b > a {
		l = append(l, Seg{Text: strings.Repeat(GlyphThinBar, b-a), Role: RoleAccent})
	}
	if rest := w - max(a, b); rest > 0 {
		l = append(l, Seg{Text: strings.Repeat(GlyphNone, rest), Role: RoleDim})
	}
	return l
}

// idleBreathCycle is the breath's length in steps (seconds) without true
// colour: dim, muted, bright, muted.
const idleBreathCycle = 4

// idleSmoothPeriod is the breath's cycle with true colour: the agents' ramp
// (rampLevel) from 20 to 100 percent and back (idle-dot-smooth).
const idleSmoothPeriod = 6 * time.Second

// idleBreathFrame is how often the smooth breath changes shade: at 1 s (the
// person's option 1, 2026-11-10) it draws on the poll, 20, 40, 80, 100, 80,
// 40 percent, and no frame is added (idle view §6). It is the one switch to
// the other options: 100 ms is the agents' own frame (option 2), 250 ms the
// middle (option 3); under a second, scheduleFade gives the idle view frames
// at that pace while the breath shows.
var idleBreathFrame = time.Second

// idleSmooth: the breath ramps where a shade can show, the agents' rule:
// true colour and the fade setting on.
func (m Model) idleSmooth() bool { return m.cfg.settings.Fade && th.Animated() }

// idleBreath is the row above the strip while every agent is idle (the
// person's choice, "Respiration"): the dotted row w cells wide with one cell
// that brightens and dims over idleBreathCycle steps, a cell further right
// each cycle so every cell carries it in turn (burn-in), never more than one
// lit cell (OLED). Without colour it breathes in two glyphs, the row's dim
// dot and the bright one.
func idleBreath(w int, elapsed time.Duration, smooth bool) Line {
	if w <= 0 {
		return nil
	}
	elapsed = max(elapsed, 0)
	if smooth { // the agents' ramp, one shade a frame
		e := elapsed.Truncate(idleBreathFrame)
		x := int(e/idleSmoothPeriod) % w
		return breathRow(w, x, Seg{Text: GlyphSet, Role: RoleAccent, Fade: rampLevel(float64(e%idleSmoothPeriod) / float64(idleSmoothPeriod))})
	}
	step := int(elapsed / time.Second)
	x, phase := (step/idleBreathCycle)%w, step%idleBreathCycle
	glyph, role := GlyphSet, [idleBreathCycle]Role{RoleDim, RoleMuted, RoleAccent, RoleMuted}[phase]
	if th.Palette() == PaletteNone { // no shade to breathe in: the two dots
		glyph, role = GlyphNone, RoleDim
		if phase == 1 || phase == 2 {
			glyph = GlyphSet
		}
	}
	return breathRow(w, x, Seg{Text: glyph, Role: role})
}

// idleRow is the row above the strip, w cells: the thread while something
// works (idle-animation), the breath while the whole team rests, smooth where
// a shade can show (idle-dot-smooth). Its step is the whole seconds since the
// view came: the 1 s poll's pace.
func (m Model) idleRow(d idleData, w int) Line {
	elapsed := max(0, m.now.Sub(m.ui.idleSince))
	if d.allIdle {
		return idleBreath(w, elapsed, m.idleSmooth())
	}
	return idleThread(w, int(elapsed/time.Second))
}

// idleFrames: the idle view takes frames between polls, only while the
// smooth breath shows and its frame is under a second (options 2 and 3).
func (m *Model) idleFrames() bool {
	return idleBreathFrame < time.Second && m.ui.idle && m.idleSmooth() && m.idleData().allIdle
}

// breathRow is the dotted row w cells wide with lit at x.
func breathRow(w, x int, lit Seg) Line {
	var l Line
	if x > 0 {
		l = append(l, Seg{Text: strings.Repeat(GlyphNone, x), Role: RoleDim})
	}
	l = append(l, lit)
	if rest := w - x - 1; rest > 0 {
		l = append(l, Seg{Text: strings.Repeat(GlyphNone, rest), Role: RoleDim})
	}
	return l
}

// idleGrid paints the idle view: the frames side by side when they fit,
// stacked when they do not, one line each on a narrow screen; the block
// drifts a cell a minute, the strip up to 4 cells sideways, and the row
// above the strip carries the thread (a cell a second) while an agent works,
// the breath while every one is idle.
func (m Model) idleGrid() *grid {
	g := newGrid(m.width, m.height)
	d := m.idleData()
	W, H := m.width, m.height-2 // the thread and the strip have the last two rows
	k := int(max(0, m.now.Sub(m.ui.idleSince)) / time.Minute)
	seed := int(m.sig.started.Unix() % 97)
	place := func(bw, bh int) (int, int) {
		x, y := idleDrift(k, seed%7, seed%5, W-bw, H-bh)
		return x, y
	}
	shift := bounce(k+seed, min(4, max(W-8, 0)))
	drawLine(g, shift, m.height-1, W-4, idleStripLine(d.strip, W-4), false)
	drawLine(g, 0, m.height-2, W, m.idleRow(d, W), false)

	nb := nextBox(d)
	// Side by side: you left, team over next right.
	you := youBox(d)
	{
		teamRows := H - nb.h() - 2
		tb := teamBox(d, teamRows)
		rw := max(tb.w, nb.w)
		bw, bh := you.w+2+2+rw+2, max(you.h()+4, tb.h()+nb.h()) // the count with room above and below
		if bw <= W && bh <= H && teamRows >= 2 {
			x, y := place(bw, bh)
			you.draw(g, x, y, you.w+2, bh) // as tall as the right column
			tb.draw(g, x+you.w+4, y, rw+2, bh-nb.h())
			nb.draw(g, x+you.w+4, y+bh-nb.h(), rw+2, nb.h())
			return g
		}
	}
	// Stacked: you, team, next, one width.
	{
		teamRows := H - you.h() - nb.h() - 2
		tb := teamBox(d, teamRows)
		bw := min(max(you.w, tb.w, nb.w)+2, W) // a long team row is cut, never the frame
		bh := you.h() + tb.h() + nb.h()
		if you.w+2 <= W && bh <= H && teamRows >= 2 {
			x, y := place(bw, bh)
			you.draw(g, x, y, bw, you.h())
			tb.draw(g, x, y+you.h(), bw, tb.h())
			nb.draw(g, x, y+you.h()+tb.h(), bw, nb.h())
			return g
		}
	}
	// One line per frame.
	need := Line{{Text: "nothing needs you", Role: RoleMuted}}
	if d.need > 0 {
		need = append(Line{{Text: strconv.Itoa(d.need), Role: RoleAccent, Bold: true}, {Text: " need you", Role: RoleMuted}}, d.parts...)
		need[1].Text += "  "
	}
	next := Line{{Text: "next: ", Role: RoleDim}}
	if len(d.next) > 0 {
		next = append(next, d.next[0]...)
	}
	lines := []Line{need, {{Text: countsFit(d.counts, d.idleAt, W), Role: RoleMuted}}, next}
	bw := 0
	for i := range lines {
		lines[i] = FitLine(lines[i], W)
		bw = max(bw, lines[i].Width())
	}
	x, y := place(bw, len(lines))
	for i, l := range lines {
		drawLine(g, x, y+i, W-x, l, false)
	}
	return g
}

func slicesInsert(s []string, i int, v string) []string {
	return append(s[:i:i], append([]string{v}, s[i:]...)...)
}

// countsFit joins the counts in w cells: too wide, the last ones go first,
// never the idle count.
func countsFit(counts []string, idleAt, w int) string {
	keep := append([]string(nil), counts...)
	for {
		line := strings.Join(keep, " "+GlyphNone+" ")
		if Width(line) <= w || len(keep) <= 1 {
			return line
		}
		drop := len(keep) - 1
		if drop == idleAt {
			drop--
		}
		if drop < 0 {
			return line
		}
		if drop < idleAt {
			idleAt--
		}
		keep = append(keep[:drop], keep[drop+1:]...)
	}
}
