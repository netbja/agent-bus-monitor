package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// auditWriter is a writer that reads the audit stream (*bus.Bus has it).
type auditWriter struct {
	*fakeWriter
	es    []bus.AuditEntry
	err   error
	asked int64
}

func (w *auditWriter) AuditLast(_ context.Context, count int64) ([]bus.AuditEntry, error) {
	w.asked = count
	return w.es, w.err
}

// auditFixture is newest first, as AuditLast returns it.
func auditFixture() []bus.AuditEntry {
	at := func(h, m int) int64 { return time.Date(2026, 11, 25, h, m, 0, 0, time.UTC).UnixMilli() }
	return []bus.AuditEntry{
		{ID: "3-0", At: at(18, 40), Action: "merge", Actor: "dispatch", Target: "coder2/audit-build-2b", Outcome: bus.AuditOK, Detail: "head 1d8dabc9 into dev"},
		{ID: "2-0", At: at(18, 31), Action: "answer", Actor: "charlie", Human: "yes", Typed: "yes", Target: "ask a1", Ref: "1795634829254-0", Outcome: bus.AuditOK, Detail: "question q1"},
		{ID: "1-0", At: at(17, 5), Action: "approve", Actor: "foureyes", Target: "audit-step2b", Ref: "1795634963449-0", Outcome: bus.AuditOK, Detail: "head 1d8dabc9"},
	}
}

func auditShell(t *testing.T, w, h int, aw *auditWriter) Model {
	t.Helper()
	pinLocal(t)
	m := fixtureShell(t, w, h)
	m.cfg.w = aw
	m, _ = pressCmd(t, m, 'H')
	m, cmd := pressCmd(t, m, 'a')
	if p := m.rd.top(); p == nil || p.kind != ovAudit || cmd == nil {
		t.Fatal("a on History opened no Audit page with a load")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func auditText(m Model) string { return strings.Join(histTexts(m.rd.top().body), "\n") }

// a on History opens the Audit list: the last 200 entries, newest first,
// read in a command; esc goes back to History.
func TestAuditPageOpensFromHistory(t *testing.T) {
	aw := &auditWriter{fakeWriter: &fakeWriter{}, es: auditFixture()}
	m := auditShell(t, 120, 40, aw)
	if aw.asked != 200 {
		t.Errorf("read %d entries, want the last 200", aw.asked)
	}
	p := m.rd.top()
	if p.title != "Audit "+GlyphNone+" 3 entries" {
		t.Errorf("title %q", p.title)
	}
	got := auditText(m)
	for _, w := range []string{"11-25 18:40  merge", "dispatch", "coder2/audit-build-2b", "head 1d8dabc9 into dev", "charlie (person)", "foureyes"} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	if strings.Index(got, "18:40") > strings.Index(got, "17:05") {
		t.Errorf("not newest first:\n%s", got)
	}
	m, _ = pressCmd(t, m, 'q')
	if p := m.rd.top(); p == nil || p.kind != ovHistory {
		t.Fatalf("esc did not go back to History")
	}
}

// tab shows one action, then the next present, in the spec's order, then
// every action again.
func TestAuditPageFiltersByAction(t *testing.T) {
	m := auditShell(t, 120, 40, &auditWriter{fakeWriter: &fakeWriter{}, es: auditFixture()})
	for _, want := range []string{"approve", "answer", "merge", ""} {
		m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyTab})
		p := m.rd.top()
		if want == "" {
			if len(p.aitems) != 3 || strings.Contains(p.title, "action:") {
				t.Fatalf("all again: %q %d rows", p.title, len(p.aitems))
			}
			continue
		}
		if len(p.aitems) != 1 || !strings.HasSuffix(p.title, "1 entries "+GlyphNone+" action: "+want) || !strings.Contains(auditText(m), want) {
			t.Fatalf("tab to %s: %q\n%s", want, p.title, auditText(m))
		}
	}
}

