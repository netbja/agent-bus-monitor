package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// noticeText is the notice on screen, glyph and text, "" none.
func noticeText(m Model) string {
	n := m.notice.cur
	if n == nil {
		return ""
	}
	return n.glyph() + " " + n.text
}

// quiet is the fixture shell with nothing paused, no bell, no fade: the base
// every notice test polls against (its first poll is the baseline).
func quiet(t *testing.T, fade bool) Model {
	t.Helper()
	pinLocal(t)
	return shellWith(t, 160, 40, shellConfig{settings: busmonSettings{Fade: fade}}, false)
}

// A pause shows when it appears between two polls and an automatic actor
// wrote it; the person's own, or one present at the first poll, shows nothing.
func TestNoticePause(t *testing.T) {
	until := shellNow.Add(112 * time.Minute).UnixMilli()
	for _, c := range []struct {
		name string
		rec  bus.PauseRecord
		want string
	}{
		{"the dispatcher's", bus.PauseRecord{Until: until, By: "dispatch", Reason: "lunch"}, GlyphNone + " dispatch: project paused until 16:56 (lunch)"},
		{"an agent's, no reason", bus.PauseRecord{Until: until, By: "master"}, GlyphNone + " master: project paused until 16:56"},
		{"a quota pause", bus.PauseRecord{Until: until, By: "dispatch", Reason: bus.QuotaPauseReason("anthropic")}, GlyphBlocked + " dispatch: paused for anthropic's quota until 16:56"},
		{"the person's", bus.PauseRecord{Until: until, By: "charlie", Reason: "lunch"}, ""},
		{"an unknown author", bus.PauseRecord{Until: until, By: "stranger"}, ""},
	} {
		m := quiet(t, false)
		s := shellFixture(shellNow)
		rec := c.rec
		s.pause = &rec
		if m = pollAgain(t, m, s, shellNow.Add(time.Second)); noticeText(m) != c.want {
			t.Errorf("%s: notice %q, want %q", c.name, noticeText(m), c.want)
		}
	}
	// Present at the first poll: the baseline, never a notice.
	m := shellWith(t, 160, 40, shellConfig{}, true)
	s := shellFixture(shellNow)
	s.pause.By = "dispatch"
	if m = pollAgain(t, m, s, shellNow.Add(time.Second)); noticeText(m) != "" {
		t.Fatalf("a pause already there showed %q", noticeText(m))
	}
	// A failed read keeps the last value: it is no transition either way.
	m = quiet(t, false)
	s = shellFixture(shellNow)
	s.pause, s.pauseFailed = nil, true
	m = pollAgain(t, m, s, shellNow.Add(time.Second))
	s = shellFixture(shellNow)
	s.pause.By = "dispatch"
	if m = pollAgain(t, m, s, shellNow.Add(2*time.Second)); noticeText(m) == "" {
		t.Fatal("a pause after a failed read showed nothing")
	}
}

// The resume and the automatic /clear come from the dispatcher's directives
// in the live tail; the backfill, a person's and another sender's do not.
func TestNoticeTail(t *testing.T) {
	live := func(id, from, target, text string, human bool) bus.Event {
		return bus.Event{ID: id, Kind: "cmd", From: from, Target: target, Type: bus.CmdDirective, Message: text, Human: human}
	}
	id := func(d time.Duration) string { return fmt.Sprintf("%d-0", shellNow.Add(d).UnixMilli()) }
	resume := "Project resumed: the pause (lunch) is over. Resume the team's work."
	cleared := "context cleared: re-run your boot steps (load your role's skills, read the memory index), then read your hand-off: agentbus reports 1-0"
	for _, c := range []struct {
		name string
		msg  tailMsg
		want string
	}{
		{"the dispatcher's resume", tailMsg{events: []bus.Event{live(id(time.Second), "dispatch", "master", resume, false)}}, GlyphDone + " project resumed"},
		{"the dispatcher's clear", tailMsg{events: []bus.Event{live(id(time.Second), "dispatch", "coder", cleared, false)}}, GlyphNone + " coder: context cleared after its hand-off"},
		{"the person's resume", tailMsg{events: []bus.Event{live(id(time.Second), "charlie", "master", resume, true)}}, ""},
		{"an agent quoting it", tailMsg{events: []bus.Event{live(id(time.Second), "master", "coder", resume, false)}}, ""},
		{"the backfill", tailMsg{events: []bus.Event{live(id(time.Second), "dispatch", "master", resume, false)}, backfill: true}, ""},
		{"older than busmon", tailMsg{events: []bus.Event{live(id(-time.Hour), "dispatch", "master", resume, false)}}, ""},
	} {
		m := quiet(t, false)
		m.cfg.liveFrom = shellNow
		if m = step(t, m, c.msg); noticeText(m) != c.want {
			t.Errorf("%s: notice %q, want %q", c.name, noticeText(m), c.want)
		}
	}
}

