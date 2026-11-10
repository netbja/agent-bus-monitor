package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/history"
)

func histID(at time.Time) string { return strconv.FormatInt(at.UnixMilli(), 10) + "-0" }

func histFixture() []history.Entry {
	d1 := time.Date(2026, 10, 31, 23, 58, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 1, 18, 47, 0, 0, time.UTC)
	return []history.Entry{
		{Stream: "verdicts", ID: histID(d1), F: map[string]string{"reviewer": "foureyes", "author": "coder", "decision": "approve", "subject": "busmon-notice", "message": "APPROVE"}},
		{Stream: "gap", ID: histID(d2.Add(-time.Hour)), F: map[string]string{"of": "status", "from": histID(d2.Add(-4 * time.Hour)), "to": histID(d2.Add(-time.Hour))}},
		{Stream: "cmd", ID: histID(d2), F: map[string]string{"from": "master", "target": "coder", "type": "directive", "command": "New feature from the person"}},
		{Stream: "cmd", ID: histID(d2.Add(2 * time.Minute)), F: map[string]string{"from": "coder", "target": "master", "type": "reply", "command": "queued after step 4"}},
	}
}

func histTexts(body []Line) []string {
	var out []string
	for _, l := range body {
		out = append(out, strings.TrimRight(l.text(), " "))
	}
	return out
}

