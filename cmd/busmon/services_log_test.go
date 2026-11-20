package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// services-restart-buttons §4: the log page reads. A fixture mixing the
// dispatcher's JSON decisions and the service's text lines.
const svcLogFixture = `2026-11-20 12:46:24 service: starting dispatch for demo (pid 42)
{"action":"none","agent":"coder","high_id":"","latency_ms":0,"reason":"no obligation","rule":1}
{"action":"escalate","agent":"coder2","high_id":"1-0","latency_ms":9,"reason":"no reply in 2h","rule":7,"ts":"2026-11-20T12:50:00+01:00"}
{"action":"none","agent":"coder2","high_id":"","latency_ms":0,"reason":"within retry window","rule":5}
{"action":"defer","agent":"master","reason":"pane busy","rule":3}
{"agent":"coder","op":"read pause","error":"connection refused","ts":"2026-11-20T12:51:00+01:00"}
2026-11-20 12:52:00 service: gave up: 5 failed starts in 10m0s
`

func TestServiceLogLines(t *testing.T) {
	var rows []svcLogLine
	for _, l := range strings.Split(strings.TrimSpace(svcLogFixture), "\n") {
		rows = append(rows, parseSvcLogLine(l))
	}
	for i, want := range []struct {
		role    Role
		routine bool
		text    string
	}{
		{RoleText, false, "2026-11-20 12:46:24 service: starting dispatch for demo (pid 42)"},
		{RoleMuted, true, "none  coder  no obligation  (rule 1)"},
		{RoleAttention, false, "escalate  coder2  no reply in 2h  (rule 7)"},
		{RoleMuted, true, "none  coder2  within retry window  (rule 5)"},
		{RoleText, false, "defer  master  pane busy  (rule 3)"},
		{RoleError, false, "coder  read pause: connection refused"},
		{RoleError, false, "2026-11-20 12:52:00 service: gave up: 5 failed starts in 10m0s"},
	} {
		got := svcLogRow(rows[i], 200).text()
		if rows[i].role != want.role || rows[i].routine != want.routine || !strings.Contains(got, want.text) {
			t.Errorf("line %d: role %v routine %v %q, want %v %v %q", i, rows[i].role, rows[i].routine, got, want.role, want.routine, want.text)
		}
		if strings.Contains(got, "high_id") || strings.Contains(got, "latency") {
			t.Errorf("line %d keeps the raw fields: %q", i, got)
		}
	}
	// A decision with ts shows its local time first, as a text line does.
	at, _ := time.Parse(time.RFC3339, "2026-11-20T12:50:00+01:00")
	if got := svcLogRow(rows[2], 200).text(); !strings.HasPrefix(got, at.Local().Format("2006-01-02 15:04:05")+"  escalate") {
		t.Errorf("escalate row %q lacks its time", got)
	}
	// An older decision (no ts) shows none: the field order starts at once.
	if got := svcLogRow(rows[4], 200).text(); !strings.HasPrefix(got, "defer  master") {
		t.Errorf("defer row %q", got)
	}
}

// The page reads back until it holds want visible lines (the routine ones
// folded), within a byte cap; the rotated file is read too.
func TestServiceLogReadsBackToVisibleLines(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "dispatch.log")
	none := `{"action":"none","agent":"coder","reason":"no obligation","rule":1}` + "\n"
	var older, newer strings.Builder
	for i := range 30 {
		fmt.Fprintf(&older, `{"action":"escalate","agent":"a%d","reason":"r","rule":7}`+"\n", i)
	}
	older.WriteString(strings.Repeat(none, 100))
	newer.WriteString(strings.Repeat(none, 5000)) // a tail made only of routine lines
	os.WriteFile(log+".1", []byte(older.String()), 0o600)
	os.WriteFile(log, []byte(newer.String()), 0o600)
	lines, err := readSvcLog(log, 20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	visible := 0
	for _, l := range lines {
		if !l.routine {
			visible++
		}
	}
	if visible != 20 || !strings.Contains(lines[0].raw, `"a10"`) {
		t.Fatalf("visible %d, first %q: want the 20 newest escalations, a10 to a29", visible, lines[0].raw)
	}
	// The cap stops the read: a 4 KiB cap never reaches the rotated file.
	capped, _ := readSvcLog(log, 20, 4096)
	for _, l := range capped {
		if !l.routine {
			t.Fatalf("a capped read reached %q", l.raw)
		}
	}
	if len(capped) == 0 || len(capped) > 4096/len(none)+1 {
		t.Fatalf("capped read: %d lines", len(capped))
	}
}

// The page: routine lines folded and counted in the title, a shows them,
// f filters by a word, esc clears the filter before it closes the page.
func TestServiceLogPageKeys(t *testing.T) {
	dir := t.TempDir()
	host, _ := os.Hostname()
	logD := filepath.Join(dir, "dispatch.log")
	os.WriteFile(logD, []byte(svcLogFixture), 0o600)
	m := fixtureShell(t, 140, 40)
	m.snap.svcInst = &bus.ServicesInstall{Manager: "systemd", Host: host}
	m.snap.svcRecs = map[string]bus.ServiceRecord{"dispatch": {State: "ready", Host: host, Log: logD}}
	next, cmd := m.Update(keyMsg(tea.Key{Code: tea.KeyF3}))
	m = run(t, next.(Model), cmd)
	next, cmd = m.Update(keyMsg(kEnter))
	m = run(t, next.(Model), cmd)
	p := m.rd.top()
	if p.kind != ovServiceLog {
		t.Fatalf("enter opened %v", p.kind)
	}
	body := plainOf(p.body)
	if strings.Contains(body, "no obligation") || !strings.Contains(body, "escalate  coder2") {
		t.Fatalf("routine lines not folded:\n%s", body)
	}
	if !strings.Contains(p.title, "5 lines") || !strings.Contains(p.title, "2 routine hidden (a)") {
		t.Fatalf("title %q", p.title)
	}
	m = keys(t, m, 'a')
	if body := plainOf(m.rd.top().body); !strings.Contains(body, "no obligation") {
		t.Fatalf("a did not show the routine lines:\n%s", body)
	}
	m = keys(t, m, 'a', 'f')
	for _, r := range "master" {
		m = keys(t, m, r)
	}
	m = keys(t, m, kEnter)
	body = plainOf(m.rd.top().body)
	if !strings.Contains(body, "defer  master") || strings.Contains(body, "escalate") {
		t.Fatalf("filter master:\n%s", body)
	}
	if m = keys(t, m, kEsc); m.rd.top() == nil || m.rd.top().kind != ovServiceLog || !strings.Contains(plainOf(m.rd.top().body), "escalate") {
		t.Fatal("esc did not clear the filter first")
	}
	if m = keys(t, m, kEsc); m.rd.top() == nil || m.rd.top().kind != ovServices {
		t.Fatal("esc did not return to the cards")
	}
}