// Health: a check worsening or recovering between two polls; a check new
// since the last read, or the first read, shows nothing. A fail mended with
// no note is taken as proof of a restart (the record names no service).
func TestNoticeHealth(t *testing.T) {
	rec := func(st guardian.Status, detail string, since int64) guardian.Record {
		return guardian.Record{ID: "services", Status: st, Detail: detail, Since: since}
	}
	failSince := shellNow.Add(-time.Hour).UnixMilli()
	for _, c := range []struct {
		name     string
		from, to guardian.Record
		note     *guardian.Note
		want     string
	}{
		{"ok to warn", rec(guardian.OK, "", 1), rec(guardian.Warn, "budget low", 2), nil, GlyphBlocked + " health: services warns (budget low)"},
		{"warn to fail", rec(guardian.Warn, "", 1), rec(guardian.Fail, "budget spent", 2), nil, GlyphBlocked + " health: services fails (budget spent)"},
		{"unknown to fail", rec(guardian.Unknown, "", 1), rec(guardian.Fail, "", 2), nil, GlyphBlocked + " health: services fails"},
		{"warn to ok", rec(guardian.Warn, "", 1), rec(guardian.OK, "fine", 2), nil, GlyphDone + " health: services ok again"},
		{"fail to ok, sentinel's note", rec(guardian.Fail, "", failSince), rec(guardian.OK, "", 2),
			&guardian.Note{ID: "services", Author: "sentinel", At: failSince + 1000, Text: "restarted missions"}, GlyphDone + " health: services ok again"},
		{"fail to ok, an old note", rec(guardian.Fail, "", failSince), rec(guardian.OK, "", 2),
			&guardian.Note{ID: "services", Author: "sentinel", At: failSince - 1000, Text: "restarted missions"}, GlyphDone + " health: services ok again"},
		{"fail to ok, the person's note", rec(guardian.Fail, "", failSince), rec(guardian.OK, "", 2),
			&guardian.Note{ID: "services", Author: "charlie", At: failSince + 1000, Text: "did it myself"}, GlyphDone + " health: services ok again"},
		{"warn to unknown", rec(guardian.Warn, "", 1), rec(guardian.Unknown, "", 2), nil, ""},
		{"fail to warn", rec(guardian.Fail, "", 1), rec(guardian.Warn, "", 2), nil, ""},
	} {
		m := quiet(t, false)
		s := shellFixture(shellNow)
		s.guardianHealth = map[string]guardian.Record{"services": c.from}
		m = pollAgain(t, m, s, shellNow.Add(time.Second))
		s = shellFixture(shellNow)
		s.guardianHealth = map[string]guardian.Record{"services": c.to}
		if c.note != nil {
			s.guardianNotes = map[string]guardian.Note{"services": *c.note}
		}
		if m = pollAgain(t, m, s, shellNow.Add(2*time.Second)); noticeText(m) != c.want {
			t.Errorf("%s: notice %q, want %q", c.name, noticeText(m), c.want)
		}
	}
	// The first read and a check new since the last read are baselines.
	m := quiet(t, false)
	s := shellFixture(shellNow)
	s.guardianHealth = map[string]guardian.Record{"services": rec(guardian.Fail, "", 1)}
	if m = pollAgain(t, m, s, shellNow.Add(time.Second)); noticeText(m) != "" {
		t.Fatalf("the first health read showed %q", noticeText(m))
	}
	s = shellFixture(shellNow)
	s.guardianHealth = map[string]guardian.Record{"services": rec(guardian.Fail, "", 1), "runaway": rec(guardian.Warn, "", 2)}
	if m = pollAgain(t, m, s, shellNow.Add(2*time.Second)); noticeText(m) != "" {
		t.Fatalf("a new check showed %q", noticeText(m))
	}
}