func TestHistoryPageNewestFirstUnderDayHeadings(t *testing.T) {
	v := histView{entries: histFixture(), days: 3, loaded: true}
	title, body, items := historyBody(v, 0, time.UTC, 100)
	if title != "History "+GlyphNone+" 3 days "+GlyphNone+" 3 entries" {
		t.Errorf("title %q", title)
	}
	got := strings.Join(histTexts(body), "\n")
	for _, w := range []string{
		"  Sun 2026-11-01",
		"18:49  coder " + GlyphArrowR + " master",
		strings.Repeat(glyphHorizontal, 2) + " not archived: 14:47 to 17:47 (status) " + strings.Repeat(glyphHorizontal, 2),
		"  Sat 2026-10-31",
		"23:58  foureyes " + GlyphArrowR + " coder",
		"o loads the 3 days before",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	if strings.Index(got, "18:49") > strings.Index(got, "18:47") || strings.Index(got, "Sun 2026-11-01") > strings.Index(got, "Sat 2026-10-31") {
		t.Errorf("not newest first:\n%s", got)
	}
	if len(items) != 3 || items[0].e.F["type"] != "reply" || !strings.Contains(histTexts(body)[items[0].line], "queued") {
		t.Fatalf("items %+v", items)
	}
	if !strings.HasPrefix(histTexts(body)[items[0].line], GlyphSelect) {
		t.Errorf("the selected row carries the mark: %q", histTexts(body)[items[0].line])
	}
}

func TestHistoryPageSaysWhatIsMissing(t *testing.T) {
	v := histView{entries: histFixture(), days: 3, loaded: true, cut: true, noArchive: true, liveErr: "dial: refused"}
	_, body, _ := historyBody(v, 0, time.UTC, 100)
	got := strings.Join(histTexts(body), "\n")
	for _, w := range []string{"more archive lines not read", "no archive on this host", "broker unreachable"} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	_, body, items := historyBody(histView{days: 3}, 0, time.UTC, 100)
	if len(items) != 0 || !strings.Contains(strings.Join(histTexts(body), "\n"), "loading") {
		t.Errorf("before the load: %v", histTexts(body))
	}
}

func TestHistoryPageShortKindsWhenNarrow(t *testing.T) {
	v := histView{entries: histFixture(), days: 3, loaded: true}
	_, body, _ := historyBody(v, 0, time.UTC, 60)
	got := strings.Join(histTexts(body), "\n")
	if !strings.Contains(got, " dir ") || strings.Contains(got, "directive") {
		t.Errorf("narrow kinds:\n%s", got)
	}
	for _, l := range body {
		if l.Width() > 60 {
			t.Errorf("wider than 60: %q", l.text())
		}
	}
}

// H opens the page and loads it in a command; o loads three more days; Enter
// opens an entry in the message page.
func TestHistoryPageOpensLoadsAndOpensAnEntry(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	recent := history.Entry{Stream: "cmd", ID: histID(now.Add(-time.Hour)), F: map[string]string{"from": "master", "target": "coder", "type": "directive", "command": "recent directive"}}
	old := history.Entry{Stream: "report", ID: histID(now.AddDate(0, 0, -4)), F: map[string]string{"agent": "coder", "kind": "note", "message": "an older report"}}
	for _, e := range []history.Entry{old, recent} {
		b, _ := json.Marshal(e)
		p := filepath.Join(dir, history.At(e).UTC().Format("2006-01-02")+".jsonl")
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(append(b, '\n'))
		f.Close()
	}
	m := fixtureShell(t, 120, 40)
	m.cfg.histDir = dir
	m, cmd := pressCmd(t, m, 'H')
	if !m.rd.open() || m.rd.top().kind != ovHistory || cmd == nil {
		t.Fatal("H opened no History page with a load")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	body := strings.Join(histTexts(m.rd.top().body), "\n")
	if !strings.Contains(body, "recent directive") || strings.Contains(body, "an older report") {
		t.Fatalf("the last 3 days:\n%s", body)
	}
	m, cmd = pressCmd(t, m, 'o')
	if cmd == nil {
		t.Fatal("o loads nothing")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if body := strings.Join(histTexts(m.rd.top().body), "\n"); !strings.Contains(body, "an older report") || !strings.Contains(m.rd.top().title, "6 days") {
		t.Fatalf("o: %q\n%s", m.rd.top().title, body)
	}
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	if top := m.rd.top(); top.kind != ovMessage || top.ev.ID != recent.ID {
		t.Fatalf("enter opened %v %q", top.kind, top.ev.ID)
	}
}

// The page at 120 columns and in the narrow tier (short kinds).
func TestHistoryPageGoldens(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{120, 40}, {70, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := fixtureShell(t, sz[0], sz[1])
			m, _ = pressCmd(t, m, 'H')
			next, _ := m.Update(historyMsg{serial: m.rd.top().serial, v: histView{entries: histFixture(), days: 3, loaded: true}})
			m = next.(Model)
			m.paint = newPainter(th)
			checkGolden(t, fmt.Sprintf("history-%dx%d.txt", sz[0], sz[1]), m.grid().plain()+"\n")
		})
	}
}

func loadedHistory(t *testing.T, es []history.Entry) Model {
	t.Helper()
	m := fixtureShell(t, 120, 40)
	m, _ = pressCmd(t, m, 'H')
	next, _ := m.Update(historyMsg{serial: m.rd.top().serial, v: histView{entries: es, days: 3, loaded: true}})
	return next.(Model)
}

// / filters the page as Activity's filter reads it; t narrows it to the
// selected entry's thread; an empty filter shows everything again.
func TestHistoryPageFilterAndThread(t *testing.T) {
	es := histFixture()
	root := es[2]
	es = append(es, history.Entry{Stream: "cmd", ID: histID(history.At(root).Add(5 * time.Minute)),
		F: map[string]string{"from": "coder", "target": "master", "type": "reply", "ref": root.ID, "command": "done with it"}})
	m := loadedHistory(t, es)
	m, _ = pressCmd(t, m, '/')
	m = typeText(t, m, "@foureyes")
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	if p := m.rd.top(); p.kind != ovHistory || len(p.hrows) != 1 || p.hrows[0].e.Stream != "verdicts" || !strings.Contains(p.title, "filter: @foureyes") {
		t.Fatalf("filtered: %q %+v", p.title, p.hrows)
	}
	m, _ = pressCmd(t, m, '/')
	for range len("@foureyes") {
		m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyBackspace})
	}
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	if n := len(m.rd.top().hrows); n != 4 {
		t.Fatalf("an empty filter shows all: %d", n)
	}
	// The newest row is the reply; t keeps its thread: the directive and the reply.
	m, _ = pressCmd(t, m, 't')
	p := m.rd.top()
	if len(p.hrows) != 2 || p.hrows[1].e.ID != root.ID {
		t.Fatalf("thread: %+v", p.hrows)
	}
}

