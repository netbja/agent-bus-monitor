package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reqFixture is today's shape (the spec's numbers, 2026-10-28): 22 messages
// from the person to master, 13 challenges to reviewer-codex of which 9 gates
// are still open, 10 report notices, 12 dispatcher notices, 11 directives
// between agents in 5 pairs, one tracked request, and answered threads.
func reqFixture(now time.Time) reqInput {
	var cmds []bus.Event
	seq := 0
	at := func(ago time.Duration) string {
		seq++
		return fmt.Sprintf("%d-%d", now.Add(-ago).UnixMilli(), seq)
	}
	add := func(e bus.Event) bus.Event {
		e.Kind = "cmd"
		cmds = append(cmds, e)
		return e
	}
	for i := 0; i < 22; i++ {
		add(bus.Event{ID: at(time.Duration(22-i) * 5 * time.Minute), Type: bus.CmdDirective, From: "charlie", Target: "master", Human: true,
			Message: fmt.Sprintf("message %d from the person", i)})
	}
	gates := map[string]map[string]string{"reviewer-codex": {}}
	subjects := []string{"lifecycle-spec", "gate-subject-close", "a-pending-pane", "a-pending-pane", "a-pending-pane", "a-identity", "a-health", "a-nav", "a-nav",
		"tokens-spec", "installer-spec", "onboarding-spec", "requests-view-spec"}
	for i, s := range subjects {
		ref := fmt.Sprintf("ref%02d", i)
		add(bus.Event{ID: at(time.Duration(60-i) * time.Minute), Type: bus.CmdChallenge, From: "coder", Target: "reviewer-codex", Ref: ref,
			Message: s + " delta for review"})
		if i < 9 {
			gates["reviewer-codex"][ref] = bus.FormatGateMeta("coder", s, s+" delta for review")
		}
	}
	for i := 0; i < 10; i++ {
		id := at(time.Duration(10-i) * time.Minute)
		add(bus.Event{ID: id, Type: bus.CmdDirective, From: "coder", Target: "master",
			Message: reportPrefix("1793"+fmt.Sprint(i)) + fmt.Sprintf("report %d first line", i)})
	}
	for _, n := range []struct {
		to string
		n  int
	}{{"sentinel", 6}, {"master", 5}, {"coder", 1}} {
		for i := 0; i < n.n; i++ {
			add(bus.Event{ID: at(time.Duration(40+i) * time.Minute), Type: bus.CmdDirective, From: "dispatch", Target: n.to, Message: "dispatcher notice for " + n.to})
		}
	}
	pairs := []struct {
		from, to string
		n        int
		ago      time.Duration
	}{{"master", "sentinel", 2, 14 * time.Hour}, {"coder", "ui-kimi", 1, 12 * time.Hour}, {"master", "foureyes", 1, 12 * time.Hour},
		{"master", "coder", 1, 11 * time.Hour}, {"sentinel", "master", 6, 48 * time.Minute}}
	for _, p := range pairs {
		for i := 0; i < p.n; i++ {
			add(bus.Event{ID: at(p.ago - time.Duration(i)*time.Minute), Type: bus.CmdDirective, From: p.from, Target: p.to, Message: p.from + " to " + p.to})
		}
	}
	// Answered threads (a reply on their thread) and threads older than 7 days.
	for i := 0; i < 70; i++ {
		root := add(bus.Event{ID: at(time.Duration(3+i) * time.Hour / 4), Type: bus.CmdDirective, From: "master", Target: "coder", Message: "answered"})
		add(bus.Event{ID: at(time.Duration(2+i) * time.Hour / 4), Type: bus.CmdReply, From: "coder", Target: "master", Ref: root.ID, Message: "done"})
	}
	for i := 0; i < 3; i++ {
		add(bus.Event{ID: at(8*24*time.Hour + time.Duration(i)*time.Hour), Type: bus.CmdDirective, From: "master", Target: "foureyes", Message: "old"})
	}
	reqs := []request{{Task: "busmon-group-a", Owner: "coder", State: "accepted", Tracked: true, ID: "req1", Thread: "req1", From: "master",
		Target: "coder", Created: now.Add(-66 * time.Minute)}}
	delivered := fmt.Sprintf("%d-9999", now.UnixMilli()) // every entry delivered
	return reqInput{cmds: cmds, reqs: reqs, gates: gates, self: "charlie", now: now,
		delivery: map[string]reqDelivery{"master": {lastDelivered: delivered}, "sentinel": {lastDelivered: delivered},
			"foureyes": {lastDelivered: delivered}, "coder": {lastDelivered: delivered}, "ui-kimi": {lastDelivered: "1-0"}},
		held: map[string]bool{"reviewer-codex": true}}
}

