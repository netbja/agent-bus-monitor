package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
	need   int        // decide + merge: the header chips' count
	parts  Line       // "♙ 2 decide   ◆ 1 merge"
	team   []Line     // the agents not idle, attention first
	idle   int        // the agents counted as idle (idle, answered, done, none)
	counts []string   // "3 waiting", "1 working", "4 idle"… (narrow), the idle one at idleAt
	idleAt int
	next   []Line     // the plan's current task, then "then: <next>"
	strip  stripParts // the bottom row
}

type stripParts struct{ tokens, planName, planCount, clock string }

func (m Model) idleData() idleData {
	q := stripOf(m.forYou)
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
	d.next = idleNext(m.snap.planState)
	sep := " " + GlyphNone + " "
	d.strip = stripParts{tokens: strings.TrimPrefix(strings.TrimPrefix(tokensTitle(m.snap.budgets, m.snap.holds, m.now), "Tokens"+sep+"today"), sep),
		clock: m.now.Local().Format("15:04")}
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
	cur := -1
	for _, states := range [][]plan.State{{plan.InProgress, plan.InReview}, {plan.Waiting, plan.Answered, plan.Blocked}} {
		for i, r := range p.rows {
			if cur < 0 && indexOfState(states, r.State) >= 0 {
				cur = i
			}
		}
	}
	if cur >= 0 {
		out = append(out, task(p.rows[cur], ""))
	}
	for i, r := range p.rows {
		if i != cur && r.State == plan.Upcoming {
			lead := ""
			if len(out) > 0 {
				lead = "then: "
			}
			out = append(out, task(r, lead))
			break
		}
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

// idleStripLine is the bottom row in w cells: tokens · plan, the clock at the
// right. Too narrow, the clock goes first, then the plan's name.
func idleStripLine(s stripParts, w int) Line {
	build := func(clock, name bool) Line {
		var parts []string
		if s.tokens != "" {
			parts = append(parts, s.tokens)
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
			if gap := w - l.Width() - Width(s.clock); gap >= 2 {
				l = append(l, Seg{Text: strings.Repeat(" ", gap), Role: RoleText}, Seg{Text: s.clock, Role: RoleMuted})
			} else {
				return nil
			}
		}
		return l
	}
	for _, try := range [][2]bool{{true, true}, {false, true}, {false, false}} {
		if l := build(try[0], try[1]); l != nil && l.Width() <= w {
			return l
		}
	}
	return FitLine(build(false, false), w)
}

// idleGrid paints the idle view: the frames side by side when they fit,
// stacked when they do not, one line each on a narrow screen; the block
// drifts a cell a minute, the strip up to 4 cells sideways.
func (m Model) idleGrid() *grid {
	g := newGrid(m.width, m.height)
	d := m.idleData()
	W, H := m.width, m.height-1 // the strip has the last row
	k := int(max(0, m.now.Sub(m.ui.idleSince)) / time.Minute)
	seed := int(m.sig.started.Unix() % 97)
	place := func(bw, bh int) (int, int) {
		x, y := idleDrift(k, seed%7, seed%5, W-bw, H-bh)
		return x, y
	}
	shift := bounce(k+seed, min(4, max(W-8, 0)))
	drawLine(g, shift, m.height-1, W-4, idleStripLine(d.strip, W-4), false)

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
