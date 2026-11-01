package main

import (
	"fmt"
	"strings"
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
		switch d.PlaceOf(records[d.ID], status) {
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
// a muted ● when something is only in hand (the person's answer: not counted in the
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

// healthView is the F5 page's own state: the selected row, the rows open
// in place, and a (the fine checks listed too).
type healthView struct {
	sel  int
	open map[string]bool
	all  bool
}

// healthItem is a selectable row of the page: its check and body line.
type healthItem struct {
	id   string
	line int
}

// healthWide: from this width a row carries its command or who deals with it;
// below, a For-you row's command goes on its own line (it is the one thing
// to read whole).
const healthWide = 100

// healthPage is F5 in the one list pattern (busmon pages, Health): its title
// with the counter summary, one line per check by place (For you, In hand,
// Not measured, the fine ones folded unless all), Enter's facts under an open
// row, and plain: the selected For-you row's command, what y copies.
func healthPage(records map[string]guardian.Record, notes map[string]guardian.Note, now time.Time, project string, v healthView, width int) (title string, body []Line, items []healthItem, plain string) {
	type row struct {
		d      guardian.Definition
		rec    guardian.Record
		has    bool
		status guardian.Status
	}
	byPlace := map[guardian.Place][]row{}
	for _, d := range guardian.Definitions() {
		rec, has := records[d.ID]
		st := checkStatus(d, records, now)
		place := d.PlaceOf(rec, st)
		byPlace[place] = append(byPlace[place], row{d, rec, has, st})
	}
	unmeasured := "not measured"
	if width < healthWide {
		unmeasured = "?"
	}
	title = "Health"
	if sum := listSummary(listCount{len(byPlace[guardian.PlaceForYou]), "to do"}, listCount{len(byPlace[guardian.PlaceInHand]), "in hand"},
		listCount{len(byPlace[guardian.PlaceFine]), "fine"}, listCount{len(byPlace[guardian.PlaceNotMeasured]), unmeasured}); sum != "" {
		title += " " + GlyphNone + " " + sum
	}
	command := func(d guardian.Definition) string { return strings.ReplaceAll(d.Command, "<project>", project) }
	nameW, noteW := 18, 0
	if width >= healthWide { // the columns as wide as what they hold, so the state keeps the rest
		nameW = 0
		for _, d := range guardian.Definitions() {
			nameW = min(max(nameW, Width(d.Title)), 32)
		}
		for place, rows := range byPlace {
			for _, r := range rows {
				switch place {
				case guardian.PlaceForYou:
					noteW = max(noteW, Width(command(r.d)))
				case guardian.PlaceInHand:
					noteW = max(noteW, Width(r.d.Handler)+2)
				}
			}
		}
		noteW = min(noteW, 34)
	}
	sections := []struct {
		place guardian.Place
		name  string
	}{{guardian.PlaceForYou, "For you"}, {guardian.PlaceInHand, "In hand"}, {guardian.PlaceNotMeasured, "Not measured"}, {guardian.PlaceFine, "Fine"}}
	for _, sec := range sections {
		rows := byPlace[sec.place]
		if len(rows) == 0 || sec.place == guardian.PlaceFine && !v.all {
			continue
		}
		body = append(body, listSection(sec.name))
		for _, r := range rows {
			lr := listRow{Name: r.d.Title, State: healthState(r.rec, r.has, r.status)}
			if r.has && r.rec.Since > 0 && r.status != guardian.Unknown {
				lr.Age = humanAge(now.Sub(time.UnixMilli(r.rec.Since)))
			} else if r.has && r.rec.ObservedAt > 0 {
				lr.Age = humanAge(now.Sub(time.UnixMilli(r.rec.ObservedAt)))
			}
			switch sec.place {
			case guardian.PlaceForYou:
				lr.Sym, lr.Role, lr.Note = GlyphBlocked, RoleAttention, Line{S(RoleAccent, command(r.d))}
				if r.status == guardian.Fail {
					lr.Role = RoleError
				}
			case guardian.PlaceInHand:
				lr.Sym, lr.Role, lr.Note = GlyphWorking, RoleAccent, Line{S(RoleMuted, "("+r.d.Handler+")")}
			case guardian.PlaceNotMeasured:
				lr.Sym, lr.Role = "?", RoleMuted
			default:
				lr.Sym, lr.Role = GlyphDone, RoleOK
			}
			selected := len(items) == v.sel
			if selected && sec.place == guardian.PlaceForYou {
				plain = command(r.d)
			}
			items = append(items, healthItem{id: r.d.ID, line: len(body)})
			body = append(body, lr.line(selected, width, nameW, noteW))
			if sec.place == guardian.PlaceForYou && noteW == 0 {
				body = append(body, FitLine(Line{S(RoleText, "    "), S(RoleAccent, command(r.d))}, width))
			}
			if v.open[r.d.ID] {
				body = append(body, healthFacts(r.d, r.rec, r.has, r.status, notes, width)...)
			}
		}
	}
	if n := len(byPlace[guardian.PlaceFine]); n > 0 && !v.all {
		body = append(body, listFold("fine", n, tourKeys(tourRef{m: ModeOverlay, act: ActShowAll})))
	}
	return title, body, items, plain
}

// healthState is a row's short state: the check's own detail, what is
// stale, or that it was never measured.
func healthState(rec guardian.Record, has bool, status guardian.Status) string {
	switch {
	case !has:
		return "no current measurement"
	case status == guardian.Unknown && rec.Status != guardian.Unknown:
		return "stale: " + rec.Detail
	case rec.Detail == "":
		return string(status)
	}
	return rec.Detail
}

// healthFacts are an open row's facts, under it behind a muted guide: since,
// the whole detail, what to do (the static remediation) and sentinel's note.
func healthFacts(d guardian.Definition, rec guardian.Record, has bool, status guardian.Status, notes map[string]guardian.Note, width int) []Line {
	var f fields
	since := "never measured"
	if has && rec.Since > 0 {
		since = time.UnixMilli(rec.Since).Format("2006-01-02 15:04")
	}
	f.add("since", S(RoleText, since))
	f.add("status", S(RoleText, string(status)))
	f.add("detail", S(RoleText, DetailText(healthState(rec, has, status))))
	f.add("what to do", S(RoleMuted, d.Remediation))
	if note, ok := notes[d.ID]; ok {
		f.add("note", S(RoleText, DetailText(note.Author+": "+note.Text)))
	}
	guide := Line{S(RoleText, "    "), S(RoleMuted, glyphVertical+" ")}
	var out []Line
	for _, l := range f.lines(max(10, width-guide.Width())) {
		out = append(out, append(append(Line{}, guide...), l...))
	}
	return out
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