// The ledger's entries, by an automatic author, each make one line; the
// person's make none; a loss makes one attention line.
func TestNoticeSwaps(t *testing.T) {
	for _, c := range []struct {
		name string
		ev   bus.SwapEvent
		want string
	}{
		{"a reload begins", bus.SwapEvent{Role: "coder", Event: bus.SwapBegin, Kind: bus.SwapKindReload, By: "sentinel", Reason: "client update"}, GlyphNone + " sentinel reloads coder: client update"},
		{"a reload ends", bus.SwapEvent{Role: "coder", Event: bus.SwapReloaded, Kind: bus.SwapKindReload, By: "sentinel"}, GlyphDone + " coder reloaded"},
		{"a reload fails", bus.SwapEvent{Role: "coder", Event: bus.SwapFailed, Kind: bus.SwapKindReload, By: "sentinel"}, GlyphBlocked + " coder: reload failed (agentbus swaps)"},
		{"a swap to a fallback", bus.SwapEvent{Role: "coder", Event: bus.SwapDone, ToProfile: "kimi-alt", By: "master", Reason: "quota"}, GlyphNone + " coder now runs on kimi-alt: quota"},
		{"a swap back", bus.SwapEvent{Role: "coder", Event: bus.SwapBack, By: "master"}, GlyphDone + " coder back on its own profile"},
		{"the person's reload", bus.SwapEvent{Role: "coder", Event: bus.SwapBegin, Kind: bus.SwapKindReload, By: "charlie"}, ""},
		{"a swap's begin", bus.SwapEvent{Role: "coder", Event: bus.SwapBegin, By: "master"}, ""},
	} {
		m := quiet(t, false)
		if m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: shellNow.Add(time.Second), swaps: []bus.SwapEvent{c.ev}}); noticeText(m) != c.want {
			t.Errorf("%s: notice %q, want %q", c.name, noticeText(m), c.want)
		}
	}
	m := quiet(t, false)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: shellNow.Add(time.Second), swapLost: true})
	if want := GlyphBlocked + " some reloads or swaps were trimmed before busmon read them (agentbus swaps)"; noticeText(m) != want {
		t.Fatalf("a loss: %q, want %q", noticeText(m), want)
	}
}

// The cursor (busmon-notice spec, "Cadence"): the first read is the
// baseline; later reads pass their events on and count the losses by
// entries-added; a count that went down is a new baseline, never a loss.
func TestSwapCursor(t *testing.T) {
	ev := func(ids ...string) []bus.SwapEvent {
		var out []bus.SwapEvent
		for _, id := range ids {
			out = append(out, bus.SwapEvent{ID: id})
		}
		return out
	}
	var c swapCursor
	if c.after() != "" {
		t.Fatalf("before a baseline the read asks for no events: %q", c.after())
	}
	if got, lost := c.advance(nil, 4, "9-0"); got != nil || lost || c.after() != "9-0" {
		t.Fatalf("baseline: %v %v %q", got, lost, c.after())
	}
	if got, lost := c.advance(ev("10-0", "11-0"), 6, "11-0"); len(got) != 2 || lost || c.after() != "11-0" {
		t.Fatalf("two new: %v %v %q", got, lost, c.after())
	}
	if got, lost := c.advance(ev("14-0"), 9, "14-0"); len(got) != 1 || !lost {
		t.Fatalf("three added, one read: %v lost=%v", got, lost)
	}
	// Nothing read (the newest entries deleted): the cursor still moves to last.
	if got, lost := c.advance(nil, 9, "15-0"); got != nil || lost || c.after() != "15-0" {
		t.Fatalf("empty read: %v %v %q", got, lost, c.after())
	}
	if got, lost := c.advance(ev("1-0"), 1, "1-0"); got != nil || lost || c.after() != "1-0" {
		t.Fatalf("the ledger began again: %v %v %q", got, lost, c.after())
	}
}

