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
	for _, w := range []string{"older lines not loaded", "no archive on this host", "broker unreachable"} {
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
