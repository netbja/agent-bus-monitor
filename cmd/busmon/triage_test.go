package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmon-queue-triage §4: the triage walk.

var kT = 'T'

// walkKinds is the page the walk opens by rank (§4.1's table).
var walkKinds = map[int]overlayKind{rankAsk: ovAsk, rankHealth: ovHealth, rankWait: ovAgent, rankGate: ovMessage,
	rankMerge: ovMerge, rankUnread: ovMessage}

func topKind(t *testing.T, m Model) overlayKind {
	t.Helper()
	if !m.rd.open() {
		t.Fatal("no page open")
	}
	return m.rd.top().kind
}

func TestTriageWalksEveryRankInOrder(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		m := rowsFixture(t, unknown, false)
		var want []attentionItem
		for _, it := range m.forYou {
			if it.Key != mergeUnknownKey {
				want = append(want, it)
			}
		}
		m = keys(t, m, kT)
		for i, it := range want {
			if !m.walkPage() || topKind(t, m) != walkKinds[it.Rank] {
				t.Fatalf("unknown=%v item %d (%s): page %v, want %v", unknown, i, it.Key, m.rd.top().kind, walkKinds[it.Rank])
			}
			if got, title := m.grid().plain(), m.walkTitle(); !strings.Contains(got, title) || title != "triage "+itoa(i+1)+"/"+itoa(len(want)) {
				t.Fatalf("item %d: title %q not %d/%d on screen", i, title, i+1, len(want))
			}
			switch it.Rank {
			case rankUnread: // its message page, read, not the expansion
				id := strings.TrimPrefix(it.Key, "unread:")
				if m.rd.top().ev.ID != id || !m.feed.unread.read[id] || m.ui.expanded != "" {
					t.Fatalf("unread: page %q read=%v expanded %q", m.rd.top().ev.ID, m.feed.unread.read[id], m.ui.expanded)
				}
			case rankAsk:
				if m.rd.top().ask.id != it.AskID {
					t.Fatalf("ask page %q, want %q", m.rd.top().ask.id, it.AskID)
				}
			case rankWait:
				if m.rd.top().key != it.Agent {
					t.Fatalf("agent page %q, want %q", m.rd.top().key, it.Agent)
				}
			}
			m = keys(t, m, 'n')
		}
		if topKind(t, m) != ovTriageEnd {
			t.Fatalf("after the last item: %v, want the end page", m.rd.top().kind)
		}
		end := m.grid().plain()
		if !strings.Contains(end, "Triage done: everything was seen once") || strings.Contains(end, "Nothing waits") {
			t.Fatalf("end title:\n%s", end)
		}
		if strings.Contains(end, "merge queue unreadable") != unknown {
			t.Fatalf("unknown=%v: the end page and merge:unknown:\n%s", unknown, end)
		}
		if !strings.Contains(end, "read 1") || !strings.Contains(end, "skipped "+itoa(len(want)-1)) {
			t.Fatalf("counts:\n%s", end)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func walkTo(t *testing.T, m Model, ok func(attentionItem) bool) (Model, attentionItem) {
	t.Helper()
	m = keys(t, m, kT)
	for i := 0; i < 20; i++ {
		if it, found := m.walkItem(); found && ok(it) {
			return m, it
		}
		m = keys(t, m, 'n')
	}
	t.Fatal("no such item in the walk")
	return m, attentionItem{}
}

func TestTriageAnswerAdvances(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, it := walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankAsk })
	a, ok := findAsk(m.snap.asks, it.AskID)
	if !ok {
		t.Fatal("no ask")
	}
	q := a.Questions[0]
	m = step(t, m, answerMsg{ask: a, qid: q.ID, header: q.Header, choice: "yes", left: 0, key: a.ID + "|" + q.ID})
	if it2, ok := m.walkItem(); !ok || it2.Rank != rankHealth || topKind(t, m) != ovHealth {
		t.Fatalf("after the last answer: %v, want the next item (health)", m.rd.top().kind)
	}
	if m.ui.walk.outcome[it.Key] != "answered" || !strings.HasPrefix(m.ui.flash, "sent "+GlyphDash+" next: stall health") {
		t.Fatalf("outcome %q flash %q", m.ui.walk.outcome[it.Key], m.ui.flash)
	}
	// left > 0: the page stays on the next question, the walk does not move
	m = rowsFixture(t, false, false)
	m, it = walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankAsk })
	m = step(t, m, answerMsg{ask: a, qid: q.ID, header: q.Header, choice: "yes", left: 1, key: a.ID + "|" + q.ID})
	if cur, ok := m.walkItem(); !ok || cur.Key != it.Key {
		t.Fatal("an answer with questions left moved the walk")
	}
}

