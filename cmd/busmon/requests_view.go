package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Requests view (spec docs/superpowers/specs/2026-10-28-requests-view-design.md):
// who owes whom an answer, now. A thread is the unit (threadID); a row is a
// group of threads with the same section, sender, target and kind. Every
// fact comes from what busmon already reads: the retained cmd stream, the
// board's tracked requests, the open gates and each target's delivery cursor.

// reqSection is a section of the view, in display order.
type reqSection int

const (
	secOwed       reqSection = iota // owed by you
	secWait                         // you wait on
	secTracked                      // tracked requests between agents
	secReviews                      // reviews still gated
	secDirectives                   // directives between agents
	secNotices                      // notices: nobody owes an answer
	secFolded                       // answered, or older than reqFoldAfter
)

// reqFoldAfter: an unanswered thread older than this is folded (the person's
// answer, busmon pages: 7 days, was the 24 h horizon).
const reqFoldAfter = 7 * 24 * time.Hour

// Kinds, as the KIND column shows them.
const (
	kindRequest   = "request"
	kindChallenge = "challenge"
	kindDirective = "directive"
	kindReports   = "reports"
	kindNotices   = "notices"
)

// reqThread is one thread of the view.
type reqThread struct {
	ID       string // the thread id (its ref, else its root id)
	Root     bus.Event
	From     string
	Target   string
	Kind     string
	Subject  string // the first line without boilerplate, the task, the challenge's subject
	State    string // unread, read, accepted, blocked, held quota; "" unknown
	At       time.Time
	Section  reqSection
	Request  *request // a tracked request
	ReportID string   // a report notice's report
	Gated    bool     // a challenge whose gate is open now
}

// reqGroup is a row: threads of one section, sender, target and kind, in the
// section's order.
type reqGroup struct {
	Section            reqSection
	From, Target, Kind string
	Threads            []reqThread
}

// Key is the group's list key.
func (g reqGroup) Key() string {
	return fmt.Sprintf("grp:%d|%s|%s|%s", g.Section, g.From, g.Target, g.Kind)
}

// Oldest is the group's age: its oldest thread.
func (g reqGroup) Oldest() time.Time {
	t := g.Threads[0].At
	for _, th := range g.Threads[1:] {
		if th.At.Before(t) {
			t = th.At
		}
	}
	return t
}

// State is unread when any thread is, else the first thread's.
func (g reqGroup) State() string {
	for _, th := range g.Threads {
		if th.State == "unread" {
			return "unread"
		}
	}
	return g.Threads[0].State
}