// pollOnce reads the ledger every fifth tick through the optional reader and
// hands the new entries over once (takeSwapNews).
func TestPollReadsTheLedger(t *testing.T) {
	f := &swapLedgerFake{fakeReader: &fakeReader{calls: map[string]int{}}, added: 2, last: "2-0"}
	st := &shared{person: "charlie", agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	pollOnce(t.Context(), f, st, 1)
	if ev, lost := takeSwapNews(st); len(ev) != 0 || lost || f.afters[0] != "" {
		t.Fatalf("baseline: %v %v %q", ev, lost, f.afters)
	}
	f.events, f.added, f.last = []bus.SwapEvent{{ID: "3-0", Role: "coder"}}, 4, "3-0"
	pollOnce(t.Context(), f, st, 2)
	if len(f.afters) != 1 {
		t.Fatalf("read off the fifth tick: %q", f.afters)
	}
	pollOnce(t.Context(), f, st, 6)
	ev, lost := takeSwapNews(st)
	if len(ev) != 1 || !lost || f.afters[1] != "2-0" {
		t.Fatalf("second read: %v %v %q", ev, lost, f.afters)
	}
	if ev, lost := takeSwapNews(st); ev != nil || lost {
		t.Fatalf("handed over twice: %v %v", ev, lost)
	}
}

type swapLedgerFake struct {
	*fakeReader
	events []bus.SwapEvent
	added  int64
	last   string
	afters []string
}

func (f *swapLedgerFake) SwapEventsAfter(_ context.Context, after string, _ int) ([]bus.SwapEvent, int64, string, error) {
	f.afters = append(f.afters, after)
	return f.events, f.added, f.last, nil
}

// One at a time: 4 s alone, 2 s while others wait; past five waiting the rest
// collapse into one line; a flash or a dialog holds the next one back.
func TestNoticeQueue(t *testing.T) {
	m := quiet(t, false)
	evs := make([]bus.SwapEvent, 0, 7)
	for _, r := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		evs = append(evs, bus.SwapEvent{Role: r, Event: bus.SwapBack, By: "master"})
	}
	t0 := shellNow.Add(time.Second)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: t0, swaps: evs})
	if noticeText(m) != GlyphDone+" a back on its own profile" || len(m.notice.queue) != 4 || m.notice.more != 2 {
		t.Fatalf("first: %q, queue %d, more %d", noticeText(m), len(m.notice.queue), m.notice.more)
	}
	at := func(d time.Duration) Model { return step(t, m, fadeMsg(t0.Add(d))) }
	if m = at(1900 * time.Millisecond); noticeText(m) != GlyphDone+" a back on its own profile" {
		t.Fatalf("gone before 2 s: %q", noticeText(m))
	}
	if m = at(2 * time.Second); noticeText(m) != GlyphDone+" b back on its own profile" {
		t.Fatalf("not the next at 2 s: %q", noticeText(m))
	}
	for i, want := range []string{"c", "d", "e"} {
		if m = at(time.Duration(4+2*i) * time.Second); noticeText(m) != GlyphDone+" "+want+" back on its own profile" {
			t.Fatalf("%d: %q", i, noticeText(m))
		}
	}
	// The last one: the collapsed rest, alone, so 4 s.
	if m = at(10 * time.Second); noticeText(m) != GlyphNone+" 2 more automatic actions (R: recap)" {
		t.Fatalf("collapsed: %q", noticeText(m))
	}
	if m = at(13900 * time.Millisecond); noticeText(m) == "" {
		t.Fatal("alone, gone before 4 s")
	}
	if m = at(14 * time.Second); noticeText(m) != "" {
		t.Fatalf("still there after 4 s: %q", noticeText(m))
	}

	// A flash holds the next notice; it starts once the flash is gone.
	m = quiet(t, false)
	m.ui.flash = "auto-scroll on"
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: t0, swaps: evs[:1]})
	if noticeText(m) != "" || len(m.notice.queue) != 1 {
		t.Fatalf("started under a flash: %q", noticeText(m))
	}
	m.ui.flash = ""
	if m = step(t, m, fadeMsg(t0.Add(3*time.Second))); noticeText(m) == "" || !m.notice.start.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("not started after the flash: %q at %v", noticeText(m), m.notice.start)
	}

	// One on screen when a flash comes keeps the time it had left: it does
	// not age under the flash (reviewer-codex).
	m = quiet(t, false)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: t0, swaps: evs[:1]})
	m = step(t, m, fadeMsg(t0.Add(time.Second)))
	m.ui.flash = "auto-scroll on" // set by a key: the same update sees it
	m = step(t, m, fadeMsg(t0.Add(time.Second)))
	if m = step(t, m, fadeMsg(t0.Add(30*time.Second))); noticeText(m) == "" {
		t.Fatal("aged out under the flash")
	}
	m.ui.flash = ""
	m = step(t, m, fadeMsg(t0.Add(30*time.Second)))
	if m = step(t, m, fadeMsg(t0.Add(32900*time.Millisecond))); noticeText(m) == "" {
		t.Fatal("lost the time it had left")
	}
	if m = step(t, m, fadeMsg(t0.Add(33*time.Second))); noticeText(m) != "" {
		t.Fatalf("outlived its 4 s: %q", noticeText(m))
	}
}

