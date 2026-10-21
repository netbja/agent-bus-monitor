package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestPersonEntry(t *testing.T) {
	cases := []struct {
		name   string
		e      bus.Event
		person string
		want   bool
	}{
		{"an ask", bus.Event{Kind: "cmd", Type: bus.CmdAsk, From: "master", Target: bus.PeopleTarget}, "bernard", true},
		{"a cmd to the person", bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "bernard"}, "bernard", true},
		{"a cmd to the default identity", bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "pilot"}, "pilot", false},
		{"agent to agent", bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "coder"}, "bernard", false},
		{"the person's own message", bus.Event{Kind: "cmd", Type: bus.CmdDirective, From: "bernard", Target: "bernard", Human: true}, "bernard", false},
		{"a notice for the person", bus.Event{Kind: "notify", From: "dispatch", For: bus.ForPerson}, "bernard", true},
		{"the trim-gap notice", bus.Event{Kind: "notify", From: "dispatch"}, "bernard", false},
		{"a notify from an agent", bus.Event{Kind: "notify", From: "coder"}, "bernard", false},
		{"a report", bus.Event{Kind: "report", Agent: "master"}, "bernard", false},
	}
	for _, c := range cases {
		if got := personEntry(c.e, c.person); got != c.want {
			t.Errorf("%s: personEntry = %v, want %v", c.name, got, c.want)
		}
	}
}

// line is a feed line whose stream id is its time, as on the bus
// (waitingAnswerAt orders by id); name is only the test's label.
func line(name, kind, from, target, typ string, at time.Time) feedLine {
	id := fmt.Sprintf("%d-0", at.UnixMilli())
	e := bus.Event{ID: id, Kind: kind, From: from, Agent: from, Target: target, Type: typ}
	return feedLine{id: name, ev: e, at: at}
}

func TestMarks(t *testing.T) {
	now := time.Now()
	at := func(m int) time.Time { return now.Add(time.Duration(m) * time.Minute) }
	feed := []feedLine{
		line("1-0", "cmd", "master", "bernard", bus.CmdDirective, at(-50)), // older than the floor
		line("2-0", "cmd", "master", "bernard", bus.CmdDirective, at(-30)), // superseded by 3-0
		line("3-0", "cmd", "master", "bernard", bus.CmdDirective, at(-20)), // unread
		line("4-0", "notify", "dispatch", "", "", at(-15)),                 // read
		line("5-0", "cmd", "coder", "bernard", bus.CmdDirective, at(-10)),  // answered by the person below
		line("6-0", "cmd", "bernard", "coder", bus.CmdDirective, at(-5)),   // the answer
		line("7-0", "cmd", "sentinel", "people", bus.CmdAsk, at(-4)),       // a closed ask
		line("8-0", "cmd", "foureyes", "coder", bus.CmdDirective, at(-3)),  // not for the person
	}
	feed[5].ev.Human = true
	ids := map[string]string{} // test label → stream id
	for _, fl := range feed {
		ids[fl.id] = fl.ev.ID
	}
	u := unreadState{forPerson: map[string]bool{}, read: map[string]bool{ids["4-0"]: true}, floor: at(-40).UnixMilli()}
	for _, n := range []string{"1-0", "2-0", "3-0", "4-0", "5-0", "7-0"} {
		u.forPerson[ids[n]] = true
	}
	asks := map[string]bus.Ask{ids["7-0"]: {ID: ids["7-0"], State: "closed"}}
	got := u.marks(feed, asks)
	want := map[string]mark{"2-0": markSuperseded, "3-0": markUnread, "5-0": markSuperseded, "7-0": markSuperseded}
	for _, fl := range feed {
		if got[fl.ev.ID] != want[fl.id] {
			t.Errorf("%s: mark %v, want %v", fl.id, got[fl.ev.ID], want[fl.id])
		}
	}
	if n := unreadCount(got); n != 1 {
		t.Fatalf("unread count %d", n)
	}
}

// A report counts for the person when its agent waits for them (or carries
// the "for you" hint) at the time it is classified.
func TestReportOfAWaitingAgentIsForThePerson(t *testing.T) {
	now := time.Now()
	h := newHarness(t, demoShared(now))
	h.feed(bus.Event{ID: "100-0", Kind: "report", Agent: "foureyes", Message: "older"},
		bus.Event{ID: "101-0", Kind: "report", Agent: "foureyes", Message: "please decide"},
		bus.Event{ID: "102-0", Kind: "report", Agent: "coder", Message: "not waiting"},
		bus.Event{ID: "103-0", Kind: "status", Agent: "foureyes", State: "waiting", Message: "decide"})
	h.u.classifyWaits()
	if !h.u.unread.forPerson["101-0"] || h.u.unread.forPerson["100-0"] || h.u.unread.forPerson["102-0"] {
		t.Fatalf("classified %v", h.u.unread.forPerson)
	}
}

