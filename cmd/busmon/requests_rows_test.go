package main

import (
	"strings"
	"testing"
	"time"
)

func reqRowsText(items []ListItem) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Line.text() + "\n")
	}
	return b.String()
}

// The Requests tab in the one list pattern (busmon pages, Requests): the
// counter summary in the title, short section names, one line per group with
// its symbol, `you` for the person, kind, subject, the age at the edge; the
// notices and the done threads each folded into one line.
func TestRequestRowsInTheListPattern(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	v := requestGroups(reqFixture(now))
	sep := " " + GlyphNone + " "
	if got := requestsTitle(v, 140).text(); got != "22 waiting"+sep+"1 tracked"+sep+"9 gated"+sep+"11 between agents" {
		t.Errorf("title %q", got)
	}
	items := requestRows(v, reqOpen{}, now, 136)
	text := reqRowsText(items)
	order := []string{"You wait on", "you " + GlyphArrowR + " master", "Tracked", "busmon-group-a " + GlyphNone + " accepted",
		"Reviews gated", "Between agents", "notices" + sep + "22, nobody owes an answer  (enter shows them)",
		"done or answered" + sep + "77, older than 7 d folded  (o shows them)"}
	at := -1
	for _, w := range order {
		i := strings.Index(text, w)
		if i <= at {
			t.Fatalf("%q missing or out of order in:\n%s", w, text)
		}
		at = i
	}
	for _, gone := range []string{"FROM", "no reply on their thread", "charlie", "Owed by you"} { // no column header, no sentence, the person is `you`, an empty section is not drawn
		if strings.Contains(text, gone) {
			t.Errorf("%q still drawn:\n%s", gone, text)
		}
	}
	for _, it := range items {
		if w := it.Line.Width(); w > 136 {
			t.Errorf("row of %d cells: %q", w, it.Line.text())
		}
		line := it.Line.text()
		switch {
		case strings.Contains(line, "ui-kimi"): // unread by its target
			if !strings.HasPrefix(line, GlyphDotFull+" ") {
				t.Errorf("unread row %q", line)
			}
		case strings.Contains(line, "sentinel "+GlyphArrowR+" master"):
			if !strings.HasPrefix(line, GlyphDotEmpty+" ") || !strings.Contains(line, "(+5)") {
				t.Errorf("read group row %q", line)
			}
		case strings.Contains(line, "reviewer-codex"): // held by its quota: that first, in red (reviewer-codex)
			if !strings.HasPrefix(line, GlyphDotFull+" ") || it.Line[0].Role != RoleError {
				t.Errorf("held gated review row %q", line)
			}
		}
	}
	// At 80 the kind shortens and nothing passes the width.
	for _, it := range requestRows(v, reqOpen{}, now, 66) {
		if w := it.Line.Width(); w > 66 {
			t.Errorf("narrow row of %d cells: %q", w, it.Line.text())
		}
	}
	if got := reqRowsText(requestRows(v, reqOpen{}, now, 66)); !strings.Contains(got, " dir ") {
		t.Errorf("narrow kind not short:\n%s", got)
	}
}

// One row per open question set, owed by the person; Enter answers it.
func TestRequestRowsListTheOpenAsks(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	in := reqFixture(now)
	in.asks = []reqAsk{{ID: "1-0", From: "master", Title: "review lot 1?", Open: 2, At: now.Add(-4 * time.Minute)}}
	in.questions = 2
	v := requestGroups(in)
	items := requestRows(v, reqOpen{}, now, 136)
	text := reqRowsText(items)
	if !strings.Contains(text, "Owed by you") || !strings.Contains(text, "master "+GlyphArrowR+" you") || !strings.Contains(text, "review lot 1? (2 questions)") {
		t.Fatalf("ask row missing:\n%s", text)
	}
	found := false
	for _, it := range items {
		found = found || it.Key == "ask:1-0" && it.Selectable
	}
	if !found {
		t.Error("the ask row is not selectable as ask:1-0")
	}
	if got := requestsTitle(v, 140).text(); !strings.HasPrefix(got, "2 owed by you") {
		t.Errorf("title %q", got)
	}
}

// The sentence a heading carried is the first line of the page a row opens.
func TestRequestPageLeadsWithItsSection(t *testing.T) {
	m := reqShell(t, 120, 40, nil)
	m.lists.requests.Select(hasKey(m, "thr:"))
	m = keys(t, m, kEnter)
	if !m.rd.open() || m.rd.top().kind != ovMessage {
		t.Fatal("no thread page")
	}
	if got := m.rd.top().body[0].text(); !strings.HasSuffix(got, ".") || !strings.Contains(got, ":") {
		t.Errorf("thread page's first line %q", got)
	}
	m.closeReader()
	m.lists.requests.Select("req:busmon-group-a")
	m = keys(t, m, kEnter)
	if got := m.rd.top().body[0].text(); got != reqSectionNote(secTracked) {
		t.Errorf("request page's first line %q", got)
	}
}

// An expanded group's threads keep their own markers: one unread among read.
func TestRequestRowsExpandedThreadsKeepTheirMarkers(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	in := reqFixture(now)
	var last string
	for _, e := range in.cmds {
		if e.From == "sentinel" && e.Target == "master" {
			last = e.ID
		}
	}
	d := in.delivery["master"]
	d.pending = map[string]bool{last: true} // the newest sentinel → master entry is unread
	in.delivery["master"] = d
	v := requestGroups(in)
	var key string
	for _, g := range v.groups[secDirectives] {
		if g.From == "sentinel" {
			key = g.Key()
		}
	}
	items := requestRows(v, reqOpen{groups: map[string]bool{key: true}}, now, 136)
	unread, read := 0, 0
	for _, it := range items {
		if !strings.HasPrefix(it.Key, "thr:") || !strings.Contains(it.Line.text(), "sentinel "+GlyphArrowR+" master") {
			continue
		}
		switch {
		case strings.HasPrefix(it.Line.text(), GlyphDotFull+" "):
			unread++
		case strings.HasPrefix(it.Line.text(), GlyphDotEmpty+" "):
			read++
		}
	}
	if unread != 1 || read != 5 {
		t.Errorf("expanded threads: %d unread, %d read; want 1 and 5", unread, read)
	}
}