// n/p on an entry opened from History step through the page's rows.
func TestHistoryEntryNextPrevious(t *testing.T) {
	m := loadedHistory(t, histFixture())
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	first := m.rd.top().ev.ID
	m, _ = pressCmd(t, m, 'n')
	if top := m.rd.top(); top.kind != ovMessage || top.ev.ID == first || len(m.rd.pages) != 2 {
		t.Fatalf("n: %v %q (pages %d)", top.kind, top.ev.ID, len(m.rd.pages))
	}
	m, _ = pressCmd(t, m, 'p')
	if m.rd.top().ev.ID != first {
		t.Fatalf("p went back to %q", m.rd.top().ev.ID)
	}
	m, _ = pressCmd(t, m, 'p')
	if m.ui.flash != "no previous item" {
		t.Fatalf("flash %q", m.ui.flash)
	}
}

// After a cut, o appends the lines before the oldest loaded one (foureyes).
func TestHistoryOlderAfterACutReachesOlderLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var es []history.Entry
	for i := 0; i < 30; i++ {
		es = append(es, history.Entry{Stream: "cmd", ID: histID(now.Add(-time.Duration(30-i) * time.Minute)), F: map[string]string{"type": "directive", "command": fmt.Sprintf("line %02d %s", i, strings.Repeat("x", 60))}})
	}
	f, _ := os.Create(filepath.Join(dir, now.UTC().Format("2006-01-02")+".jsonl"))
	for _, e := range es {
		b, _ := json.Marshal(e)
		f.Write(append(b, '\n'))
	}
	f.Close()
	m := fixtureShell(t, 120, 40)
	m.cfg.histDir = dir
	m.histCap = 1200
	m, cmd := pressCmd(t, m, 'H')
	next, _ := m.Update(cmd())
	m = next.(Model)
	p := m.rd.top()
	if !p.hist.cut || len(p.hist.entries) >= 30 {
		t.Fatalf("the first load is cut: %d cut %v", len(p.hist.entries), p.hist.cut)
	}
	n := len(p.hist.entries)
	m, cmd = pressCmd(t, m, 'o')
	next, _ = m.Update(cmd())
	m = next.(Model)
	if got := len(m.rd.top().hist.entries); got <= n {
		t.Fatalf("o added nothing older: %d then %d", n, got)
	}
}

// A failed read of gaps.jsonl is said, never silent (reviewer-codex).
func TestHistoryPageSaysAFailedGapRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "gaps.jsonl"), 0o700); err != nil { // a directory: the read fails
		t.Fatal(err)
	}
	m := fixtureShell(t, 120, 40)
	m.cfg.histDir = dir
	m, cmd := pressCmd(t, m, 'H')
	next, _ := m.Update(cmd())
	m = next.(Model)
	if body := strings.Join(histTexts(m.rd.top().body), "\n"); !strings.Contains(body, "gaps could not be read") {
		t.Fatalf("the failed gap read is not said:\n%s", body)
	}
}

