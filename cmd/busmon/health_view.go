package main

import (
	"fmt"
	"strings"
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

func guardianSeg(s guardianSummary) Line {
	if s.Total == 0 {
		return Line{{Text: GlyphNone, Role: RoleMuted}}
	}
	if s.Unhealthy == 0 {
		return Line{{Text: GlyphDone, Role: RoleOK}}
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
	return Line{{Text: label, Role: role}}
}

func healthOverlayText(records map[string]guardian.Record, notes map[string]guardian.Note, now time.Time) string {
	var b strings.Builder
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
		fmt.Fprintf(&b, "%s\n", tg(RoleAccent, d.Title+"  "+string(status)))
		fmt.Fprintf(&b, "  since: %s\n  detail: %s\n  remediation: %s\n", tg(RoleText, since), tg(RoleText, detail), tg(RoleMuted, d.Remediation))
		if note, ok := notes[d.ID]; ok {
			fmt.Fprintf(&b, "  sentinel note (%s): %s\n", tg(RoleMuted, note.Author), tg(RoleText, note.Text))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
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
