package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The Requests view's rows: a column header, then each section's header and
// rows. Columns follow the width: FROM → TO, KIND (≥ reqKindWidth), STATE
// (≥ reqStateWidth; below, the state is the row's marker), SUBJECT, N, AGE.

const (
	reqKindWidth  = 106 // the row width from which KIND has a column (a 120-column screen)
	reqStateWidth = 150 // and STATE (a 160-column screen)
	reqWaitShown  = 2   // "you wait on": the person's newest messages shown before "+ N older"
)

// reqOpen is what the person opened in the view: groups expanded, the folded
// section shown.
type reqOpen struct {
	groups map[string]bool // group, "older:<target>" keys
	folded bool
}

type reqCols struct {
	width, fromTo, kind, state, n, subject int
	arrow                                  string
}

func reqColumns(width int) reqCols {
	c := reqCols{width: width, fromTo: 18, n: 3, arrow: GlyphArrowR}
	if width >= reqKindWidth {
		c.fromTo, c.kind = 22, 9
	}
	if width >= reqStateWidth {
		c.fromTo, c.state, c.n, c.arrow = 26, 11, 5, " "+GlyphArrowR+" "
	}
	// marker 2, from→to, kind, state, subject, " N", " AGE"
	c.subject = max(4, width-2-c.fromTo-1-c.kind-c.state-c.n-1-4-1)
	if c.kind > 0 {
		c.subject--
	}
	if c.state > 0 {
		c.subject--
	}
	return c
}

func (c reqCols) header() Line {
	l := Line{S(RoleMuted, "  "+Fit("FROM"+c.arrow+"TO", c.fromTo)+" ")}
	if c.kind > 0 {
		l = append(l, S(RoleMuted, Fit("KIND", c.kind)+" "))
	}
	if c.state > 0 {
		l = append(l, S(RoleMuted, Fit("STATE", c.state)+" "))
	}
	subj := "SUBJECT"
	if c.kind > 0 {
		subj = "SUBJECT / FIRST LINE"
	}
	return append(l, S(RoleMuted, Fit(subj, c.subject)+" "+padLeft("N", c.n)+" "+padLeft("AGE", 4)))
}

// row lays out one row: marker, from → to, kind, state, subject, n, age.
func (c reqCols) row(marker Seg, from, target, kind, state string, subject Line, n int, at, now time.Time) Line {
	ft := Preview(from) + c.arrow + Preview(target)
	l := Line{marker, S(RoleText, Fit(ft, c.fromTo)+" ")}
	if c.kind > 0 {
		l = append(l, S(RoleMuted, Fit(kind, c.kind)+" "))
	}
	if c.state > 0 {
		l = append(l, S(reqStateRole(state), Fit(state, c.state)+" "))
	}
	l = append(l, FitLine(subject, c.subject)...) // padded to the column
	age := ""
	if !at.IsZero() {
		age = humanAge(now.Sub(at))
	}
	return append(l, S(RoleMuted, " "+padLeft(fmt.Sprint(n), c.n)+" "+padLeft(age, 4)))
}

func reqStateRole(state string) Role {
	switch state {
	case "unread", "blocked":
		return RoleAttention
	case "held quota":
		return RoleError
	case "accepted":
		return RoleOK
	}
	return RoleMuted
}