func TestHistoryGapAcrossMidnightCarriesItsDate(t *testing.T) {
	from := time.Date(2026, 10, 31, 22, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	g := history.Entry{Stream: "gap", ID: histID(to), F: map[string]string{"of": "status", "from": histID(from), "to": histID(to)}}
	if got := histGapText(g, time.UTC); !strings.Contains(got, "Sat 10-31 22:00 to Sun 11-01 02:00") {
		t.Fatalf("%q", got)
	}
}

// o windows again what was already read: a restart gap whose stretch ends 5
// days ago, and live entries older than 3 days with no archive on this host
// (reviewer-codex).
func TestHistoryOlderWindowsGapsAndLiveAgain(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	gap := history.Entry{Stream: "gap", ID: histID(now.Add(-time.Hour)), F: map[string]string{"of": "*", "from": "unknown", "to": histID(now.AddDate(0, 0, -5))}}
	b, _ := json.Marshal(gap)
	os.WriteFile(filepath.Join(dir, "gaps.jsonl"), append(b, '\n'), 0o600)
	os.WriteFile(filepath.Join(dir, now.UTC().Format("2006-01-02")+".jsonl"), append(b, '\n'), 0o600)
	m := fixtureShell(t, 120, 40)
	m.cfg.histDir = dir
	m, cmd := pressCmd(t, m, 'H')
	next, _ := m.Update(cmd())
	m = next.(Model)
	if strings.Contains(strings.Join(histTexts(m.rd.top().body), "\n"), "archive restarted") {
		t.Fatal("a gap ending 5 days ago shows in the 3-day window")
	}
	m, cmd = pressCmd(t, m, 'o')
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(strings.Join(histTexts(m.rd.top().body), "\n"), "archive restarted") {
		t.Fatal("o does not show the gap its wider window covers")
	}

	// No archive: the live read at open holds a 4-day-old entry; o shows it.
	old := history.Entry{Stream: "cmd", ID: histID(now.AddDate(0, 0, -4)), F: map[string]string{"type": "directive", "command": "four days ago"}}
	m = fixtureShell(t, 120, 40)
	m, _ = pressCmd(t, m, 'H')
	next, _ = m.Update(historyMsg{serial: m.rd.top().serial, v: func() histView {
		v := histView{days: 3, loaded: true, noArchive: true, live: []history.Entry{old}}
		v.window(now)
		return v
	}()})
	m = next.(Model)
	m, cmd = pressCmd(t, m, 'o')
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(strings.Join(histTexts(m.rd.top().body), "\n"), "four days ago") {
		t.Fatal("o does not window the live entries again")
	}
}

// A capped first read keeps the file's tail, in write order: what is left may
// hold newer entries than the oldest shown, and the page says so, never "older
// lines" (reviewer-codex).
func TestHistoryCutNoticeSaysLinesMayBeNewer(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var es []history.Entry
	for i := 10; i < 20; i++ { // cmd's page first, newer ids
		es = append(es, history.Entry{Stream: "cmd", ID: histID(now.Add(-time.Hour + time.Duration(i)*time.Second)), F: map[string]string{"type": "directive", "command": "newer " + strings.Repeat("c", 40)}})
	}
	for i := 0; i < 10; i++ { // then status's page, older ids
		es = append(es, history.Entry{Stream: "status", ID: histID(now.Add(-time.Hour + time.Duration(i)*time.Second)), F: map[string]string{"agent": "coder", "state": "idle", "message": strings.Repeat("s", 40)}})
	}
	f, _ := os.Create(filepath.Join(dir, now.UTC().Format("2006-01-02")+".jsonl"))
	for _, e := range es {
		b, _ := json.Marshal(e)
		f.Write(append(b, '\n'))
	}
	f.Close()
	m := fixtureShell(t, 120, 40)
	m.cfg.histDir = dir
	m.histCap = 700 // the status block's tail only
	m, cmd := pressCmd(t, m, 'H')
	next, _ := m.Update(cmd())
	m = next.(Model)
	body := strings.Join(histTexts(m.rd.top().body), "\n")
	if strings.Contains(body, "newer cccc") {
		t.Fatalf("the fixture should leave the cmd block unread:\n%s", body)
	}
	if !strings.Contains(body, "more archive lines not read (in write order, some may be newer): o reads them") {
		t.Fatalf("the notice:\n%s", body)
	}
}

// history-view-lag: the rows are built once per load, filter and width, not
// once per key. Moving the selection (j, k, a held key's repeats) and a poll
// re-mark the selected row only; a new filter, width or load builds again.
func TestHistoryMoveDoesNotRebuildRows(t *testing.T) {
	now := time.Now()
	var es []history.Entry
	for i := range 200 {
		es = append(es, history.Entry{Stream: "cmd", ID: histID(now.Add(-time.Duration(200-i) * time.Minute)),
			F: map[string]string{"from": "master", "target": "coder", "type": "directive", "command": fmt.Sprintf("message %d", i)}})
	}
	m := fixtureShell(t, 120, 40)
	m.push(page{kind: ovHistory})
	v := histView{days: histDaysStep, loaded: true, entries: es}
	m.rd.top().hist, m.rd.top().dirty = &v, true
	m.refresh()
	builds := histRowBuilds
	selected := func(m Model) string {
		p := m.rd.top()
		l := p.body[p.hrows[p.sel].line]
		if l.text()[:len(GlyphSelect)] != GlyphSelect {
			t.Fatalf("row %d is not marked: %q", p.sel, l.text())
		}
		return l.text()
	}
	first := selected(m)
	for range 10 {
		m = keys(t, m, 'j')
	}
	if got := histRowBuilds - builds; got != 0 {
		t.Fatalf("10 moves built the rows %d times", got)
	}
	if p := m.rd.top(); p.sel != 10 || selected(m) == first {
		t.Fatalf("the selection did not move: sel %d", p.sel)
	}
	for _, it := range m.rd.top().hrows { // exactly one row marked
		if l := m.rd.top().body[it.line].text(); strings.HasPrefix(l, GlyphSelect) && it.line != m.rd.top().hrows[10].line {
			t.Fatalf("a second marked row: %q", l)
		}
	}
	m = keys(t, m, 'k')
	if m.rd.top().sel != 9 || histRowBuilds != builds {
		t.Fatalf("k: sel %d, builds %d", m.rd.top().sel, histRowBuilds-builds)
	}
	m.gen++ // a poll
	m.rd.top().dirty = true
	m.refresh()
	if histRowBuilds != builds {
		t.Fatal("a poll built the rows again")
	}
	m.rd.top().histFilter = parseFilter("message 1") // a new filter builds them
	m.rd.top().dirty = true
	m.refresh()
	if histRowBuilds != builds+1 {
		t.Fatalf("a new filter: %d builds", histRowBuilds-builds)
	}
	v2 := v // a new load builds them
	m.rd.top().hist, m.rd.top().dirty = &v2, true
	m.refresh()
	if histRowBuilds != builds+2 {
		t.Fatalf("a new load: %d builds", histRowBuilds-builds)
	}
	m.rd.top().hist.loaded, m.rd.top().dirty = false, true // o: a wider read under way, in place
	m.refresh()
	if histRowBuilds != builds+3 || !strings.Contains(strings.Join(histTexts(m.rd.top().body), "\n"), "loading") {
		t.Fatalf("o's read under way: %d builds", histRowBuilds-builds)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40}) // a new width builds them
	m = next.(Model)
	m.rd.top().dirty = true
	m.refresh()
	if histRowBuilds != builds+4 {
		t.Fatalf("a new width: %d builds", histRowBuilds-builds)
	}
}

// history-view-lag: a long entry (a full report) is cut before it is
// measured, and its row reads the same as the row of its first part.
func TestHistoryRowCutsLongTextFirst(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("word é 漢 ", 20000)
	row := func(text string) string {
		v := histView{days: histDaysStep, loaded: true, entries: []history.Entry{{Stream: "report", ID: histID(now),
			F: map[string]string{"agent": "coder", "kind": "note", "message": text}}}}
		_, body, items := historyBody(v, 0, time.UTC, 160)
		return body[items[0].line].text()
	}
	if a, b := row(long), row(long[:2000]); a != b {
		t.Fatalf("the cut changed the row:\n%q\n%q", a, b)
	}
}