func groupsOf(v reqView, s reqSection) []string {
	var out []string
	for _, g := range v.groups[s] {
		out = append(out, fmt.Sprintf("%s>%s %s %d", g.From, g.Target, g.Kind, len(g.Threads)))
	}
	return out
}

// Sections, kinds and groups on today's shape; the title counts are the sums
// of their sections (0 · 22 · 21 = 1 + 9 + 11), notices and folded never counted.
func TestRequestGroups(t *testing.T) {
	v := requestGroups(reqFixture(shellNow))
	for _, tc := range []struct {
		s    reqSection
		want string
	}{
		{secOwed, "[]"},
		{secWait, "[charlie>master directive 22]"},
		{secTracked, "[master>coder request 1]"},
		{secReviews, "[coder>reviewer-codex challenge 9]"},
		{secDirectives, "[coder>ui-kimi directive 1 master>sentinel directive 2 master>foureyes directive 1 master>coder directive 1 sentinel>master directive 6]"},
	} {
		if got := fmt.Sprint(groupsOf(v, tc.s)); got != tc.want {
			t.Errorf("section %d: %s, want %s", tc.s, got, tc.want)
		}
	}
	notices := strings.Join(groupsOf(v, secNotices), ",")
	for _, want := range []string{"coder>master reports 10", "dispatch>sentinel notices 6", "dispatch>master notices 5", "dispatch>coder notices 1"} {
		if !strings.Contains(notices, want) {
			t.Errorf("notices lack %q: %s", want, notices)
		}
	}
	if len(v.folded) != 70+3+4 { // answered, older than 7 days, challenges whose gate closed
		t.Errorf("folded %d, want 77", len(v.folded))
	}
	you, wait, agents := v.titleCounts()
	if you != 0 || wait != 22 || agents != 21 {
		t.Errorf("title %d · %d · %d, want 0 · 22 · 21", you, wait, agents)
	}
	// Each title number is the sum of its sections' rows, on any fixture.
	for _, in := range []reqInput{reqFixture(shellNow), {now: shellNow, self: "charlie"}, func() reqInput {
		in := reqFixture(shellNow)
		in.self, in.questions = "master", 2
		return in
	}()} {
		v := requestGroups(in)
		you, wait, agents := v.titleCounts()
		sum := func(ss ...reqSection) int {
			n := 0
			for _, s := range ss {
				for _, g := range v.groups[s] {
					n += len(g.Threads)
				}
			}
			return n
		}
		if you != sum(secOwed)+in.questions || wait != sum(secWait) || agents != sum(secTracked, secReviews, secDirectives) {
			t.Errorf("self %s: title %d·%d·%d is not its sections' sums", in.self, you, wait, agents)
		}
	}
}