// Subjects are a review group's subjects in order, a repeat counted (`a ×3`).
func (g reqGroup) Subjects() string {
	var order []string
	n := map[string]int{}
	for _, th := range g.Threads {
		if n[th.Subject] == 0 {
			order = append(order, th.Subject)
		}
		n[th.Subject]++
	}
	parts := make([]string, len(order))
	for i, s := range order {
		parts[i] = s
		if n[s] > 1 {
			parts[i] = fmt.Sprintf("%s %s%d", s, GlyphTimes, n[s])
		}
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

// reqDelivery is a target's cursor: entries above lastDelivered, or in its
// pending list, are unread.
type reqDelivery struct {
	lastDelivered string
	pending       map[string]bool
}

// reqInput is everything the view is computed from.
type reqInput struct {
	cmds      []bus.Event                  // the retained cmd stream, oldest first
	reqs      []request                    // the board's requests
	gates     map[string]map[string]string // target -> ref -> meta, open now
	delivery  map[string]reqDelivery       // target -> its cursor, when read
	held      map[string]bool              // target -> its provider is held
	questions int                          // open questions for the person (they stay in For you)
	asks      []reqAsk                     // the open asks, one row each in owed by you
	self      string
	now       time.Time
}

// reqAsk is an open ask as the view lists it.
type reqAsk struct {
	ID, From, Title string
	Open            int // its open questions
	At              time.Time
}

// reqView is the computed view: the groups of each section and the folded threads.
type reqView struct {
	groups    map[reqSection][]reqGroup
	folded    []reqThread
	questions int
	asks      []reqAsk
	self      string
}

// count is the number of threads of a section (the questions link counts in owed).
func (v reqView) count(s reqSection) int {
	n := 0
	for _, g := range v.groups[s] {
		n += len(g.Threads)
	}
	if s == secOwed {
		n += v.questions
	}
	return n
}

// titleCounts: for you (section 1), you wait on (2), between agents (3+4+5).
func (v reqView) titleCounts() (forYou, wait, agents int) {
	return v.count(secOwed), v.count(secWait), v.count(secTracked) + v.count(secReviews) + v.count(secDirectives)
}

// owedOldest is the age of the oldest thread owed by the person (the open
// questions have none): zero when none is dated.
func (v reqView) owedOldest(now time.Time) time.Duration {
	var oldest time.Duration
	for _, g := range v.groups[secOwed] {
		if t := g.Oldest(); !t.IsZero() && now.Sub(t) > oldest {
			oldest = now.Sub(t)
		}
	}
	return oldest
}

// owedText is the header's orange item (busmon-req-header): the view's own
// count of what the person owes, with its oldest; "" when nothing is owed.
func owedText(v reqView, now time.Time) string {
	n, _, _ := v.titleCounts()
	if n == 0 {
		return ""
	}
	word := "requests"
	if n == 1 {
		word = "request"
	}
	text := fmt.Sprintf("%d %s waiting", n, word)
	if oldest := v.owedOldest(now); oldest > 0 {
		text += " " + GlyphNone + " oldest " + humanAge(oldest)
	}
	return text
}

// reportPrefix is what `report --to` writes before the report's first line.
func reportPrefix(id string) string {
	return "Report " + id + " for you (full text: agentbus reports " + id + "): "
}

// reportSubject is a report notice's own first line and its report id; ok
// only for the exact prefix reportNotice writes.
func reportSubject(msg string) (subject, id string, ok bool) {
	rest, found := strings.CutPrefix(msg, "Report ")
	if !found {
		return "", "", false
	}
	id, _, found = strings.Cut(rest, " ")
	if !found || id == "" || !strings.HasPrefix(msg, reportPrefix(id)) {
		return "", "", false
	}
	return strings.TrimPrefix(msg, reportPrefix(id)), id, true
}

// requestGroups computes the view.
func requestGroups(in reqInput) reqView {
	v := reqView{groups: map[reqSection][]reqGroup{}, questions: in.questions, asks: in.asks, self: in.self}
	horizon := in.now.Add(-reqFoldAfter)
	known := map[string]bool{}
	var threads []reqThread
	for i := range in.reqs {
		r := in.reqs[i]
		if r.Thread != "" {
			known[r.Thread] = true
		}
		if r.ID != "" {
			known[r.ID] = true
		}
		if !r.Tracked {
			continue
		}
		at := r.Created
		if at.IsZero() {
			at = r.Updated
		}
		th := reqThread{ID: "req:" + r.Task, From: r.From, Target: target(r), Kind: kindRequest, Subject: r.Task,
			State: r.State, At: at, Request: &r, Section: secTracked}
		switch {
		case !r.waiting(): // done or superseded: answered, folded with its own history
			th.Section = secFolded
			if !r.Updated.IsZero() {
				th.At = r.Updated
			}
		case th.Target == in.self:
			th.Section = secOwed
		case th.From == in.self:
			th.Section = secWait
		}
		threads = append(threads, th)
	}

	roots := map[string]bus.Event{}
	var order []string
	answered := map[string]bool{}
	for _, e := range in.cmds {
		if e.Kind != "cmd" {
			continue
		}
		id := threadID(e)
		if known[id] || known[e.ID] {
			continue
		}
		switch e.Type {
		case bus.CmdDirective, bus.CmdChallenge:
			if _, seen := roots[id]; !seen {
				roots[id] = e
				order = append(order, id)
			}
		case bus.CmdReply, bus.CmdVerdict:
			answered[id] = true
		}
	}
	for _, id := range order {
		e := roots[id]
		th := reqThread{ID: id, Root: e, From: e.From, Target: e.Target, At: entryTime(e.ID),
			Subject: oneLine(messageBody(e)), State: in.state(e)}
		switch {
		case e.Type == bus.CmdChallenge:
			th.Kind = kindChallenge
			if meta, ok := in.gates[e.Target][id]; ok {
				th.Gated = true
				if g := bus.ParseGateMeta(meta); g.Subject != "" {
					th.Subject = g.Subject
				}
			}
			if !th.Gated || th.Subject == oneLine(messageBody(e)) { // no stored subject: the challenge's first word
				if f := strings.Fields(th.Subject); len(f) > 0 {
					th.Subject = f[0]
				}
			}
		default:
			if s, rid, ok := reportSubject(e.Message); ok {
				th.Kind, th.Subject, th.ReportID = kindReports, oneLine(s), rid
			} else if e.From == "dispatch" || e.NoWake {
				th.Kind = kindNotices
			} else {
				th.Kind = kindDirective
			}
		}
		th.Section = in.section(th, answered[id], th.At.Before(horizon))
		threads = append(threads, th)
	}

	for _, th := range threads {
		if th.Section == secFolded {
			v.folded = append(v.folded, th)
		}
	}
	sort.SliceStable(v.folded, func(i, j int) bool { return v.folded[i].At.After(v.folded[j].At) })
	index := map[string]int{}
	for _, th := range threads {
		if th.Section == secFolded {
			continue
		}
		g := reqGroup{Section: th.Section, From: th.From, Target: th.Target, Kind: th.Kind}
		k := g.Key()
		i, ok := index[k]
		if !ok {
			i = len(v.groups[th.Section])
			index[k] = i
			v.groups[th.Section] = append(v.groups[th.Section], g)
		}
		v.groups[th.Section][i].Threads = append(v.groups[th.Section][i].Threads, th)
	}
	for s, gs := range v.groups {
		newestFirst := s == secWait
		for i := range gs {
			ts := gs[i].Threads
			sort.SliceStable(ts, func(a, b int) bool {
				if newestFirst {
					return ts[a].At.After(ts[b].At)
				}
				return ts[a].At.Before(ts[b].At)
			})
		}
		sort.SliceStable(gs, func(a, b int) bool {
			ua, ub := gs[a].State() == "unread", gs[b].State() == "unread"
			if ua != ub {
				return ua
			}
			if newestFirst {
				return gs[a].Threads[0].At.After(gs[b].Threads[0].At)
			}
			return gs[a].Oldest().Before(gs[b].Oldest())
		})
	}
	return v
}

// section places a cmd thread. A challenge is owed iff its gate is open now,
// whatever its age or its thread says; everything else answered or older
// than the horizon is folded.
func (in reqInput) section(th reqThread, answered, old bool) reqSection {
	if th.Kind == kindChallenge {
		switch {
		case !th.Gated:
			return secFolded
		case th.Target == in.self:
			return secOwed
		case th.From == in.self:
			return secWait
		}
		return secReviews
	}
	if answered || old {
		return secFolded
	}
	switch {
	case th.Kind == kindReports || th.Kind == kindNotices:
		return secNotices
	case th.Target == in.self:
		return secOwed
	case th.From == in.self:
		return secWait
	}
	return secDirectives
}

// state is a cmd entry's delivery to its target: held quota first (it
// explains the silence), then unread/read from the target's cursor; ""
// when the cursor was not read.
func (in reqInput) state(e bus.Event) string {
	if in.held[e.Target] {
		return "held quota"
	}
	d, ok := in.delivery[e.Target]
	if !ok {
		return ""
	}
	if d.pending[e.ID] || d.lastDelivered == "" || idAfter(e.ID, d.lastDelivered) {
		return "unread"
	}
	return "read"
}

// idAfter reports stream id a newer than b.
func idAfter(a, b string) bool {
	am, as := splitStreamID(a)
	bm, bs := splitStreamID(b)
	return am > bm || am == bm && as > bs
}

func splitStreamID(id string) (ms, seq int64) {
	m, s, _ := strings.Cut(id, "-")
	fmt.Sscan(m, &ms)
	fmt.Sscan(s, &seq)
	return ms, seq
}

// reqSectionNote is the sentence a section's heading used to carry: the first
// line of a thread's page now (busmon pages, Requests).
func reqSectionNote(s reqSection) string {
	switch s {
	case secOwed:
		return "Owed by you: the sender waits for your answer on this thread."
	case secWait:
		return "You wait on it: no reply on its thread yet."
	case secTracked:
		return "Tracked: the board records its acceptance and completion."
	case secReviews:
		return "Review still gated: the reviewer's gate stays open until a verdict."
	case secDirectives:
		return "Between agents: no reply on the thread yet. Untracked: the protocol records no acceptance for it."
	case secNotices:
		return "Notices: nobody owes an answer."
	}
	return "Answered, or older than 7 days."
}
