package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// recapNotices is the screenshot's shape: report notices to the person, two
// with the same text, one older than a day, and one plain directive.
func recapNotices(now time.Time) recapInput {
	id := func(ago time.Duration, seq int) string {
		return fmt.Sprintf("%d-%d", now.Add(-ago).UnixMilli(), seq)
	}
	notice := func(rid, text string) string { return reportPrefix(rid) + text }
	r1, r2, r3, r4 := id(23*time.Hour, 9), id(22*time.Hour, 8), id(22*time.Hour-time.Minute, 8), id(30*time.Hour, 9)
	return recapInput{Now: now, Since: now.Add(-48 * time.Hour), Human: "charlie",
		Agents: map[string]*agentState{"master": {state: "idle", registered: true, provider: "anthropic"}},
		Commands: []bus.Event{
			{ID: id(30*time.Hour, 0), Kind: "cmd", From: "master", Target: "charlie", Type: bus.CmdDirective, Message: notice(r4, "an old one")},
			{ID: id(23*time.Hour, 0), Kind: "cmd", From: "master", Target: "charlie", Type: bus.CmdDirective, Message: notice(r1, "Migration C is in PR")},
			{ID: id(22*time.Hour, 0), Kind: "cmd", From: "master", Target: "charlie", Type: bus.CmdDirective, Message: notice(r2, "The mock-ups are in PR 212")},
			{ID: id(22*time.Hour-time.Minute, 0), Kind: "cmd", From: "master", Target: "charlie", Type: bus.CmdDirective, Message: notice(r3, "The mock-ups are in PR 212")},
			{ID: id(time.Hour, 0), Kind: "cmd", From: "coder", Target: "charlie", Type: bus.CmdDirective, Message: "Need a decision"},
		},
		Reports: []bus.Event{
			{ID: r1, Kind: "report", Agent: "master", Message: "Migration C is in PR", Full: "Migration C is in PR\nfull text one"},
			{ID: r3, Kind: "report", Agent: "master", Message: "The mock-ups are in PR 212", Full: "The mock-ups are in PR 212\nall pages"},
		},
	}
}

func forYouItems(rows []recapRow) []recapRow {
	var out []recapRow
	for _, r := range rows {
		if r.Section == recapForYou && strings.HasPrefix(r.Key, "cmd:") {
			out = append(out, r)
		}
	}
	return out
}