func TestActivityRowCarriesTheMark(t *testing.T) {
	fl := line("9", "cmd", "master", "bernard", bus.CmdDirective, time.Now())
	for _, c := range []struct {
		m     mark
		glyph string
		role  Role
	}{{markUnread, GlyphDotFull, RoleAttention}, {markSuperseded, GlyphDotEmpty, RoleMuted}, {markNone, " ", RoleMuted}} {
		fl.mark = c.m
		for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
			row := activityRow(fl, tier, 80)
			if row.Width() != 80 || !strings.HasPrefix(row.text(), c.glyph+" ") || row[0].Role != c.role {
				t.Fatalf("mark %v tier %v: %q role %v", c.m, tier, row.text(), row[0].Role)
			}
		}
	}
}

// Opening a line marks it read and saves the cache (0600), keeping only ids
// still in the retained window.
func TestOpeningAMarkedLineReadsIt(t *testing.T) {
	now := time.Now()
	h := newHarness(t, demoShared(now))
	dir := t.TempDir()
	h.u.cachePath = filepath.Join(dir, "busmon", "demo.json")
	h.u.cache = &busmonCache{Project: "demo", Read: []string{"1-0"}}
	h.feed(bus.Event{ID: "200-0", Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "bernard", Message: "look"})
	if !h.u.unread.forPerson["200-0"] {
		t.Fatal("a cmd to the person is not classified")
	}
	h.draw(160, 45)
	id := h.u.feed[len(h.u.feed)-1].id
	if h.u.feed[len(h.u.feed)-1].mark != markUnread {
		t.Fatalf("mark before opening %v", h.u.feed[len(h.u.feed)-1].mark)
	}
	h.u.openFeedLine(id)
	if !slices.Equal(h.u.cache.Read, []string{"200-0"}) {
		t.Fatalf("cache read %v (1-0 is no longer retained)", h.u.cache.Read)
	}
	info, err := os.Stat(h.u.cachePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %v %v", info, err)
	}
	h.u.closeOverlay()
	h.draw(160, 45)
	if h.u.feed[len(h.u.feed)-1].mark != markNone {
		t.Fatal("still marked after opening")
	}
}

func TestHeaderCountsUnread(t *testing.T) {
	d := headerData{project: "demo", unread: 2, now: time.Now(), health: health{ok: true}}
	if !strings.Contains(headerText(d), GlyphDotFull+" 2") {
		t.Fatalf("header %q", headerText(d))
	}
	d.unread = 0
	if strings.Contains(headerText(d), "unread") {
		t.Fatal("header counts zero")
	}
}

func TestKeyUJumpsToTheNextUnread(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.feed(bus.Event{ID: "300-0", Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "bernard", Message: "one"},
		bus.Event{ID: "301-0", Kind: "cmd", Type: bus.CmdDirective, From: "coder", Target: "foureyes", Message: "x"},
		bus.Event{ID: "302-0", Kind: "cmd", Type: bus.CmdDirective, From: "coder", Target: "bernard", Message: "two"})
	h.draw(160, 45)
	h.u.setFocusPanel(PanelActivity)
	h.key(runeKey('u'))
	first := h.u.lists[PanelActivity].Selected()
	h.key(runeKey('u'))
	second := h.u.lists[PanelActivity].Selected()
	h.key(runeKey('u'))
	third := h.u.lists[PanelActivity].Selected()
	ev := func(key string) string { return h.u.feed[h.u.feedIndex(key)].ev.ID }
	if ev(first) != "300-0" || ev(second) != "302-0" || ev(third) != "300-0" {
		t.Fatalf("u went %s, %s, %s", ev(first), ev(second), ev(third))
	}
}

func headerText(d headerData) string {
	var sb strings.Builder
	for _, l := range headerLines(d, 160, TierWide) {
		sb.WriteString(l.text() + "\n")
	}
	return sb.String()
}

// The alignment fixture draws unread and superseded rows (so every size and
// palette is checked with the mark column filled).
func TestAlignmentFeedHasBothMarks(t *testing.T) {
	h := newHarness(t, hostileShared(time.Now(), false))
	alignCases()[0].setup(h)
	h.draw(160, 45)
	seen := map[mark]bool{}
	for _, fl := range h.u.feed {
		seen[fl.mark] = true
	}
	if !seen[markUnread] || !seen[markSuperseded] {
		t.Fatalf("marks in the fixture: %v", seen)
	}
}

// The recap lists unread entries first, under "Expected from you".
func TestRecapListsUnreadFirst(t *testing.T) {
	now := time.Now()
	m := buildRecap(recapInput{Now: now, Human: "bernard", Agents: map[string]*agentState{},
		Unread: []bus.Event{{ID: fmt.Sprintf("%d-0", now.Add(-time.Minute).UnixMilli()), Kind: "cmd", From: "master", Target: "bernard", Message: "lot 1 is yours"}},
		Asks:   []bus.Ask{{ID: "a1", From: "coder", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q", Header: "Go?"}}}}}})
	if len(m.Rows) < 3 || m.Rows[0].Title != "Expected from you" || !strings.Contains(m.Rows[1].Title, "unread") || !strings.Contains(m.Rows[1].Detail, "lot 1 is yours") {
		var got []string
		for _, r := range m.Rows {
			got = append(got, r.Title)
		}
		t.Fatalf("rows %q", got)
	}
}