func sent(tgt string, errs ...error) sendResultMsg {
	return sendResultMsg{plan: sendPlan{result: sendSent, tgt: tgt, body: "ok", directed: true}, errs: errs, window: true}
}

func TestTriageSendAdvancesAndFailureKeeps(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, it := walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankWait })
	if m2 := step(t, m, sent("someone-else")); func() bool { cur, _ := m2.walkItem(); return cur.Key != it.Key }() {
		t.Fatal("a send to another agent moved the walk")
	}
	failed := step(t, m, sent(it.Agent, errors.New("boom")))
	if cur, ok := failed.walkItem(); !ok || cur.Key != it.Key || !failed.ui.flashErr {
		t.Fatalf("a failed send moved the walk or said nothing: %q", failed.ui.flash)
	}
	m = step(t, m, sent(it.Agent))
	cur, ok := m.walkItem()
	if !ok || cur.Key == it.Key || m.ui.walk.outcome[it.Key] != "sent" {
		t.Fatalf("the send did not move the walk: %+v", cur)
	}
	flash := m.ui.flash
	if !strings.HasPrefix(flash, "sent "+GlyphDash+" next: "+cur.Agent) {
		t.Fatalf("flash %q", flash)
	}
	m = step(t, m, pollMsg{snap: m.snap, at: shellNow}) // no timer: a poll keeps it
	if m.ui.flash != flash {
		t.Fatalf("a poll cleared the flash: %q", m.ui.flash)
	}
	m = keys(t, m, 'j')
	if m.ui.flash == flash {
		t.Fatal("the next key kept the flash")
	}
	// a message to master on any other item is no terminal act: plan tasks
	// are no rows, so is their Action row's act (home-inbox-drawer §8)
	m = rowsFixture(t, false, false)
	m, it = walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankGate })
	m = step(t, m, sent("master"))
	if cur, _ := m.walkItem(); cur.Key != it.Key {
		t.Fatal("a message to master moved the walk off a gate")
	}
}

func TestTriageNewAskBeforeOlderUnread(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, _ = walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankMerge })
	s := m.snap
	a := s.asks[0]
	a.ID, a.CreatedAt = "1793027000000-0", shellNow.UnixMilli()
	s.asks = append(append([]bus.Ask(nil), s.asks...), a)
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	for i := 0; i < 5; i++ {
		m = keys(t, m, 'n')
		it, ok := m.walkItem()
		if !ok {
			t.Fatal("walk ended before the new ask")
		}
		if it.Rank == rankUnread {
			t.Fatal("the older unread line came before the new ask")
		}
		if it.AskID == a.ID {
			return
		}
	}
	t.Fatal("the new ask was never reached")
}

func TestTriageBackSkipsGoneReachesRead(t *testing.T) {
	m := rowsFixture(t, false, false)
	var unread attentionItem
	for _, it := range m.forYou {
		if it.Rank == rankUnread {
			unread = it
		}
	}
	m = keys(t, m, kT)
	for i := 0; i < 20 && !(m.rd.top().kind == ovMessage && "unread:"+m.rd.top().ev.ID == unread.Key); i++ {
		m = keys(t, m, 'n')
	}
	m = keys(t, m, 'n') // the end page
	if topKind(t, m) != ovTriageEnd {
		t.Fatal("no end page")
	}
	m = keys(t, m, 'p') // the line read under the walk, gone from the list
	if _, still := m.forYouItem(unread.Key); still || topKind(t, m) != ovMessage || "unread:"+m.rd.top().ev.ID != unread.Key {
		t.Fatalf("p did not reach the read line: %v", m.rd.top().kind)
	}
	// the merge row before it goes (merged): p skips it
	s := m.snap
	s.mergeQ = bus.MergeQueueRecord{AttemptedAt: shellNow.UnixMilli()}
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	m = keys(t, m, 'p')
	if it, ok := m.walkItem(); !ok || it.Rank != rankGate {
		t.Fatalf("p did not skip the gone merge row: %v", m.rd.top().kind)
	}
}

