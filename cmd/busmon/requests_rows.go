package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Requests view's rows: a column header, then each section's header and
// rows. Columns follow the width: FROM → TO, KIND (≥ reqKindWidth), STATE
// (≥ reqStateWidth; below, the state is the row's marker), SUBJECT, N, AGE.

const (
	reqKindWidth = 106 // the row width from which the kind is spelled out (a 120-column screen)
	reqWaitShown = 2   // "you wait on": the person's newest messages shown before "+ N older"
)

// reqOpen is what the person opened in the view: groups expanded, the folded
// section shown.
type reqOpen struct {
	groups map[string]bool // group, "older:<target>" keys
	folded bool
}

// reqKindShort: the kind below reqKindWidth (busmon pages, Requests at 80).
var reqKindShort = map[string]string{kindRequest: "req", kindChallenge: "chal", kindDirective: "dir", kindReports: "rep", kindNotices: "ntc", "ask": "ask"}

// reqLayout is the row's columns at a width: the one list row (listrow.go),
// "from → to" as its name, the kind and subject as its state.
type reqLayout struct {
	width, nameW, kindW int
	short               bool
}

func reqLayoutFor(width int) reqLayout {
	if width >= reqKindWidth {
		return reqLayout{width: width, nameW: 24, kindW: 10}
	}
	return reqLayout{width: width, nameW: 18, kindW: 5, short: true}
}

// who is a name as a row reads it: the person is `you` (the header names them).
func (c reqLayout) who(name, self string) string {
	if name == self && self != "" {
		return "you"
	}
	return name
}

// row lays out one row: symbol, from → to, kind, subject, age.
func (c reqLayout) row(sym Seg, from, target, self, kind, subject string, at, now time.Time) Line {
	if c.short {
		if k, ok := reqKindShort[kind]; ok {
			kind = k
		}
	}
	r := listRow{Sym: sym.Text, Role: sym.Role, Name: c.who(from, self) + " " + GlyphArrowR + " " + c.who(target, self),
		State: Fit(kind, c.kindW) + subject}
	if !at.IsZero() {
		r.Age = humanAge(now.Sub(at))
	}
	return r.cells(c.width, c.nameW, 0)
}

// reqSym is a row's symbol: what the person or a reviewer owes is ◆; a
// message's delivery to its target is ● unread or ○ read (held quota ● in
// the error colour); · when its cursor was not read.
func reqSym(sec reqSection, kind, state string) Seg {
	switch {
	case sec == secOwed || sec == secTracked || kind == kindChallenge:
		return S(RoleAttention, GlyphGate)
	case state == "held quota":
		return S(RoleError, GlyphDotFull)
	case state == "unread":
		return S(RoleAttention, GlyphDotFull)
	case state == "read":
		return S(RoleMuted, GlyphDotEmpty)
	}
	return S(RoleMuted, GlyphNone)
}

