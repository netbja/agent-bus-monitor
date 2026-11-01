package main

import (
	"fmt"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

// guardianSummary counts the checks by their place (busmon pages, Health).
type guardianSummary struct {
	ToDo       int  // only the person can fix: what the pill counts
	ToDoFail   bool // one of them fails: the pill reads red
	InHand     int  // something deals with it: never counted in the header
	Fine       int
	Unmeasured int // never fine
	Total      int
}

func summarizeGuardian(records map[string]guardian.Record, now time.Time) guardianSummary {
	s := guardianSummary{Total: len(guardian.Definitions())}
	for _, d := range guardian.Definitions() {
		status := checkStatus(d, records, now)
		switch d.Place(status) {
		case guardian.PlaceForYou:
			s.ToDo++
			s.ToDoFail = s.ToDoFail || status == guardian.Fail
		case guardian.PlaceInHand:
			s.InHand++
		case guardian.PlaceFine:
			s.Fine++
		default:
			s.Unmeasured++
		}
	}
	return s
}

// checkStatus is d's status now: unknown without a current measurement.
func checkStatus(d guardian.Definition, records map[string]guardian.Record, now time.Time) guardian.Status {
	rec, ok := records[d.ID]
	if !ok {
		return guardian.Unknown
	}
	return guardian.EffectiveStatus(rec, now.UnixMilli(), d.Interval)
}

// guardianSeg is the header's health pill at width: what only the person can
// fix ("health ▲ 2 to do (F5)", "health ▲2" below the wide tier, "▲2" in the
// compact header), else the checks not measured ("?3", never the tick), else
// a muted ● when something is only in hand (Bernard: not counted in the
// header), else the tick: every check measured ok.
func guardianSeg(s guardianSummary, width int) Line {
	name := "health " // a bare "!4" read as noise (A6); the compact header keeps the short form
	if width < 80 {
		name = ""
	}
	switch {
	case s.Total == 0:
		return Line{{Text: name + GlyphNone, Role: RoleMuted}}
	case s.ToDo > 0:
		role := RoleAttention
		if s.ToDoFail {
			role = RoleError
		}
		label := fmt.Sprintf("%s%d", GlyphBlocked, s.ToDo)
		if width >= wideMinWidth {
			label = fmt.Sprintf("%s %d to do (%s)", GlyphBlocked, s.ToDo, tourKeys(tourRef{m: ModeShell, act: ActHealth}))
		}
		return Line{{Text: name + label, Role: role}}
	case s.Unmeasured > 0: // not measured yet or unreadable, never shown as healthy
		return Line{{Text: fmt.Sprintf("%s?%d", name, s.Unmeasured), Role: RoleMuted}}
	case s.InHand > 0:
		return Line{{Text: name + GlyphWorking, Role: RoleMuted}}
	}
	return Line{{Text: name + GlyphDone, Role: RoleOK}}
}

func healthOverlayText(records map[string]guardian.Record, notes map[string]guardian.Note, now time.Time) []Line {
	var b body
	for _, d := range guardian.Definitions() {
		rec, ok := records[d.ID]
		status, since, detail := guardian.Unknown, "never measured", "no current measurement"
		if ok {
			status = guardian.EffectiveStatus(rec, now.UnixMilli(), d.Interval)
			if rec.Since > 0 {
				since = time.UnixMilli(rec.Since).Format(time.RFC3339)
			}
			if rec.Detail != "" {
				detail = rec.Detail
			}
			if status == guardian.Unknown {
				detail = "last measurement is stale or invalid: " + detail
			}
		}
		b.seg(RoleAccent, d.Title+"  "+string(status)).text("\n")
		b.text("  since: ").text(since).text("\n  detail: ").text(detail).text("\n  remediation: ").seg(RoleMuted, d.Remediation).text("\n")
		if note, ok := notes[d.ID]; ok {
			b.text("  sentinel note (").seg(RoleMuted, note.Author).text("): ").text(note.Text).text("\n")
		}
		b.text("\n")
	}
	lines := b.Lines()
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 { // the last blank line goes, as the tagged text's trailing "\n" did
		lines = lines[:n-1]
	}
	return lines
}

func healthRecapRows(rows []guardian.Transition) []recapRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]recapRow, 0, len(rows))
	for _, row := range rows {
		title := fmt.Sprintf("health %s: %s → %s", row.ID, row.Previous, row.Status)
		out = append(out, recapRow{Key: "health:" + row.StreamID, Section: "Since your last visit", Title: title,
			Detail: time.UnixMilli(row.At).Format(time.RFC3339), Kind: "health"})
	}
	return out
}
