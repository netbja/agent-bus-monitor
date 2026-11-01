package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// eventLook is how one bus event reads in the Activity table (spec §3.4).
// Text is the raw message before Preview: the row sanitises it, the copy and
// the filter never see this value.
type eventLook struct {
	From, To, Type string
	Role           Role
	Text           string
}

// lookEvent maps every Kind/Type to from/to/type. "request" is a label derived
// from a directive that carries request metadata (Task); the wire type stays
// directive. An unknown cmd type or kind shows its raw name, muted.
func lookEvent(e bus.Event) eventLook {
	l := lookEvent1(e)
	if e.Human && l.From != "" { // a person wrote it (busmon): their glyph and name
		l.From = GlyphHuman + " " + displayName(l.From)
	}
	return l
}

func lookEvent1(e bus.Event) eventLook {
	switch e.Kind {
	case "status":
		text := e.State
		if e.Message != "" {
			text += " " + GlyphNone + " " + e.Message
		}
		return eventLook{From: e.Agent, Type: "status", Role: RoleMuted, Text: text}
	case "report":
		look := eventLook{From: e.Agent, Type: "report", Role: RoleText, Text: e.Message + reportMarker(e.Full)}
		if e.RKind == bus.ReportAuto {
			look.Type, look.Role = "auto", RoleMuted
		}
		return look
	case "notify":
		return eventLook{From: e.From, Type: "notify", Role: RoleAttention, Text: e.Message}
	case "cmd":
		look := eventLook{From: e.From, To: e.Target, Type: e.Type, Role: RoleMuted, Text: e.Message}
		switch e.Type {
		case bus.CmdDirective:
			look.Role = RoleAccent
			if e.Task != "" {
				look.Type = "request"
			}
		case bus.CmdReply:
			look.Role = RoleAccent
		case bus.CmdChallenge, bus.CmdVerdict:
			look.Role = RoleAttention
		case bus.CmdEscalation:
			look.Role = RoleDispatch
		case bus.CmdComplete:
			look.Role = RoleOK
		case bus.CmdBlocked:
			look.Role = RoleError
		case bus.CmdAsk: // questions for people: read from the hash while it holds the ask
			look.Role, look.To, look.Text = RoleAttention, "", "asks "+GlyphNone+" "+e.Message
			if a, ok := currentAsks[e.ID]; ok {
				look.Text = fmt.Sprintf("asks %s %s (%d question(s), %d open)", GlyphNone, a.Title, len(a.Questions), a.Open())
			}
		}
		return look
	}
	return eventLook{Type: e.Kind, Role: RoleMuted, Text: e.Message}
}

// activityCols are the fixed column widths per tier (gap included); the
// message takes the rest. Narrow has no "to" column: the target is folded into
// the message.
type activityCols struct{ time, from, to, typ int }

func colsFor(tier Tier) activityCols {
	switch tier {
	case TierWide:
		return activityCols{time: 7, from: 10, to: 11, typ: 11}
	case TierMedium:
		return activityCols{time: 6, from: 9, to: 9, typ: 10}
	}
	return activityCols{time: 6, from: 9, typ: 10}
}

// col fits s to a column of n cells, keeping one trailing space.
func col(s string, n int, r Role) Seg {
	if n <= 0 {
		return Seg{Role: r}
	}
	return Seg{Text: Fit(s, n-1) + " ", Role: r}
}

// activityHeader is the column header row (wide and medium), exactly width cells.
func activityHeader(tier Tier, width int) Line {
	if tier == TierNarrow {
		return nil
	}
	c := colsFor(tier)
	l := Line{{Text: "  ", Role: RoleDim}, col("time", c.time, RoleDim), col("from", c.from, RoleDim), col("to", c.to, RoleDim),
		col("type", c.typ, RoleDim), {Text: "message", Role: RoleDim}}
	return FitLine(l, width)
}

// sameFanOut: e repeats the last line's directive to another agent, written
// by the same person within a second: busmon's @all, sent one entry per agent
// (each agent keeps its own delivery) but read as one line.
func sameFanOut(fl feedLine, e bus.Event, at time.Time) bool {
	p := fl.ev
	if fl.sep || e.Kind != "cmd" || p.Kind != "cmd" || !e.Human || !p.Human || e.Type != bus.CmdDirective ||
		p.Type != bus.CmdDirective || e.From != p.From || e.Message != p.Message || e.Target == p.Target {
		return false
	}
	if at.Sub(fl.at) > time.Second || at.Before(fl.at) {
		return false
	}
	for _, t := range fl.to {
		if t == e.Target {
			return false
		}
	}
	return true
}

// todayFunc is the clock the day separator compares with (a test seam).
var todayFunc = time.Now

// activityRow renders one feed line, event or day separator, in exactly width cells.
func activityRow(fl feedLine, tier Tier, width int) Line {
	return activityRowAt(fl, tier, width, todayFunc())
}

// activityRowCalls counts the rows laid out (the lag test: a message that
// changes no line lays out none).
var activityRowCalls int

// activityKey is every input activityRowAt reads, so a hit is never stale:
// the event whole (comparable), its time, the fan-out's size, the mark, the
// tier and width, and for a day separator the day it is judged on. Colours
// are not in it: rows carry roles, painted at draw time.
type activityKey struct {
	ev    bus.Event
	at    int64
	sep   bool
	to    int
	mark  mark
	tier  Tier
	width int
	day   string
}

// activityCache keeps the Activity rows laid out by the last fill (the
// person's lag: every message laid out the whole feed again). Each fill keeps
// only the rows it used, so it never outgrows the feed.
type activityCache struct{ rows, next map[activityKey]Line }