// The footer: the notice at the right end, the hints giving way from the
// right; the flash still wins; the row is exactly the width.
func TestNoticeFooter(t *testing.T) {
	m := quiet(t, false)
	plain := footerRow(m)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: shellNow.Add(time.Second),
		swaps: []bus.SwapEvent{{Role: "coder", Event: bus.SwapReloaded, Kind: bus.SwapKindReload, By: "sentinel"}}})
	row := footerRow(m)
	if !strings.HasSuffix(row, GlyphDone+" coder reloaded ") {
		t.Fatalf("not at the right end: %q", row)
	}
	if Width(row) != 160 {
		t.Fatalf("footer is %d cells", Width(row))
	}
	first := strings.Fields(plain)[0]
	if !strings.HasPrefix(strings.TrimSpace(row), first) {
		t.Fatalf("the left hints moved: %q vs %q", row, plain)
	}
	m.ui.flash = "auto-scroll on"
	if row := footerRow(m); strings.Contains(row, "reloaded") {
		t.Fatalf("the notice drew over a flash: %q", row)
	}
}

// The fade: true colour with fade on blends the last second; anything else
// is drawn whole and dropped. The fade frames run only while one shows.
func TestNoticeFade(t *testing.T) {
	reload := []bus.SwapEvent{{Role: "coder", Event: bus.SwapReloaded, Kind: bus.SwapKindReload, By: "sentinel"}}
	t0 := shellNow.Add(time.Second)
	withPalette(t, "dark", false, 1<<24)
	m := quiet(t, true)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: t0, swaps: reload})
	if !m.animated() {
		t.Fatal("a notice on screen schedules no frames")
	}
	if f := m.noticeLine(30)[0].Fade; f != 0 {
		t.Fatalf("faded at once: %d", f)
	}
	m = step(t, m, fadeMsg(t0.Add(3500*time.Millisecond)))
	if f := m.noticeLine(30)[0].Fade; f < 40 || f > 60 {
		t.Fatalf("half way through the blend: %d", f)
	}
	m = step(t, m, fadeMsg(t0.Add(4*time.Second)))
	if noticeText(m) != "" {
		t.Fatal("still there after 4 s")
	}

	withPalette(t, "dark", false, 16)
	m = quiet(t, true)
	m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: t0, swaps: reload})
	m = step(t, m, fadeMsg(t0.Add(3500*time.Millisecond)))
	if f := m.noticeLine(30)[0].Fade; f != 0 {
		t.Fatalf("16 colours faded: %d", f)
	}
}

// footerRow is the footer's text, cell by cell.
func footerRow(m Model) string { return rowText(m.grid(), m.layout.Footer.Y, 0, m.width) }