func TestTriageEmptyOpensTheEndPage(t *testing.T) {
	m := rowsFixture(t, false, true)
	m = keys(t, m, kT)
	if topKind(t, m) != ovTriageEnd || !strings.Contains(m.grid().plain(), "Nothing waits for you") {
		t.Fatalf("empty: %v\n%s", m.rd.top().kind, m.grid().plain())
	}
	m = keys(t, m, kEnter)
	if topKind(t, m) != ovPlanTask || m.rd.top().key != "p1" {
		t.Fatalf("enter on the end page: %v %q", m.rd.top().kind, m.rd.top().key)
	}
	m = keys(t, m, kEsc)
	if topKind(t, m) != ovTriageEnd || m.ui.walk == nil {
		t.Fatal("esc from the task did not return to the end page")
	}
	m = keys(t, m, kEsc)
	if m.rd.open() || m.ui.walk != nil {
		t.Fatal("esc on the end page did not end the walk")
	}
}

func TestTriageEscEndsAndHoldsModals(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = keys(t, m, kT, 'n')
	if !m.busyForModal() {
		t.Fatal("the reminder could open during the walk")
	}
	m.rd.pages = nil // the gap between two pages: the walk alone holds the modals
	if !m.busyForModal() {
		t.Fatal("the walk's own flag does not hold the modals")
	}
	m = rowsFixture(t, false, false)
	m = keys(t, m, kT, 'n', kEsc)
	if m.rd.open() || m.ui.walk != nil {
		t.Fatal("esc did not end the walk")
	}
	// a page opened above the walk's: n is that page's, esc returns to the walk
	m = keys(t, m, kT, 'n') // health
	m = keys(t, m, tea.Key{Code: tea.KeyF5})
	if m.walkPage() {
		t.Fatal("the page above counted as the walk's")
	}
	m = keys(t, m, kEsc)
	if !m.walkPage() {
		t.Fatal("esc above did not return to the walk")
	}
}

func TestTriageKey(t *testing.T) {
	if lookup(ModeShell, PanelForYou, TierWide, Key{Code: keyRune, Rune: 'T'}) != ActTriage {
		t.Fatal("T is not triage on the main screen")
	}
	if lookup(ModeShellAsk, PanelForYou, TierWide, Key{Code: keyRune, Rune: 'n'}) != ActNextItem {
		t.Fatal("n on an ask page")
	}
	m := rowsFixture(t, false, false)
	for _, w := range []int{60, 140} {
		f := footerHintsOn(ModeShell, PanelForYou, TierNarrow, w, nil, m.shellHint).text()
		if !strings.Contains(f, "T triage") || strings.Index(f, "enter") > strings.Index(f, "T triage") {
			t.Fatalf("width %d: T is kept, after enter: %q", w, f)
		}
	}
	var help string
	for _, l := range helpText() {
		help += l.text() + "\n"
	}
	if !strings.Contains(help, "triage") || !strings.Contains(help, "not t, the tour") {
		t.Fatal("F1 does not list T beside t")
	}
}

// foureyes (busmon-queue-strip): the screen after a header click is already
// the refreshed one.
func TestHeaderClickRefreshes(t *testing.T) {
	for _, hit := range []headerHit{hitUnread, hitHealth, hitDecide} {
		m := stripFixture(t)
		x, y := chipX(t, m, hit)
		m = clickAt(t, m, x, y)
		got := m.grid().plain()
		m.refresh()
		if want := m.grid().plain(); got != want {
			t.Fatalf("hit %d: the screen was not refreshed after the click", hit)
		}
	}
}

func TestTourListsTriage(t *testing.T) {
	for _, p := range tourBook {
		for _, b := range p.blocks {
			for _, r := range b.rows {
				for _, c := range r.cell {
					if c.key != nil && c.key.act == ActTriage {
						if p.since != tourVersion {
							t.Fatalf("page %q since %d: seen again only from %d", p.title, p.since, tourVersion)
						}
						return
					}
				}
			}
		}
	}
	t.Fatal("no tour row for T")
}

// Only merge:unknown left: the list is not empty, so the walk's done title,
// and the end page says the queue cannot be read (§4.4, reviewer-codex).
func TestTriageOnlyUnknownMerge(t *testing.T) {
	m := rowsFixture(t, false, true)
	s := m.snap
	s.mergeQ, s.mergeQRead = bus.MergeQueueRecord{AttemptedAt: shellNow.UnixMilli(), Reason: "no origin remote"}, true
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	if _, ok := m.forYouItem(mergeUnknownKey); !ok {
		t.Fatal("fixture: no merge:unknown row")
	}
	m = keys(t, m, kT)
	got := m.grid().plain()
	if topKind(t, m) != ovTriageEnd || strings.Contains(got, "Nothing waits for you") ||
		!strings.Contains(got, "Triage done: everything was seen once") || !strings.Contains(got, "merge queue unreadable") {
		t.Fatalf("only merge:unknown:\n%s", got)
	}
}