func padLeft(s string, n int) string {
	if w := Width(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

// threadKey is a thread's list key.
func threadKey(th reqThread) string {
	if th.Request != nil {
		return th.ID // "req:<task>"
	}
	return "thr:" + th.ID
}

// reqSubject is a thread's subject as its row reads it: a tracked request
// carries its state, the one word that is the state itself.
func reqSubject(th reqThread) string {
	if th.Request != nil && th.State != "" {
		return th.Subject + " " + GlyphNone + " " + th.State
	}
	return th.Subject
}

// reqSectionNames are the sections' headings; the sentences that explained
// them are the first line of a thread's page (reqSectionNote).
var reqSectionNames = map[reqSection]string{secOwed: "Owed by you", secWait: "You wait on", secTracked: "Tracked",
	secReviews: "Reviews gated", secDirectives: "Between agents"}

// requestRows are the view's list items in the one list pattern (busmon
// pages, Requests): a heading per section with something in it, one line
// per group, the notices and the done threads each folded into one line.
func requestRows(v reqView, open reqOpen, now time.Time, width int) []ListItem {
	c := reqLayoutFor(width)
	fit := func(l Line) Line { return FitLine(l, width) }
	sep := " " + GlyphNone + " "
	var out []ListItem
	head := func(s reqSection) {
		out = append(out, ListItem{Line: fit(Line{{Text: reqSectionNames[s], Role: RoleText, Bold: true}})})
	}
	threadRow := func(th reqThread, indent bool) ListItem {
		sym := reqSym(th.Section, th.Kind, th.State)
		subject := reqSubject(th)
		if indent {
			sym, subject = S(RoleMuted, " "), "  "+subject
		}
		return ListItem{Key: threadKey(th), Selectable: true, Line: fit(c.row(sym, th.From, th.Target, v.self, th.Kind, Preview(subject), th.At, now))}
	}
	groupRow := func(g reqGroup) []ListItem {
		if len(g.Threads) == 1 {
			return []ListItem{threadRow(g.Threads[0], false)}
		}
		subject := g.Threads[len(g.Threads)-1].Subject
		if g.Kind == kindChallenge {
			subject = g.Subjects()
		}
		subject = fmt.Sprintf("%s (+%d)", Preview(subject), len(g.Threads)-1)
		key := g.Key()
		items := []ListItem{{Key: key, Selectable: true, Expanded: open.groups[key],
			Line: fit(c.row(reqSym(g.Section, g.Kind, g.State()), g.From, g.Target, v.self, g.Kind, subject, g.Oldest(), now))}}
		if open.groups[key] {
			for _, th := range g.Threads {
				items = append(items, threadRow(th, true))
			}
		}
		return items
	}

	if len(v.asks) > 0 || len(v.groups[secOwed]) > 0 {
		head(secOwed)
		for _, a := range v.asks {
			word := "questions"
			if a.Open == 1 {
				word = "question"
			}
			out = append(out, ListItem{Key: "ask:" + a.ID, Selectable: true,
				Line: fit(c.row(S(RoleAttention, GlyphGate), a.From, v.self, v.self, "ask", fmt.Sprintf("%s (%d %s)", Preview(a.Title), a.Open, word), a.At, now))})
		}
		for _, g := range v.groups[secOwed] {
			out = append(out, groupRow(g)...)
		}
	}
	if gs := v.groups[secWait]; len(gs) > 0 {
		head(secWait)
		for _, g := range gs {
			for i, th := range g.Threads {
				if i < reqWaitShown || open.groups[olderKey(g)] {
					out = append(out, threadRow(th, i >= reqWaitShown))
				}
			}
			if rest := g.Threads[min(reqWaitShown, len(g.Threads)):]; len(rest) > 0 {
				key := olderKey(g)
				older := reqGroup{Threads: rest}
				label := fmt.Sprintf("+ %d older  (enter shows them)", len(rest))
				if open.groups[key] {
					label = fmt.Sprintf("%d older shown  (enter folds them)", len(rest))
				}
				out = append(out, ListItem{Key: key, Selectable: true, Expanded: open.groups[key],
					Line: fit(c.row(S(RoleMuted, " "), g.From, g.Target, v.self, g.Kind, label, older.Oldest(), now))})
			}
		}
	}
	for _, s := range []reqSection{secTracked, secReviews, secDirectives} {
		if gs := v.groups[s]; len(gs) > 0 {
			head(s)
			for _, g := range gs {
				out = append(out, groupRow(g)...)
			}
		}
	}
	if n := v.count(secNotices); n > 0 {
		out = append(out, ListItem{Key: reqNoticesKey, Selectable: true, Line: fit(reqFold(fmt.Sprintf("notices%s%d, nobody owes an answer  (enter lists them)", sep, n)))})
	}
	if len(v.folded) > 0 {
		label := fmt.Sprintf("done or answered%s%d, older than 7 d folded  (o shows them)", sep, len(v.folded))
		if open.folded {
			label = fmt.Sprintf("done or answered%s%d, older than 7 d  (o folds them)", sep, len(v.folded))
		}
		out = append(out, ListItem{Key: "fold", Selectable: true, Expanded: open.folded, Line: fit(reqFold(label))})
		if open.folded {
			for _, th := range v.folded {
				out = append(out, threadRow(th, true))
			}
		}
	}
	return out
}

// reqNoticesKey is the one line every notice folds into.
const reqNoticesKey = "ntc:all"

// reqFold is a folded line of the list (listFold's look, its own words).
func reqFold(label string) Line {
	h := strings.Repeat(glyphHorizontal, 2)
	return Line{S(RoleMuted, h+" "+label+" "+h)}
}

// requestsTitle is the panel's counter summary (the list pattern): the
// sections' sums in their own words, the ones at 0 left out; shorter words
// under 100 cells.
func requestsTitle(v reqView, width int) Line {
	owed, wait := "owed by you", "waiting"
	agents := "between agents"
	if width < 100 {
		owed, agents = "owed", "agents"
	}
	sum := listSummary(listCount{v.count(secOwed), owed}, listCount{v.count(secWait), wait}, listCount{v.count(secTracked), "tracked"},
		listCount{v.count(secReviews), "gated"}, listCount{v.count(secDirectives), agents})
	if sum == "" {
		return Line{{Text: "nothing waiting", Role: RoleMuted}}
	}
	role := RoleMuted
	if v.count(secOwed) > 0 {
		role = RoleAttention // orange = what is owed to you (busmon-req-header)
	}
	return Line{{Text: sum, Role: role}}
}

// requestInput is the view's facts from the snapshot: the retained cmd
// stream, the board, every open gate, each agent's delivery cursor and hold.
func (m *Model) requestInput() reqInput {
	s := m.snap
	in := reqInput{cmds: s.cmdHistory, reqs: s.requests, questions: openQuestionCount(s.asks), asks: reqAsks(s.asks), self: m.cfg.self, now: m.now,
		gates: map[string]map[string]string{}, delivery: map[string]reqDelivery{}, held: map[string]bool{}}
	for n, a := range s.agents {
		if len(a.gates) > 0 {
			in.gates[n] = a.gates
		}
		if a.held {
			in.held[n] = true
		}
		if a.deliveryOK {
			in.delivery[n] = reqDelivery{lastDelivered: a.delivered, pending: a.pendingIDs}
		}
	}
	if len(s.personGates) > 0 && in.gates[m.cfg.self] == nil {
		in.gates[m.cfg.self] = s.personGates
	}
	return in
}

// reqAsks are the open asks as the view lists them, owed by the person.
func reqAsks(asks []bus.Ask) []reqAsk {
	var out []reqAsk
	for _, a := range openAsks(asks) {
		out = append(out, reqAsk{ID: a.ID, From: a.From, Title: a.Title, Open: a.Open(), At: time.UnixMilli(a.CreatedAt)})
	}
	return out
}

// olderKey is a "you wait on" group's "+ N older" row: the group's whole
// identity, so two groups to one recipient (directives, challenges) never share it.
func olderKey(g reqGroup) string { return "older:" + g.Key() }

// olderThreads are the threads behind an older row.
func olderThreads(v reqView, key string) []reqThread {
	for _, g := range v.groups[secWait] {
		if olderKey(g) == key {
			return g.Threads[min(reqWaitShown, len(g.Threads)):]
		}
	}
	return nil
}

// reqRowKey is a Requests row that stands for threads.
func reqRowKey(key string) bool {
	if key == reqNoticesKey { // every notice at once: d never closes them all
		return false
	}
	for _, p := range []string{"thr:", "grp:", "ntc:", "older:"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// reqThreadsOf are the threads a Requests row stands for.
func (m *Model) reqThreadsOf(key string) []reqThread {
	v := requestGroups(m.requestInput())
	if strings.HasPrefix(key, "older:") {
		return olderThreads(v, key)
	}
	var out []reqThread
	if key == reqNoticesKey {
		for _, g := range v.groups[secNotices] {
			out = append(out, g.Threads...)
		}
		return out
	}
	for s := secOwed; s < secFolded; s++ {
		for _, g := range v.groups[s] {
			if key == g.Key() || key == "ntc:"+g.Key() {
				return g.Threads
			}
			for _, th := range g.Threads {
				if threadKey(th) == key {
					out = append(out, th)
				}
			}
		}
	}
	for _, th := range v.folded {
		if threadKey(th) == key {
			out = append(out, th)
		}
	}
	return out
}

// openRequestRow is Enter on a Requests row: a group or "+ N older" expands
// (Enter again folds), the folded line shows them, a notice group lists its
// notices, a report notice opens the report, a thread its page, a request
// its request page, the questions link the person's open questions.
func (m *Model) openRequestRow(key string) tea.Cmd {
	o := &m.ui.reqOpen
	switch {
	case key == "asks":
		return m.pushAskOf("")
	case strings.HasPrefix(key, "ask:"):
		return m.pushAsk(strings.TrimPrefix(key, "ask:"))
	case key == "fold":
		o.folded = !o.folded
	case strings.HasPrefix(key, "req:"):
		return m.push(page{kind: ovRequest, key: key, lead: reqSectionNote(secTracked)})
	case strings.HasPrefix(key, "grp:"), strings.HasPrefix(key, "older:"):
		if o.groups == nil {
			o.groups = map[string]bool{}
		}
		o.groups[key] = !o.groups[key]
	case strings.HasPrefix(key, "ntc:"):
		ths := m.reqThreadsOf(key)
		if len(ths) == 0 {
			return nil
		}
		var b body
		b.seg(RoleMuted, reqSectionNote(secNotices)).text("\n\n")
		for _, th := range ths {
			b.seg(RoleMuted, padLeft(humanAge(m.now.Sub(th.At)), 4)+"  ").text(Preview(th.From) + " " + GlyphArrowR + " " + Preview(th.Target) + "  ").text(Preview(th.Subject)).text("\n")
		}
		return m.push(page{kind: ovNotices, title: fmt.Sprintf("notices %s %d", GlyphNone, len(ths)), body: b.Lines(), fixed: true})
	case strings.HasPrefix(key, "thr:"):
		ths := m.reqThreadsOf(key)
		if len(ths) == 0 {
			return nil
		}
		th := ths[0]
		if th.ReportID != "" {
			for _, fl := range m.feed.feed {
				if !fl.sep && fl.ev.Kind == "report" && fl.ev.ID == th.ReportID {
					return m.pushMessage(fl.ev, entryTime(fl.ev.ID))
				}
			}
		}
		pg := messagePage(th.Root, th.At)
		pg.lead = reqSectionNote(th.Section)
		return m.push(pg)
	}
	m.refresh()
	return nil
}