// For you in the list format (recap-foryou): newest first, the report's own
// preview (never the notice's envelope nor its id), identical items merged
// with their count, the ones older than a day marked to fold.
func TestRecapForYouItems(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	in := recapNotices(now)
	items := forYouItems(buildRecap(in).Rows)
	var got []string
	for _, r := range items {
		got = append(got, r.Name+"|"+r.Text+"|"+strings.Repeat("+", r.Count-1)+"|"+map[bool]string{true: "older"}[r.Older])
		if strings.Contains(r.Text, "Report ") || strings.Contains(r.Text, "full text") {
			t.Errorf("envelope kept: %q", r.Text)
		}
	}
	want := []string{"coder|Need a decision||", "master|The mock-ups are in PR 212|+|", "master|Migration C is in PR||", "master|an old one||older"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, newest, _ := reportSubject(in.Commands[3].Message); items[1].ReportID != newest || items[1].Report.Full == "" {
		t.Fatalf("the merged item keeps its newest report: %+v", items[1])
	}
	if items[0].Report.ID != "" {
		t.Fatalf("a plain directive has no report: %+v", items[0])
	}
}

// One line per item: the sender, its text with (N), the age at the right
// edge (a merged item's newest, 21h59 here); the older ones folded behind '+ N older' until o.
func TestRecapForYouLines(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	rec := buildRecap(recapNotices(now))
	folded := recapFold(rec, false)
	lines, _ := recapBody(folded.Rows, -1, 100)
	text := tokLinesText(lines)
	if strings.Contains(text, "an old one") || !strings.Contains(text, "+ 1 older") {
		t.Fatalf("folded:\n%s", text)
	}
	var mock string
	for _, l := range lines {
		if strings.Contains(l.text(), "mock-ups") {
			mock = l.text()
		}
		if l.Width() > 100 {
			t.Errorf("line of %d cells: %q", l.Width(), l.text())
		}
	}
	if !strings.HasPrefix(mock, "    master") || !strings.Contains(mock, "The mock-ups are in PR 212 (2)") || !strings.HasSuffix(mock, "21h") || Width(mock) != 100 {
		t.Fatalf("item line %q", mock)
	}
	if text := tokLinesText(recapBodyLines(recapFold(rec, true).Rows)); !strings.Contains(text, "an old one") || strings.Contains(text, "older") {
		t.Fatalf("shown:\n%s", text)
	}
}

func recapBodyLines(rows []recapRow) []Line {
	lines, _ := recapBody(rows, -1, 100)
	return lines
}

// On the page: o shows the older items, Enter opens the report's full text,
// Y copies it.
func TestRecapForYouKeys(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	m := reqShell(t, 70, 40, nil) // narrow: the recap shows For you
	m.gotRecap(recapMsg{model: buildRecap(recapNotices(now))})
	m.maybeOpenRecap()
	p := m.rd.top()
	if p == nil || p.kind != ovRecap {
		t.Fatal("recap not open")
	}
	if strings.Contains(m.grid().plain(), "an old one") {
		t.Fatal("an older item shows before o")
	}
	m = keys(t, m, 'o')
	if !strings.Contains(tokLinesText(m.rd.top().body), "an old one") {
		t.Fatal("o did not show the older items")
	}
	p = m.rd.top()
	for i, r := range p.recap.Rows {
		if strings.Contains(r.Text, "mock-ups") {
			p.sel = i
		}
	}
	p.dirty = true
	m.refresh()
	if !m.pageOffers(ActCopyThread) || !m.pageOffers(ActOlder) {
		t.Fatal("the footer does not offer Y and o on the recap")
	}
	m = keys(t, m, 'Y')
	if !strings.Contains(m.ui.flash, "copied") {
		t.Fatalf("Y copied nothing: %q", m.ui.flash)
	}
	m = keys(t, m, kEnter)
	if top := m.rd.top(); top == nil || top.kind == ovRecap || !strings.Contains(tokLinesText(top.body), "all pages") {
		t.Fatalf("Enter did not open the report's full text: %+v", top)
	}
}

// An unread entry that is also a For you item is that item, marked unread
// (foureyes): one line, never a second 'unread:' row; an unread entry with no
// item (a notice for the person) keeps its own row.
func TestRecapForYouUnreadOnce(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	in := recapNotices(now)
	direct := in.Commands[4] // coder -> charlie "Need a decision"
	notice := bus.Event{ID: fmt.Sprintf("%d-0", now.Add(-time.Minute).UnixMilli()), Kind: "notify", From: "dispatch", For: bus.ForPerson, Message: "the dispatcher wants you"}
	in.Unread = []bus.Event{direct, notice}
	rec := buildRecap(in)
	lines, _ := recapBody(recapFold(rec, false).Rows, -1, 100)
	text := tokLinesText(lines)
	if n := strings.Count(text, "Need a decision"); n != 1 {
		t.Fatalf("the unread directive shows %d times:\n%s", n, text)
	}
	if !strings.Contains(text, "the dispatcher wants you") {
		t.Fatalf("an unread entry with no item lost its row:\n%s", text)
	}
	for _, l := range lines {
		if s := l.text(); strings.Contains(s, "Need a decision") && !strings.HasPrefix(s, "  "+GlyphDotFull+" coder") {
			t.Fatalf("the item is not marked unread: %q", s)
		}
		if s := l.text(); strings.Contains(s, "Migration C") && strings.Contains(s, GlyphDotFull) {
			t.Fatalf("a read item is marked unread: %q", s)
		}
	}
}

// Every item has its age (reviewer-codex): an ask from its creation, a
// blocked agent from its declaration.
func TestRecapForYouAges(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	in := recapNotices(now)
	in.Asks = []bus.Ask{{ID: "a1", From: "coder", State: "open", CreatedAt: now.Add(-2 * time.Hour).UnixMilli(),
		Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1", Header: "Review", Question: "Approve?"}}}}}
	in.Agents["coder"] = &agentState{state: "blocked", message: "tests fail", stateAt: now.Add(-5 * time.Minute), registered: true}
	lines, _ := recapBody(buildRecap(in).Rows, -1, 100)
	for _, l := range lines {
		s := l.text()
		if strings.Contains(s, "asks:") && !strings.HasSuffix(s, " 2h") || strings.Contains(s, "blocked:") && !strings.HasSuffix(s, " 5m") {
			t.Errorf("no age: %q", s)
		}
	}
}

// A report notice whose report is no longer retained keeps its preview;
// Enter says the report is gone, never the notice's envelope, and Y copies
// nothing (reviewer-codex).
func TestRecapForYouReportGone(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	in := recapNotices(now)
	in.Reports = nil
	m := reqShell(t, 70, 40, nil)
	m.gotRecap(recapMsg{model: buildRecap(in)})
	m.maybeOpenRecap()
	p := m.rd.top()
	for i, r := range p.recap.Rows {
		if strings.Contains(r.Text, "Migration C") {
			p.sel = i
		}
	}
	p.dirty = true
	m.refresh()
	if m.pageOffers(ActCopyThread) {
		t.Fatal("Y offered for a report no longer retained")
	}
	m = keys(t, m, 'Y')
	if strings.Contains(m.ui.flash, "copied") || !strings.Contains(m.ui.flash, "no longer retained") {
		t.Fatalf("Y: %q", m.ui.flash)
	}
	m = keys(t, m, kEnter)
	body := tokLinesText(m.rd.top().body)
	if !strings.Contains(body, "no longer retained") || !strings.Contains(body, "Migration C is in PR") || strings.Contains(body, "full text: agentbus") {
		t.Fatalf("Enter on a report gone:\n%s", body)
	}
}