// An item dealt with elsewhere (gone from the list) is not counted skipped.
func TestTriageGoneIsNotSkipped(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, _ = walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankMerge })
	s := m.snap
	s.mergeQ = bus.MergeQueueRecord{AttemptedAt: shellNow.UnixMilli()} // merged meanwhile
	m = step(t, m, pollMsg{snap: s, at: shellNow})
	for i := 0; i < 5 && m.rd.top().kind != ovTriageEnd; i++ {
		m = keys(t, m, 'n')
	}
	end := m.grid().plain()
	// visited: ask, health, 3 waits, gate, merge (gone), unread (read): 6 skipped
	// (no plan row: home-inbox-drawer §3.3)
	if !strings.Contains(end, "read 1") || !strings.Contains(end, "skipped 6") {
		t.Fatalf("a row gone from the list counted as skipped:\n%s", end)
	}
}

// n after p goes forward through the visited items before the live list.
func TestTriageForwardThroughVisited(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = keys(t, m, kT, 'n', 'n') // ask, health, the first wait
	third, _ := m.walkItem()
	m = keys(t, m, 'p', 'p') // back to the ask
	if it, _ := m.walkItem(); it.Rank != rankAsk {
		t.Fatalf("p p: %s, want the ask", it.Key)
	}
	m = keys(t, m, 'n')
	if it, _ := m.walkItem(); it.Rank != rankHealth {
		t.Fatalf("n after p: %s, want the second visited item (health)", it.Key)
	}
	m = keys(t, m, 'n')
	if it, _ := m.walkItem(); it.Key != third.Key {
		t.Fatalf("n: %s, want %s", it.Key, third.Key)
	}
	if got := m.walkTitle(); got != "triage 3/8" { // no plan row (home-inbox-drawer §3.3)
		t.Fatalf("title %q", got)
	}
}

// The end page's frame carries no walk place.
func TestTriageEndPageHasNoPlace(t *testing.T) {
	m := rowsFixture(t, false, true)
	m = keys(t, m, kT)
	if topKind(t, m) != ovTriageEnd {
		t.Fatal("no end page")
	}
	if got := m.grid().plain(); strings.Contains(got, "triage ") {
		t.Fatalf("the end page shows a walk place:\n%s", got)
	}
	m = rowsFixture(t, false, false)
	m = keys(t, m, kT)
	for i := 0; i < 12 && m.rd.top().kind != ovTriageEnd; i++ {
		m = keys(t, m, 'n')
	}
	if got := m.grid().plain(); strings.Contains(got, "triage ") || !strings.Contains(got, "Triage done") {
		t.Fatalf("the end page after a walk shows a walk place:\n%s", got)
	}
}

// An ask answered on a page above the walk's closes that page as today; the
// walk stays on its item.
func TestTriageAnswerAboveTheWalkKeepsIt(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, it := walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankWait })
	ask := m.snap.asks[0]
	m.pushAsk(ask.ID)
	if m.walkPage() || topKind(t, m) != ovAsk {
		t.Fatal("fixture: the ask page is not above the walk's")
	}
	q := ask.Questions[0]
	m = step(t, m, answerMsg{ask: ask, qid: q.ID, header: q.Header, choice: "yes", left: 0, key: ask.ID + "|" + q.ID})
	if cur, ok := m.walkItem(); !ok || cur.Key != it.Key || topKind(t, m) != ovAgent {
		t.Fatalf("the answer above moved the walk: %v", m.rd.top().kind)
	}
}

// The walk's ask closed elsewhere: the refresh that drops its page moves the
// walk on, never ends it (foureyes).
func TestTriageAskClosedElsewhereMovesOn(t *testing.T) {
	m := rowsFixture(t, false, false)
	m = keys(t, m, kT)
	s := m.snap
	s.asks = nil
	m = step(t, m, pollMsg{snap: s, at: shellNow}) // the refresh drops the ask's page
	if m.ui.walk == nil || !m.walkPage() {
		t.Fatal("the walk ended")
	}
	if it, ok := m.walkItem(); !ok || it.Rank == rankAsk {
		t.Fatalf("the walk did not move on: %+v", it)
	}
}