// row is activityRowAt, laid out only when one of its inputs changed.
func (c *activityCache) row(fl feedLine, tier Tier, width int, today time.Time) Line {
	k := activityKey{ev: fl.ev, at: fl.at.UnixNano(), sep: fl.sep, to: len(fl.to), mark: fl.mark, tier: tier, width: width}
	if fl.sep {
		k.day = today.Format("2006-01-02")
	}
	if c.next == nil {
		c.next = map[activityKey]Line{}
	}
	l, ok := c.next[k]
	if !ok {
		if l, ok = c.rows[k]; !ok {
			l = activityRowAt(fl, tier, width, today)
		}
		c.next[k] = l
	}
	return l
}

// sweep ends a fill: the rows it used are the cache.
func (c *activityCache) sweep() { c.rows, c.next = c.next, nil }

// activityRowAt is activityRow with "(today)" judged at today: the shell's
// model time, so its rows never read the clock.
func activityRowAt(fl feedLine, tier Tier, width int, today time.Time) Line {
	activityRowCalls++
	if fl.sep {
		head := strings.Repeat(glyphHorizontal, 2) + " " + fl.at.Format("Mon 2006-01-02") + " "
		l := Line{{Text: head, Role: RoleMuted}}
		if y, m, d := fl.at.Date(); y == today.Year() && m == today.Month() && d == today.Day() {
			l = append(l, Seg{Text: "- ", Role: RoleMuted}, Seg{Text: "(today)", Role: RoleOK, Bold: true}, Seg{Text: " ", Role: RoleMuted})
		}
		rest := max(0, width-l.Width())
		return FitLine(append(l, Seg{Text: strings.Repeat(glyphHorizontal, rest), Role: RoleMuted}), width)
	}
	look := lookEvent(fl.ev)
	if len(fl.to) > 1 {
		look.To = "@all (" + strconv.Itoa(len(fl.to)) + ")"
	}
	c := colsFor(tier)
	l := Line{markCell(fl.mark), col(fl.at.Format("15:04"), c.time, RoleMuted), col(Preview(look.From), c.from, RoleText)}
	msg := Preview(look.Text)
	if tier == TierNarrow {
		if look.To != "" {
			msg = GlyphArrowR + " " + Preview(look.To) + " " + msg
		}
	} else {
		to := ""
		if look.To != "" {
			to = GlyphArrowR + " " + Preview(look.To)
		}
		l = append(l, col(to, c.to, RoleMuted))
	}
	l = append(l, col(look.Type, c.typ, look.Role), Seg{Text: msg, Role: RoleText})
	return FitLine(l, width)
}

// markCell is the unread column, before the time in every tier: the mark and
// its gap (busmon UX item 8). The dots are the working/idle glyphs, read in
// their own column; the glyph differs, so the no-colour palettes tell them apart.
func markCell(m mark) Seg {
	switch m {
	case markUnread:
		return Seg{Text: GlyphDotFull + " ", Role: RoleAttention}
	case markSuperseded:
		return Seg{Text: GlyphDotEmpty + " ", Role: RoleMuted}
	}
	return Seg{Text: "  ", Role: RoleMuted}
}

// pauseCursor counts what arrived while Activity was paused: events admitted
// by the current filter with a sequence number past the pause point. The count
// is recomputed from the feed on every fill (Recount), so a filter change
// while paused is reflected at once (spec §3.4).
type pauseCursor struct {
	paused bool
	at     int // last sequence number visible when the pause started
	n      int
}

// Pause records the pause point; pausing again while paused keeps the first.
func (p *pauseCursor) Pause(lastSeq int) {
	if p.paused {
		return
	}
	p.paused, p.at, p.n = true, lastSeq, 0
}

// Recount sets the count from the sequence numbers of the events the current
// filter admits (day separators excluded).
func (p *pauseCursor) Recount(admitted []int) {
	p.n = 0
	if !p.paused {
		return
	}
	for _, seq := range admitted {
		if seq > p.at {
			p.n++
		}
	}
}

func (p *pauseCursor) Resume()      { *p = pauseCursor{} }
func (p *pauseCursor) Paused() bool { return p.paused }
func (p *pauseCursor) New() int     { return p.n }

// activityFrame is the Activity frame title (live/paused) and label (event
// count and filter). The filter stays visible whatever is selected.
func activityFrame(p *pauseCursor, shown, total int, f feedFilter) (title, label Line) {
	title = Line{{Text: "Activity", Role: RoleText}}
	if p.Paused() {
		title = append(title, Seg{Text: fmt.Sprintf("  auto-scroll off %s %d new", GlyphNone, p.New()), Role: RoleAttention})
	} else {
		title = append(title, Seg{Text: "  auto-scroll on", Role: RoleOK})
	}
	count := fmt.Sprintf("%d events", total)
	if shown != total {
		count = fmt.Sprintf("%d of %d events", shown, total)
	}
	filter := "none"
	if !f.empty() {
		filter = f.describe()
	}
	label = Line{{Text: count + " " + GlyphNone + " filter: " + Preview(filter), Role: RoleMuted}}
	return title, label
}

// admittedEvents counts the feed's events (day separators are not events) and
// lists the sequence numbers of those the filter admits: what "N new" and the
// title's counts are measured on.
func admittedEvents(feed []feedLine, f feedFilter) (admitted []int, events int) {
	for _, fl := range feed {
		if fl.sep {
			continue
		}
		events++
		if f.match(fl) {
			admitted = append(admitted, fl.seq)
		}
	}
	return admitted, events
}

// currentAsks is the asks the last fill saw, by id (draw goroutine only).
var currentAsks = map[string]bus.Ask{}
