package main

import (
	"fmt"
	"strings"

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
		case bus.CmdBlocked:
			look.Role = RoleError
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
	l := Line{col("time", c.time, RoleDim), col("from", c.from, RoleDim), col("to", c.to, RoleDim),
		col("type", c.typ, RoleDim), {Text: "message", Role: RoleDim}}
	return FitLine(l, width)
}

// activityRow renders one feed line, event or day separator, in exactly width cells.
func activityRow(fl feedLine, tier Tier, width int) Line {
	if fl.sep {
		head := strings.Repeat(glyphHorizontal, 2) + " " + fl.at.Format("Mon 2006-01-02") + " "
		rest := max(0, width-Width(head))
		return FitLine(Line{{Text: head + strings.Repeat(glyphHorizontal, rest), Role: RoleDim}}, width)
	}
	look := lookEvent(fl.ev)
	c := colsFor(tier)
	l := Line{col(fl.at.Format("15:04"), c.time, RoleMuted), col(Preview(look.From), c.from, RoleText)}
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

// pauseCursor counts what arrived while Activity was paused: events admitted
// by the current filter with a sequence number past the pause point. Day
// separators are never passed to Arrive.
type pauseCursor struct {
	paused bool
	at     int // last sequence number visible when the pause started
	seen   int // highest sequence counted so far
	n      int
}

// Pause records the pause point; pausing again while paused keeps the first.
func (p *pauseCursor) Pause(lastSeq int) {
	if p.paused {
		return
	}
	p.paused, p.at, p.seen, p.n = true, lastSeq, lastSeq, 0
}

// Arrive counts one admitted event that arrived while paused, once per seq.
func (p *pauseCursor) Arrive(seq int, admitted bool) {
	if !p.paused || !admitted || seq <= p.seen {
		return
	}
	p.seen = seq
	p.n++
}

func (p *pauseCursor) Resume()      { *p = pauseCursor{} }
func (p *pauseCursor) Paused() bool { return p.paused }
func (p *pauseCursor) New() int     { return p.n }

// activityFrame is the Activity frame title (live/paused) and label (event
// count and filter). The filter stays visible whatever is selected.
func activityFrame(p *pauseCursor, shown, total int, f feedFilter) (title, label Line) {
	title = Line{{Text: "Activity", Role: RoleText}}
	if p.Paused() {
		title = append(title, Seg{Text: fmt.Sprintf("  paused %s %d new", GlyphNone, p.New()), Role: RoleAttention})
	} else {
		title = append(title, Seg{Text: "  live", Role: RoleOK})
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
