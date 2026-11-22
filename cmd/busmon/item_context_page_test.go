package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-dedupe step 3 on the pages (spec §5; master's answers): an item's
// page ends with a Context section, its lines numbered 1-3; a digit or a
// click opens one in full, Enter keeps its meaning. On the ask page the
// digits choose an option, so a click opens them.

func ctxShell(t *testing.T, st *shared, evs ...bus.Event) Model {
	t.Helper()
	m := newShell(shellConfig{project: "demo", self: "bob", st: st, cache: &busmonCache{Project: "demo"}})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	return step(t, m, tailMsg{events: evs})
}

func openItem(t *testing.T, m Model, key string) Model {
	t.Helper()
	i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Key == key })
	if i < 0 {
		t.Fatalf("no item %s in %v", key, itemKeys(m.forYou))
	}
	return run(t, m, m.openForYouItem(m.forYou[i]))
}

// hasContext: the page has the section's heading line (an agent's page has
// a "Context:" field of its own, the context fill).
func hasContext(p *page) bool {
	return slices.ContainsFunc(p.body, func(l Line) bool { return plainOf(l) == "Context" })
}

func mergeShared() *shared {
	s := targetSnap()
	return &shared{agents: map[string]*agentState{"master": {registered: true, state: "idle"}}, person: "bob", mergeQ: s.mergeQ, mergeQRead: true}
}

func TestContextOnTheMergePage(t *testing.T) {
	m := ctxShell(t, mergeShared(),
		needsRep(0, "master", bus.NeedsAnswer, "Merge #694 first?"), needsNotice(1, 0, bus.NeedsAnswer),
		repEv(2, "master", "#694 a"), repEv(4, "master", "#694 b"), repEv(6, "master", "#694 c"))
	m = openItem(t, m, "merge:pr:694")
	p := m.rd.top()
	if p == nil || p.kind != ovMerge {
		t.Fatalf("page %+v", p)
	}
	body := plainLines(p.body)
	if !hasContext(p) {
		t.Fatalf("no section:\n%s", body)
	}
	for _, want := range []string{"1  master", "Merge #694 first?", "needs your answer", "2  master", "#694 c", "3  master", "#694 b", "1 older in Activity"} {
		if !strings.Contains(body, want) {
			t.Fatalf("no %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "#694 a") {
		t.Fatalf("a fourth line shown:\n%s", body)
	}
	if !m.pageOffers(ActContext) {
		t.Fatal("the page does not offer 1-3")
	}
	// 2 opens the second line in full; esc comes back.
	m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: '2', Text: "2"})))
	if q := m.rd.top(); q.kind != ovMessage || q.ev.ID != rid(6) {
		t.Fatalf("2 opened %+v", q)
	}
	m = keys(t, m, kEsc)
	if q := m.rd.top(); q.kind != ovMerge {
		t.Fatalf("esc: %+v", q)
	}
	// A click on the third line opens it too.
	line := slices.IndexFunc(p.body, func(l Line) bool { return strings.HasPrefix(plainOf(l), "3  ") })
	r := m.readerRect()
	m = run(t, m, m.readerClick(r.X+3, r.Y+1+line-p.top))
	if q := m.rd.top(); q.kind != ovMessage || q.ev.ID != rid(4) {
		t.Fatalf("click opened %+v", q)
	}
}

// A page with no attached message has no section, and offers no digit.
func TestContextNoneNoSection(t *testing.T) {
	m := ctxShell(t, mergeShared(), repEv(0, "master", "about #695"),
		needsRep(2, "master", bus.NeedsRead, "Release notes"), needsNotice(3, 2, bus.NeedsRead),
		bus.Event{Kind: "cmd", ID: rid(4), From: "master", Target: "bob", Type: bus.CmdDirective, Ref: rid(2), Message: "page 3 changed"})
	m = openItem(t, m, "merge:pr:694")
	if body := plainLines(m.rd.top().body); hasContext(m.rd.top()) || m.pageOffers(ActContext) {
		t.Fatalf("an empty context:\n%s", body)
	}
	// An agent's page that is no wait item's (master's report is an item of
	// its own, with a reply): no section either.
	m = run(t, m, m.pushAgent("master"))
	if body := plainLines(m.rd.top().body); hasContext(m.rd.top()) {
		t.Fatalf("a plain agent page:\n%s", body)
	}
}

// The wait page: the agent's newest report.
func TestContextOnTheWaitPage(t *testing.T) {
	st := &shared{agents: map[string]*agentState{"coder": {registered: true, state: "waiting", stateAt: entryTime(rid(-1))}}, person: "bob"}
	m := ctxShell(t, st, repEv(0, "coder", "older"), repEv(2, "coder", "blocked on the base"))
	i := slices.IndexFunc(m.forYou, func(it attentionItem) bool { return it.Rank == rankWait })
	if i < 0 {
		t.Fatalf("no wait: %v", itemKeys(m.forYou))
	}
	m = run(t, m, m.openForYouItem(m.forYou[i]))
	body := plainLines(m.rd.top().body)
	if !hasContext(m.rd.top()) || !strings.Contains(body, "blocked on the base") {
		t.Fatalf("wait page:\n%s", body)
	}
}

// The gate's page (its challenge's message page): the replies on it.
func TestContextOnTheGatePage(t *testing.T) {
	st := mergeShared()
	gateRef := rid(-9)
	st.personGates = map[string]string{gateRef: "master|sign off the release"}
	m := ctxShell(t, st, bus.Event{Kind: "cmd", ID: rid(0), From: "master", Target: "bob", Type: bus.CmdDirective, Ref: gateRef, Message: "the notes are attached"})
	m = openItem(t, m, "gate:bob:"+gateRef)
	if body := plainLines(m.rd.top().body); !hasContext(m.rd.top()) || !strings.Contains(body, "the notes are attached") {
		t.Fatalf("gate page:\n%s", body)
	}
	m = run(t, m, m.update(tea.KeyPressMsg(tea.Key{Code: '1', Text: "1"})))
	if q := m.rd.top(); q.kind != ovMessage || q.ev.ID != rid(0) {
		t.Fatalf("1 opened %+v", q)
	}
}

// The ask page: shown, opened by a click; its digits still choose options.
func TestContextOnTheAskPage(t *testing.T) {
	st := mergeShared()
	st.asks = []bus.Ask{{ID: rid(-5), From: "master", Title: "Language", CreatedAt: entryTime(rid(-5)).UnixMilli(), State: "open",
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Header: "L", Question: "which?", Options: []bus.AskOption{{Label: "fr"}, {Label: "en"}}}}}}}
	m := ctxShell(t, st, bus.Event{Kind: "cmd", ID: rid(0), From: "master", Target: "bob", Type: bus.CmdDirective, Ref: rid(-5), Message: "the options explained"})
	m = openItem(t, m, "ask:"+rid(-5))
	p := m.rd.top()
	if p.kind != ovAsk || !strings.Contains(plainLines(p.body), "the options explained") {
		t.Fatalf("ask page %+v\n%s", p.kind, plainLines(p.body))
	}
	line := slices.IndexFunc(p.body, func(l Line) bool {
		return strings.HasPrefix(plainOf(l), "1  ") && strings.Contains(plainOf(l), "explained")
	})
	r := m.readerRect()
	m = run(t, m, m.readerClick(r.X+3, r.Y+1+line-p.top))
	if q := m.rd.top(); q.kind != ovMessage || q.ev.ID != rid(0) {
		t.Fatalf("click opened %+v", q)
	}
}