// reqMarker is the state as the row's marker (below the STATE column's width).
func reqMarker(c reqCols, state string, notice bool) Seg {
	switch {
	case notice:
		return S(RoleMuted, GlyphNone+" ")
	case c.state > 0:
		return S(RoleText, "  ")
	case state == "unread":
		return S(RoleAttention, GlyphDotFull+" ")
	case state == "read":
		return S(RoleMuted, GlyphDotEmpty+" ")
	case state == "held quota":
		return S(RoleError, GlyphDotFull+" ")
	}
	return S(RoleText, "  ")
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

// requestRows are the view's list items.
func requestRows(v reqView, open reqOpen, now time.Time, width int) []ListItem {
	c := reqColumns(width)
	fit := func(l Line) Line { return FitLine(l, width) }
	head := func(text string) ListItem { return ListItem{Line: fit(Line{S(RoleDim, text)})} }
	sep := " " + GlyphNone + " "
	out := []ListItem{{Line: fit(c.header())}}
	threadRow := func(th reqThread, indent bool) ListItem {
		marker := reqMarker(c, th.State, th.Kind == kindReports || th.Kind == kindNotices)
		if indent {
			marker = S(RoleMuted, "  ")
		}
		return ListItem{Key: threadKey(th), Selectable: true,
			Line: fit(c.row(marker, th.From, th.Target, th.Kind, th.State, Line{S(RoleText, Preview(th.Subject))}, 1, th.At, now))}
	}
	groupRow := func(g reqGroup) []ListItem {
		if len(g.Threads) == 1 {
			return []ListItem{threadRow(g.Threads[0], false)}
		}
		subject := Line{S(RoleText, Preview(g.Threads[len(g.Threads)-1].Subject))}
		if g.Kind == kindChallenge {
			subject = Line{S(RoleText, Preview(g.Subjects()))}
		}
		key := g.Key()
		notice := g.Kind == kindReports || g.Kind == kindNotices
		if notice {
			key = "ntc:" + key
		}
		items := []ListItem{{Key: key, Selectable: true, Expanded: open.groups[key],
			Line: fit(c.row(reqMarker(c, g.State(), notice), g.From, g.Target, g.Kind, g.State(), subject, len(g.Threads), g.Oldest(), now))}}
		if open.groups[key] && !notice {
			for _, th := range g.Threads {
				items = append(items, threadRow(th, true))
			}
		}
		return items
	}

	owed := v.count(secOwed)
	out = append(out, head(fmt.Sprintf("owed by you%s%d", sep, owed)))
	if v.questions > 0 {
		word := "questions wait"
		if v.questions == 1 {
			word = "question waits"
		}
		out = append(out, ListItem{Key: "asks", Selectable: true, Line: fit(Line{S(RoleAttention, GlyphDotFull+" "),
			S(RoleAttention, fmt.Sprintf("%d open %s for you in For you (enter answers)", v.questions, word))})})
	}
	for _, g := range v.groups[secOwed] {
		out = append(out, groupRow(g)...)
	}

	if gs := v.groups[secWait]; len(gs) > 0 {
		var to []string
		oldest := gs[0].Oldest()
		for _, g := range gs {
			to = append(to, Preview(g.Target))
			if g.Oldest().Before(oldest) {
				oldest = g.Oldest()
			}
		}
		out = append(out, head(fmt.Sprintf("you wait on %s%s%d, no reply on their thread  (oldest %s)", strings.Join(to, ", "), sep,
			v.count(secWait), humanAge(now.Sub(oldest)))))
		for _, g := range gs {
			for i, th := range g.Threads {
				if i < reqWaitShown || open.groups[olderKey(g)] {
					out = append(out, threadRow(th, i >= reqWaitShown))
				}
			}
			if rest := g.Threads[min(reqWaitShown, len(g.Threads)):]; len(rest) > 0 {
				key := olderKey(g)
				older := reqGroup{Threads: rest}
				label := fmt.Sprintf("+ %d older  (enter expands)", len(rest))
				if open.groups[key] {
					label = fmt.Sprintf("%d older shown  (enter folds)", len(rest))
				}
				out = append(out, ListItem{Key: key, Selectable: true, Expanded: open.groups[key],
					Line: fit(c.row(S(RoleText, "  "), g.From, g.Target, g.Kind, older.State(), Line{S(RoleMuted, label)}, len(rest), older.Oldest(), now))})
			}
		}
	}

	if gs := v.groups[secTracked]; len(gs) > 0 {
		out = append(out, head(fmt.Sprintf("tracked requests%s%d waiting", sep, v.count(secTracked))))
		for _, g := range gs {
			out = append(out, groupRow(g)...)
		}
	}
	if gs := v.groups[secReviews]; len(gs) > 0 {
		reviewers := map[string]bool{}
		for _, g := range gs {
			reviewers[g.Target] = true
		}
		word := "reviewers"
		if len(reviewers) == 1 {
			word = "reviewer"
		}
		out = append(out, head(fmt.Sprintf("reviews still gated%s%d %s, %d open gates", sep, len(reviewers), word, v.count(secReviews))))
		for _, g := range gs {
			out = append(out, groupRow(g)...)
		}
	}
	if gs := v.groups[secDirectives]; len(gs) > 0 {
		out = append(out, head(fmt.Sprintf("directives between agents, no reply on the thread%s%d in %d pairs", sep, v.count(secDirectives), len(gs))))
		for _, g := range gs {
			out = append(out, groupRow(g)...)
		}
	}
	if gs := v.groups[secNotices]; len(gs) > 0 {
		out = append(out, head("notices"+sep+"folded, nobody owes an answer  (enter lists them)"))
		for _, g := range gs {
			out = append(out, groupRow(g)...)
		}
	}
	if len(v.folded) > 0 {
		label := fmt.Sprintf("answered or older than 24 h%s%d folded  (o shows them)", sep, len(v.folded))
		if open.folded {
			label = fmt.Sprintf("answered or older than 24 h%s%d  (o folds them)", sep, len(v.folded))
		}
		out = append(out, ListItem{Key: "fold", Selectable: true, Expanded: open.folded, Line: fit(Line{S(RoleDim, label)})})
		if open.folded {
			for _, th := range v.folded {
				out = append(out, threadRow(th, true))
			}
		}
	}
	return out
}

// requestsTitle is the panel's title count: the sums of its sections. The
// long labels shorten under 100 cells, never to bare numbers.
func requestsTitle(v reqView, width int) Line {
	you, wait, agents := v.titleCounts()
	sep := Seg{Text: " " + GlyphNone + " ", Role: RoleMuted}
	youRole := RoleMuted
	if you > 0 {
		youRole = RoleAttention
	}
	if width >= 100 {
		return Line{{Text: fmt.Sprintf("%d for you", you), Role: youRole}, sep,
			{Text: fmt.Sprintf("you wait on %d", wait), Role: RoleMuted}, sep,
			{Text: fmt.Sprintf("%d between agents", agents), Role: RoleMuted}}
	}
	var l Line
	for _, p := range []struct {
		word string
		n    int
		role Role
	}{{"you", you, youRole}, {"wait", wait, RoleMuted}, {"agents", agents, RoleMuted}} {
		if p.n == 0 {
			continue
		}
		if l != nil {
			l = append(l, sep)
		}
		l = append(l, Seg{Text: fmt.Sprintf("%s %d", p.word, p.n), Role: p.role})
	}
	if l == nil {
		return Line{{Text: "nothing waiting", Role: RoleMuted}}
	}
	return l
}

// requestInput is the view's facts from the snapshot: the retained cmd
// stream, the board, every open gate, each agent's delivery cursor and hold.
func (m *Model) requestInput() reqInput {
	s := m.snap
	in := reqInput{cmds: s.cmdHistory, reqs: s.requests, questions: openQuestionCount(s.asks), self: m.cfg.self, now: m.now,
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
	case key == "fold":
		o.folded = !o.folded
	case strings.HasPrefix(key, "req:"):
		return m.push(page{kind: ovRequest, key: key})
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
		for _, th := range ths {
			b.seg(RoleMuted, padLeft(humanAge(m.now.Sub(th.At)), 4)+"  ").text(Preview(th.Subject)).text("\n")
		}
		t := ths[0]
		return m.push(page{kind: ovNotices, title: fmt.Sprintf("%s %s %s %s %d %s", Preview(t.From), GlyphArrowR, Preview(t.Target), GlyphNone, len(ths), t.Kind),
			body: b.Lines(), fixed: true})
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
		return m.pushMessage(th.Root, th.At)
	}
	m.refresh()
	return nil
}