// Order: unread before read, then oldest first; "you wait on" newest first.
// State: held quota, then the target's cursor.
func TestRequestGroupsOrderAndState(t *testing.T) {
	v := requestGroups(reqFixture(shellNow))
	d := v.groups[secDirectives]
	if d[0].Target != "ui-kimi" || d[0].State() != "unread" {
		t.Errorf("first directive group %s>%s %s, want the unread one first", d[0].From, d[0].Target, d[0].State())
	}
	for i := 2; i < len(d); i++ {
		if d[i].Oldest().Before(d[i-1].Oldest()) {
			t.Errorf("directives not oldest first at %d", i)
		}
	}
	w := v.groups[secWait][0].Threads
	if !w[0].At.After(w[1].At) || w[0].State != "read" {
		t.Errorf("you wait on: first %v %s, want newest first and read", w[0].At, w[0].State)
	}
	if r := v.groups[secReviews][0]; r.State() != "held quota" {
		t.Errorf("review state %q, want held quota", r.State())
	}
	if got := v.groups[secReviews][0].Subjects(); got != "lifecycle-spec "+GlyphNone+" gate-subject-close "+GlyphNone+" a-pending-pane "+GlyphTimes+"3 "+GlyphNone+" a-identity "+GlyphNone+" a-health "+GlyphNone+" a-nav "+GlyphTimes+"2" {
		t.Errorf("review subjects %q", got)
	}
}

// A review is owed while its gate is open, whatever its thread says: a gate
// closed by gate close (a wake=no reply) or by a verdict under another ref
// folds it, and a verdict on the thread with the gate still open does not.
func TestRequestReviewsFollowTheGate(t *testing.T) {
	ch := bus.Event{Kind: "cmd", ID: "100-0", Type: bus.CmdChallenge, From: "coder", Target: "foureyes", Ref: "r1", Message: "x"}
	reply := bus.Event{Kind: "cmd", ID: "101-0", Type: bus.CmdReply, From: "coder", Target: "foureyes", Ref: "r1", NoWake: true, Message: "gate closed"}
	now := time.UnixMilli(200)
	open := map[string]map[string]string{"foureyes": {"r1": "coder|x"}}
	if v := requestGroups(reqInput{cmds: []bus.Event{ch, reply}, gates: open, now: now}); v.count(secReviews) != 1 {
		t.Error("an open gate with a reply on its thread left the reviews")
	}
	if v := requestGroups(reqInput{cmds: []bus.Event{ch}, gates: nil, now: now}); v.count(secReviews) != 0 || len(v.folded) != 1 {
		t.Error("a closed gate with no reply stayed owed")
	}
}

// Only reportNotice's exact prefix is a report notice.
func TestReportSubject(t *testing.T) {
	s, id, ok := reportSubject(reportPrefix("1793-0") + "a): b")
	if !ok || id != "1793-0" || s != "a): b" {
		t.Errorf("exact prefix: %q %q %v", s, id, ok)
	}
	for _, m := range []string{"Report 1793-0 for you: x", "Report 1793-0 for you (full text: agentbus reports 1794-0): x",
		"report 1793-0 for you (full text: agentbus reports 1793-0): x", "Report  for you (full text: agentbus reports ): x", "Reports are in"} {
		if _, _, ok := reportSubject(m); ok {
			t.Errorf("%q read as a report notice", m)
		}
	}
	in := reqInput{now: time.UnixMilli(1000), cmds: []bus.Event{{Kind: "cmd", ID: "900-0", Type: bus.CmdDirective, From: "coder", Target: "master",
		Message: "Report 1 for you: looks like one"}}}
	if v := requestGroups(in); v.count(secDirectives) != 1 {
		t.Error("a look-alike notice was not a directive")
	}
}

// A review subject is the gate's stored subject, else the challenge's first word.
func TestRequestReviewSubjectFallsBackToTheFirstWord(t *testing.T) {
	ch := bus.Event{Kind: "cmd", ID: "100-0", Type: bus.CmdChallenge, From: "coder", Target: "foureyes", Ref: "r1", Message: "a-nav delta 5182b41 for review"}
	in := reqInput{cmds: []bus.Event{ch}, gates: map[string]map[string]string{"foureyes": {"r1": "coder|a-nav delta 5182b41 for review"}}, now: time.UnixMilli(200)}
	if got := requestGroups(in).groups[secReviews][0].Threads[0].Subject; got != "a-nav" {
		t.Errorf("subject %q, want the first word", got)
	}
	in.gates["foureyes"]["r1"] = bus.FormatGateMeta("coder", "busmon-a-nav", "a-nav delta 5182b41 for review")
	if got := requestGroups(in).groups[secReviews][0].Threads[0].Subject; got != "busmon-a-nav" {
		t.Errorf("subject %q, want the stored one", got)
	}
}