// enter opens the selected entry in place, its ref first; enter again
// closes it.
func TestAuditPageEnterShowsTheRef(t *testing.T) {
	m := auditShell(t, 120, 40, &auditWriter{fakeWriter: &fakeWriter{}, es: auditFixture()})
	m, _ = pressCmd(t, m, 'j')
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	got := auditText(m)
	ref := strings.Index(got, "ref     1795634829254-0")
	if ref < 0 || !strings.Contains(got, "by      a person's entry, typed") || strings.Index(got, "charlie") > ref {
		t.Fatalf("opened:\n%s", got)
	}
	if strings.Contains(got, "1795634963449-0") {
		t.Errorf("an entry not selected was opened too:\n%s", got)
	}
	if m.rd.top().kind != ovAudit {
		t.Fatal("enter left the page")
	}
	m, _ = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	if strings.Contains(auditText(m), "1795634829254-0") {
		t.Errorf("enter again did not close it:\n%s", auditText(m))
	}
}

// A click selects a row; a second click on it opens it.
func TestAuditPageClick(t *testing.T) {
	m := auditShell(t, 120, 40, &auditWriter{fakeWriter: &fakeWriter{}, es: auditFixture()})
	y := -1
	for i, l := range strings.Split(m.grid().plain(), "\n") {
		if strings.Contains(l, "foureyes") {
			y = i
		}
	}
	if y < 0 {
		t.Fatalf("no foureyes row:\n%s", m.grid().plain())
	}
	m, _ = clickCmd(t, m, 20, y)
	if m.rd.top().sel != 2 {
		t.Fatalf("click selected %d", m.rd.top().sel)
	}
	m, _ = clickCmd(t, m, 20, y)
	if !strings.Contains(auditText(m), "ref     1795634963449-0") {
		t.Fatalf("second click:\n%s", auditText(m))
	}
}

// A read that fails, or no broker, is said; the page never writes.
func TestAuditPageSaysAFailedRead(t *testing.T) {
	fw := &fakeWriter{}
	m := auditShell(t, 120, 40, &auditWriter{fakeWriter: fw, err: errors.New("dial: refused")})
	if got := auditText(m); !strings.Contains(got, "the audit stream could not be read: dial: refused") {
		t.Fatalf("failed read:\n%s", got)
	}
	if len(fw.sent) != 0 {
		t.Errorf("the page wrote: %v", fw.sent)
	}
	_, body, items := auditBody(auditView{}, 0, time.UTC, 120)
	if len(items) != 0 || !strings.Contains(strings.Join(histTexts(body), "\n"), "loading") {
		t.Errorf("before the load: %v", histTexts(body))
	}
	_, body, _ = auditBody(auditView{loaded: true}, 0, time.UTC, 120)
	if !strings.Contains(strings.Join(histTexts(body), "\n"), "no audit entry") {
		t.Errorf("empty: %v", histTexts(body))
	}
}

// Below auditWide a row drops its detail; no row is wider than the page.
func TestAuditPageNarrow(t *testing.T) {
	v := auditView{entries: auditFixture(), loaded: true, open: map[string]bool{"2-0": true}}
	_, body, _ := auditBody(v, 0, time.UTC, 70)
	got := strings.Join(histTexts(body), "\n")
	if strings.Contains(got, "head 1d8dabc9 into dev") || !strings.Contains(got, "coder2/audit") {
		t.Errorf("narrow:\n%s", got)
	}
	for _, l := range body {
		if l.Width() > 70 {
			t.Errorf("wider than 70: %q", l.text())
		}
	}
}

// The page at 120 columns and in the narrow tier.
func TestAuditPageGoldens(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {70, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := auditShell(t, sz[0], sz[1], &auditWriter{fakeWriter: &fakeWriter{}, es: auditFixture()})
			m.paint = newPainter(th)
			checkGolden(t, fmt.Sprintf("audit-%dx%d.txt", sz[0], sz[1]), m.grid().plain()+"\n")
		})
	}
}
