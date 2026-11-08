package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
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

// priorityWordW is the priority column at the wide width: "high", "low".
const priorityWordW = 4

// reqNoteRoom is the subject's room a row keeps before it shows a note.
const reqNoteRoom = 24

// priority is a level as the row's mark: the word at the wide width
// (progress-line §2.1), the one-cell arrow under reqKindWidth.
func (c reqLayout) priority(level string) Seg {
	m := priorityMark(level)
	if c.short {
		return m
	}
	switch level {
	case bus.PriorityHigh, bus.PriorityLow:
		m.Text = level
	}
	return m
}

// row lays out one row: symbol, from → to, the priority mark, kind,
// subject, age.
func (c reqLayout) row(sym, mark Seg, from, target, self, kind, subject string, at, now time.Time) Line {
	return c.rowNote(sym, mark, from, target, self, kind, subject, nil, at, now)
}

// rowNote is row with a note before the age (a request's plan task,
// progress-line §2.2); the note is dropped first when the row is short.
func (c reqLayout) rowNote(sym, mark Seg, from, target, self, kind, subject string, note Line, at, now time.Time) Line {
	if c.short {
		if k, ok := reqKindShort[kind]; ok {
			kind = k
		}
	}
	r := listRow{Sym: sym.Text, Role: sym.Role, Name: c.who(from, self) + " " + GlyphArrowR + " " + c.who(target, self),
		State: Fit(kind, c.kindW) + subject, Mark: mark, HasMark: true, Note: note}
	if !c.short {
		r.MarkW = priorityWordW
	}
	if !at.IsZero() {
		r.Age = humanAge(now.Sub(at))
	}
	noteW := note.Width()
	markW := max(r.MarkW, 1)
	subjectW := c.width - (Width(r.Sym) + 1 + c.nameW + 2 + 2 + listAgeW) - (markW + 1) - c.kindW - (noteW + 2)
	if noteW > 0 && subjectW < reqNoteRoom {
		noteW = 0 // the subject keeps its room: the link goes first (progress-line §2.2)
	}
	return r.cells(c.width, c.nameW, noteW)
}

// reqSym is a row's symbol: a target held by its quota first, ● in the
// error colour (it explains the silence, whatever is owed: reviewer-codex);
// then what the person or a reviewer owes is ◆; a message's delivery to its
// target is ● unread or ○ read; · when its cursor was not read.
func reqSym(sec reqSection, kind, state string) Seg {
	switch {
	case state == "held quota":
		return S(RoleError, GlyphDotFull)
	case sec == secOwed || sec == secTracked || kind == kindChallenge:
		return S(RoleAttention, GlyphGate)
	case state == "unread":
		return S(RoleAttention, GlyphDotFull)
	case state == "read":
		return S(RoleMuted, GlyphDotEmpty)
	}
	return S(RoleMuted, GlyphNone)
}

// priorityMark is a request's level as its row shows it: ↑ in the attention
// colour for high, ↓ muted for low, a blank cell for normal or no request.
func priorityMark(level string) Seg {
	switch level {
	case bus.PriorityHigh:
		return S(RoleAttention, GlyphUp)
	case bus.PriorityLow:
		return S(RoleMuted, GlyphDownArrow)
	}
	return S(RoleText, " ")
}

// noMark is the blank mark of a row that is no request.
var noMark = priorityMark("")

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
// them are the first line of a thread's page (reqSectionNote). Three headings
// for five sections (home-inbox-drawer §5): tracked requests, gated reviews
// and directives are all between agents.
var reqSectionNames = map[reqSection]string{secOwed: "Owed by you", secWait: "You wait on", secTracked: "Between agents",
	secReviews: "Between agents", secDirectives: "Between agents"}

// reqBetween are the sections under "Between agents", in their order.
var reqBetween = []reqSection{secTracked, secReviews, secDirectives}