// Two "you wait on" groups to one recipient (directives and challenges):
// each "+ N older" row is its own, and resolves to its own threads.
func TestRequestWaitOlderRowsAreTheirGroups(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	var cmds []bus.Event
	gates := map[string]map[string]string{"master": {}}
	for i := 0; i < 4; i++ {
		cmds = append(cmds, bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-0", 900_000+i), Type: bus.CmdDirective, From: "charlie", Target: "master", Message: "d"})
		ref := fmt.Sprintf("c%d", i)
		cmds = append(cmds, bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-1", 900_000+i), Type: bus.CmdChallenge, From: "charlie", Target: "master", Ref: ref, Message: "c"})
		gates["master"][ref] = "charlie|c"
	}
	v := requestGroups(reqInput{cmds: cmds, gates: gates, self: "charlie", now: now})
	var older []string
	for _, it := range requestRows(v, reqOpen{}, now, 120) {
		if strings.HasPrefix(it.Key, "older:") {
			older = append(older, it.Key)
		}
	}
	if len(older) != 2 || older[0] == older[1] {
		t.Fatalf("older rows %v, want two distinct", older)
	}
	open := reqOpen{groups: map[string]bool{older[0]: true}}
	n := 0
	for _, it := range requestRows(v, open, now, 120) {
		if it.Selectable && strings.HasPrefix(it.Key, "thr:") {
			n++
		}
	}
	if n != 2+2+2 { // both groups' 2 shown, plus the opened group's 2 older
		t.Errorf("opening one older row shows %d thread rows, want 6", n)
	}
	for _, k := range older {
		ths := olderThreads(v, k)
		if len(ths) != 2 {
			t.Fatalf("%s resolves to %d threads", k, len(ths))
		}
		if want := map[string]string{"directive": kindDirective, "challenge": kindChallenge}; ths[0].Kind != ths[1].Kind {
			t.Errorf("%s mixes kinds %v", k, want)
		}
	}
	if olderThreads(v, older[0])[0].Kind == olderThreads(v, older[1])[0].Kind {
		t.Error("both older rows resolve to the same group")
	}
}

// A tracked request that is done or superseded is folded as answered, never lost.
func TestRequestDoneTrackedRequestIsFolded(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	root := bus.Event{Kind: "cmd", ID: "990000-0", Type: bus.CmdDirective, From: "master", Target: "coder", Message: "do x"}
	r := request{Task: "x", Tracked: true, ID: root.ID, Thread: root.ID, From: "master", Target: "coder", State: "done", Updated: now.Add(-time.Minute)}
	v := requestGroups(reqInput{cmds: []bus.Event{root}, reqs: []request{r}, now: now})
	if len(v.folded) != 1 || v.folded[0].Request == nil || v.folded[0].Subject != "x" {
		t.Fatalf("folded %+v, want the done request", v.folded)
	}
	if v.count(secTracked) != 0 {
		t.Error("a done request still waits")
	}
}

// Unanswered threads fold after 7 days, no longer after 24 h (the person's
// answer, busmon pages): a 3-day-old directive between agents is still listed.
func TestRequestUnansweredFoldsAfterSevenDays(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	in := reqInput{self: "charlie", now: now, cmds: []bus.Event{
		{Kind: "cmd", ID: fmt.Sprintf("%d-1", now.Add(-3*24*time.Hour).UnixMilli()), Type: bus.CmdDirective, From: "master", Target: "coder", Message: "three days"},
		{Kind: "cmd", ID: fmt.Sprintf("%d-1", now.Add(-8*24*time.Hour).UnixMilli()), Type: bus.CmdDirective, From: "master", Target: "foureyes", Message: "eight days"},
	}}
	v := requestGroups(in)
	if got := groupsOf(v, secDirectives); len(got) != 1 || got[0] != "master>coder directive 1" {
		t.Errorf("directives: %v", got)
	}
	if len(v.folded) != 1 || v.folded[0].Subject != "eight days" {
		t.Errorf("folded: %+v", v.folded)
	}
}
