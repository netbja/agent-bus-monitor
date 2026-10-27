package main

import (
	"fmt"
	"time"

	guardian "github.com/netbja/agent-bus-monitor/health"
)

type guardianSummary struct {
	Unhealthy int // every check not ok, unknown included
	Problems  int // fail and warn only: what the pill counts
	Total     int
	Worst     guardian.Status
}

func summarizeGuardian(records map[string]guardian.Record, now time.Time) guardianSummary {
	s := guardianSummary{Total: len(guardian.Definitions()), Worst: guardian.OK}
	for _, d := range guardian.Definitions() {
		rec, ok := records[d.ID]
		status := guardian.Unknown
		if ok {
			status = guardian.EffectiveStatus(rec, now.UnixMilli(), d.Interval)
		}
		if status != guardian.OK {
			s.Unhealthy++
		}
		if status == guardian.Fail || status == guardian.Warn {
			s.Problems++
		}
		if status == guardian.Fail {
			s.Worst = guardian.Fail
		} else if status == guardian.Warn && s.Worst != guardian.Fail {
			s.Worst = guardian.Warn
		} else if status == guardian.Unknown && s.Worst == guardian.OK {
			s.Worst = guardian.Unknown
		}
	}
	return s
}

func guardianSeg(s guardianSummary, compact bool) Line {
	name := "health " // a bare "!4" read as noise (A6); the compact header keeps the short form
	if compact {
		name = ""
	}
	if s.Total == 0 {
		return Line{{Text: name + GlyphNone, Role: RoleMuted}}
	}
	if s.Unhealthy == 0 {
		return Line{{Text: name + GlyphDone, Role: RoleOK}}
	}
	role := RoleMuted
	switch s.Worst {
	case guardian.Fail:
		role = RoleError
	case guardian.Warn:
		role = RoleAttention
	}
	label := fmt.Sprintf("!%d", s.Problems)
	if s.Problems == 0 { // only unknown: not measured yet or unreadable, never shown as healthy
		label = fmt.Sprintf("?%d", s.Unhealthy)
	}
	return Line{{Text: name + label, Role: role}}
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
