package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Ideas page, F6 (ideas slice 1): the project's ideas from the broker
// ({p}:ideas, bus/ideas.go) in the one list pattern: a section per state in
// the order of life, tab narrowing to one state, enter opening an idea, a
// adding one, s opening the state picker under the selected row (y sets it,
// as the person: the bus refuses an agent's name).

// ideasView is the page's own state.
type ideasView struct {
	filter int    // 0 all, else bus.IdeaStates[filter-1]
	selID  string // the selected idea: it wins over sel while it is listed
	pick   *ideaPick
}

// ideaPick is s's picker: the idea, the state it was in when opened (the
// compare-and-set's expected state), and the cursor among bus.IdeaStates.
type ideaPick struct {
	id, from string
	sel      int
}

// ideaItem is a selectable row: its idea and body line.
type ideaItem struct {
	id   string
	line int
}

func ideaFilterName(f int) string {
	if f <= 0 || f > len(bus.IdeaStates) {
		return "all"
	}
	return bus.IdeaStates[f-1]
}

func nextIdeaFilter(f int) int { return (f + 1) % (len(bus.IdeaStates) + 1) }

// ideaSetter is an idea's last setter as a note, flagged when it is not the
// configured person ("" person: nobody to compare with).
func ideaSetter(it bus.Idea, person string, now time.Time) Line {
	by, at := it.SetBy()
	if by == "" {
		return Line{S(RoleMuted, "added by "+it.By)}
	}
	text := "set by " + by + " " + humanAge(now.Sub(time.UnixMilli(at))) + " ago"
	if person != "" && by != person {
		return Line{S(RoleAttention, text+", not you")}
	}
	return Line{S(RoleMuted, text)}
}

// ideasPage builds F6: its title with the counts, the rows by state, the
// picker under its row, and plain: the selected idea's title (y).
func ideasPage(ideas []bus.Idea, unread bool, person string, v ideasView, sel int, now time.Time, width int) (title string, body []Line, items []ideaItem, plain string) {
	title = "Ideas"
	if unread {
		body = append(body, Line{S(RoleAttention, "  the ideas could not be read: the list may be old")})
	}
	by := map[string][]bus.Idea{}
	for _, it := range ideas {
		by[it.State] = append(by[it.State], it)
	}
	var counts []listCount
	for _, st := range bus.IdeaStates {
		counts = append(counts, listCount{len(by[st]), st})
	}
	if sum := listSummary(counts...); sum != "" {
		title += " " + GlyphNone + " " + sum
	}
	only := ""
	if v.filter > 0 {
		only = ideaFilterName(v.filter)
		title += " " + GlyphNone + " " + only + " only"
	}
	if len(ideas) == 0 && !unread {
		return title, append(body, Line{S(RoleMuted, "  no idea yet: a adds one (agents add theirs with agentbus idea add)")}), nil, ""
	}
	if v.selID != "" {
		i := 0
		for _, st := range bus.IdeaStates {
			if only != "" && st != only {
				continue
			}
			for _, it := range by[st] {
				if it.ID == v.selID {
					sel = i
				}
				i++
			}
		}
	}
	nameW := 0
	for _, it := range ideas {
		nameW = max(nameW, Width(it.ID+"  "+it.Title))
	}
	nameW = min(nameW, max(20, width/2))
	noteW := 0
	if width >= healthWide {
		noteW = 28
	}
	for _, st := range bus.IdeaStates {
		if only != "" && st != only {
			continue
		}
		rows := by[st]
		if len(rows) == 0 {
			continue
		}
		body = append(body, listSection(strings.ToUpper(st[:1])+st[1:]))
		for _, it := range rows {
			selected := len(items) == sel
			if selected {
				plain = it.Title
			}
			setter := ideaSetter(it, person, now)
			lr := listRow{Sym: GlyphIdea, Role: RoleAccent, Name: it.ID + "  " + it.Title, Note: setter,
				Age: humanAge(now.Sub(time.UnixMilli(it.At)))}
			if it.State == bus.IdeaDropped {
				lr.Role = RoleMuted
			}
			if noteW == 0 { // narrow: the setter is the state column
				lr.State = setter[0].Text
			}
			items = append(items, ideaItem{id: it.ID, line: len(body)})
			l := lr.line(selected, width, nameW, noteW)
			if noteW == 0 && setter[0].Role == RoleAttention {
				l = flagSetter(l)
			}
			body = append(body, l)
			if v.pick != nil && v.pick.id == it.ID {
				body = append(body, ideaPicker(*v.pick, width)...)
			}
		}
	}
	if only != "" && len(by[only]) == 0 {
		body = append(body, Line{S(RoleMuted, "  no idea "+only+" (tab: the next state)")})
	}
	return title, body, items, plain
}

// flagSetter colours a narrow row's "set by" text as the wide note would.
func flagSetter(l Line) Line {
	for i := range l {
		if strings.Contains(l[i].Text, "set by") {
			l[i].Role = RoleAttention
		}
	}
	return l
}

// ideaPicker is s's picker, under its row: the four states, the idea's own
// marked "(now)", the cursor a pill, and the keys.
func ideaPicker(p ideaPick, width int) []Line {
	guide := Line{S(RoleText, "    "), S(RoleMuted, glyphVertical+" ")}
	l := append(Line{}, guide...)
	for i, st := range bus.IdeaStates {
		label := st
		if st == p.from {
			label += " (now)"
		}
		l = append(l, Seg{Text: " " + label + " ", Role: RoleAccent, Pill: i == p.sel}, S(RoleText, " "))
	}
	keys := append(append(Line{}, guide...), S(RoleMuted, GlyphUp+GlyphDownArrow+" choose "+GlyphNone+" y sets it, as you "+GlyphNone+" esc cancels"))
	return []Line{FitLine(l, width), FitLine(keys, width)}
}

// ideaDetail is an idea's page body: its facts, its text, its changes.
func ideaDetail(it bus.Idea, person string, now time.Time, width int) []Line {
	var f fields
	f.add("state", S(RoleText, it.State))
	f.add("added", S(RoleText, fmt.Sprintf("added by %s %s", it.By, time.UnixMilli(it.At).Local().Format("2006-01-02 15:04"))))
	if it.Source != "" {
		f.add("source", S(RoleText, DetailText(it.Source)))
	}
	if by, _ := it.SetBy(); by != "" {
		f.add("set", ideaSetter(it, person, now)...)
	}
	out := f.lines(width)
	if strings.TrimSpace(it.Text) != "" {
		out = append(out, Line{})
		for _, l := range strings.Split(it.Text, "\n") {
			out = append(out, FitLine(Line{S(RoleText, DetailText(l))}, width))
		}
	}
	if len(it.Log) > 0 {
		out = append(out, Line{}, listSection("Changes"))
		for i := len(it.Log) - 1; i >= 0; i-- {
			c := it.Log[i]
			out = append(out, FitLine(Line{S(RoleText, fmt.Sprintf("  %s → %s", c.From, c.To)),
				S(RoleMuted, fmt.Sprintf("  by %s %s", c.By, time.UnixMilli(c.At).Local().Format("2006-01-02 15:04")))}, width))
		}
	}
	out = append(out, Line{}, Line{S(RoleMuted, "s sets its state (you only; the bus refuses an agent's name and records who and when)")})
	return out
}