// requestRows are the view's list items in the one list pattern (busmon
// pages, Requests): a heading per section with something in it, one line
// per group, the notices and the done threads each folded into one line.
func requestRows(v reqView, open reqOpen, now time.Time, width int) []ListItem {
	c := reqLayoutFor(width)
	fit := func(l Line) Line { return FitLine(l, width) }
	sep := " " + GlyphNone + " "
	var out []ListItem
	head := func(s reqSection, n int) { // the heading and its count: "Owed by you  2"
		out = append(out, ListItem{Line: fit(Line{{Text: reqSectionNames[s], Role: RoleText, Bold: true}, S(RoleMuted, fmt.Sprintf("  %d", n))})})
	}
	threadRow := func(th reqThread, indent bool) ListItem {
		sym := reqSym(th.Section, th.Kind, th.State) // an expanded thread keeps its own marker (reviewer-codex)
		subject := reqSubject(th)
		if indent {
			subject = "  " + subject
		}
		mark := noMark
		var note Line
		if th.Request != nil {
			mark = c.priority(th.Request.Priority)
			if pl, ok := v.plans[th.Request.Task]; ok {
				note = Line{S(RoleMuted, "plan #"+Preview(pl.id)+" "+plan.Label(pl.state))}
			}
		}
		return ListItem{Key: threadKey(th), Selectable: true, Line: fit(c.rowNote(sym, mark, th.From, th.Target, v.self, th.Kind, Preview(subject), note, th.At, now))}
	}
	groupRow := func(g reqGroup) []ListItem {
		if len(g.Threads) == 1 {
			items := []ListItem{threadRow(g.Threads[0], false)}
			if q := g.Threads[0].Request; q != nil && q.Tracked && q.State == "accepted" { // progress-line §3: not a row
				if l, ok := progressLine(*q, v.paces[reqOwner(*q)], now); ok {
					items = append(items, ListItem{Line: fit(append(Line{{Text: "     ", Role: RoleDim}}, l...))})
				}
			}
			return items
		}
		subject := g.Threads[len(g.Threads)-1].Subject
		if g.Kind == kindChallenge {
			subject = g.Subjects()
		}
		subject = fmt.Sprintf("%s (+%d)", Preview(subject), len(g.Threads)-1)
		key := g.Key()
		items := []ListItem{{Key: key, Selectable: true, Expanded: open.groups[key],
			Line: fit(c.row(reqSym(g.Section, g.Kind, g.State()), c.priority(groupPriority(g)), g.From, g.Target, v.self, g.Kind, subject, g.Oldest(), now))}}
		if open.groups[key] {
			for _, th := range g.Threads {
				items = append(items, threadRow(th, true))
			}
		}
		return items
	}

	if len(v.asks) > 0 || len(v.groups[secOwed]) > 0 {
		head(secOwed, v.count(secOwed))
		for _, a := range v.asks {
			word := "questions"
			if a.Open == 1 {
				word = "question"
			}
			out = append(out, ListItem{Key: "ask:" + a.ID, Selectable: true,
				Line: fit(c.row(S(RoleAttention, GlyphGate), noMark, a.From, v.self, v.self, "ask", fmt.Sprintf("%s (%d %s)", Preview(a.Title), a.Open, word), a.At, now))})
		}
		for _, g := range v.groups[secOwed] {
			out = append(out, groupRow(g)...)
		}
	}
	if gs := v.groups[secWait]; len(gs) > 0 {
		head(secWait, v.count(secWait))
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
					Line: fit(c.row(S(RoleMuted, " "), noMark, g.From, g.Target, v.self, g.Kind, label, older.Oldest(), now))})
			}
		}
	}
	between := 0
	for _, s := range reqBetween {
		between += v.count(s)
	}
	if between > 0 {
		head(secTracked, between)
		for _, s := range reqBetween {
			for _, g := range v.groups[s] {
				out = append(out, groupRow(g)...)
			}
		}
	}
	if n := v.count(secNotices); n > 0 {
		label := fmt.Sprintf("notices%s%d, nobody owes an answer  (enter shows them)", sep, n)
		if open.groups[reqNoticesKey] {
			label = fmt.Sprintf("notices%s%d, nobody owes an answer  (enter folds them)", sep, n)
		}
		out = append(out, ListItem{Key: reqNoticesKey, Selectable: true, Expanded: open.groups[reqNoticesKey], Line: fit(reqFold(label))})
		if open.groups[reqNoticesKey] { // in place, one row per from → to as before the fold (foureyes)
			for _, g := range v.groups[secNotices] {
				if len(g.Threads) == 1 { // an only notice opens itself (a report notice its report)
					out = append(out, threadRow(g.Threads[0], false))
					continue
				}
				subject := fmt.Sprintf("%s (+%d)", Preview(g.Threads[len(g.Threads)-1].Subject), len(g.Threads)-1)
				out = append(out, ListItem{Key: "ntc:" + g.Key(), Selectable: true,
					Line: fit(c.row(reqSym(g.Section, g.Kind, g.State()), noMark, g.From, g.Target, v.self, g.Kind, subject, g.Oldest(), now))})
			}
		}
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

// reqNoticesKey is the one line every notice folds into; Enter shows their
// groups in place, each opened or closed (d) as a group.
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
	// "directives", not "between agents": the heading Between agents now sums
	// tracked, gated and directives (home-inbox-drawer §5), as the header does.
	agents := "directives"
	if width < 100 {
		owed, agents = "owed", "dir"
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
	in := reqInput{cmds: s.cmdHistory, reqs: s.requests, questions: openQuestionCount(s.asks), asks: reqAsks(s.asks), plans: planLinks(s.planState), self: m.cfg.self, now: m.now,
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
	case key == reqNoticesKey, strings.HasPrefix(key, "grp:"), strings.HasPrefix(key, "older:"):
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
