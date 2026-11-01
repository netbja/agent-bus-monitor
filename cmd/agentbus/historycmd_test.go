package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/history"
)

func hid(at time.Time) string { return strconv.FormatInt(at.UnixMilli(), 10) + "-0" }

// archiveFixture writes an archive in dir: a directive and its reply, a gap in
// status, and a report.
func archiveFixture(t *testing.T, dir string, at time.Time) []history.Entry {
	t.Helper()
	es := []history.Entry{
		{Stream: "cmd", ID: hid(at), F: map[string]string{"from": "master", "target": "coder", "type": "directive", "command": "Build the History page"}},
		{Stream: "gap", ID: hid(at.Add(time.Minute)), F: map[string]string{"of": "status", "from": hid(at.Add(-time.Hour)), "to": hid(at.Add(time.Minute))}},
		{Stream: "report", ID: hid(at.Add(2 * time.Minute)), F: map[string]string{"agent": "coder", "kind": "note", "message": "step 1\ndone"}},
	}
	os.MkdirAll(dir, 0o700)
	var b bytes.Buffer
	for _, e := range es {
		line, _ := json.Marshal(e)
		b.Write(append(line, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, at.UTC().Format("2006-01-02")+".jsonl"), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return es
}

func TestHistoryMergesTheArchiveWithTheLiveStreams(t *testing.T) {
	at := time.Date(2026, 11, 1, 17, 47, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "history")
	es := archiveFixture(t, dir, at)
	live := []history.Entry{es[0], // archived already: printed once
		{Stream: "cmd", ID: hid(at.Add(3 * time.Minute)), F: map[string]string{"from": "coder", "target": "master", "type": "reply", "ref": es[0].ID, "command": "queued"}}}
	d := historyDeps{Dir: dir, Now: func() time.Time { return at.Add(time.Hour) }, Loc: time.UTC,
		Live: func(context.Context) ([]history.Entry, error) { return live, nil }}
	var out, errb bytes.Buffer
	if code := runHistory(nil, d, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := []string{
		"Sun 11-01 17:47  master → coder  directive  Build the History page",
		"-- not archived: Sun 11-01 16:47 to 17:48 (status) --",
		"Sun 11-01 17:49  coder  report  step 1 done",
		"Sun 11-01 17:50  coder → master  reply  queued",
	}
	if got := strings.TrimSpace(out.String()); got != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}

	out.Reset()
	runHistory([]string{"--thread", es[0].ID}, d, &out, &errb)
	if got := strings.Count(out.String(), "\n"); got != 2 || strings.Contains(out.String(), "not archived") {
		t.Fatalf("the thread across archive and live:\n%s", out.String())
	}
	out.Reset()
	runHistory([]string{"--limit", "1"}, d, &out, &errb)
	if !strings.Contains(out.String(), "queued") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("limit keeps the newest:\n%s", out.String())
	}
}

func TestHistoryJSONRoundTripsTheArchiveLines(t *testing.T) {
	at := time.Date(2026, 11, 1, 17, 47, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "history")
	es := archiveFixture(t, dir, at)
	d := historyDeps{Dir: dir, Now: func() time.Time { return at.Add(time.Hour) }, Loc: time.UTC,
		Live: func(context.Context) ([]history.Entry, error) { return nil, nil }}
	var out, errb bytes.Buffer
	runHistory([]string{"--json"}, d, &out, &errb)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(es) {
		t.Fatalf("%d lines: %s", len(lines), out.String())
	}
	for i, l := range lines {
		var e history.Entry
		if err := json.Unmarshal([]byte(l), &e); err != nil || e.ID != es[i].ID || e.Stream != es[i].Stream || e.F["message"] != es[i].F["message"] {
			t.Errorf("line %d: %s (%v)", i, l, err)
		}
	}
}

func TestHistoryWithoutABrokerReadsTheArchiveAndSaysSo(t *testing.T) {
	at := time.Date(2026, 11, 1, 17, 47, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "history")
	archiveFixture(t, dir, at)
	d := historyDeps{Dir: dir, Now: func() time.Time { return at.Add(time.Hour) }, Loc: time.UTC,
		Live: func(context.Context) ([]history.Entry, error) { return nil, errors.New("dial: refused") }}
	var out, errb bytes.Buffer
	if code := runHistory(nil, d, &out, &errb); code != 0 || !strings.Contains(out.String(), "Build the History page") {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(errb.String(), "broker unreachable: archive only") {
		t.Fatalf("stderr: %s", errb.String())
	}
	// No archive on this host: the live streams only, said.
	d.Dir = filepath.Join(t.TempDir(), "none")
	d.Live = func(context.Context) ([]history.Entry, error) { return nil, nil }
	errb.Reset()
	runHistory(nil, d, &out, &errb)
	if !strings.Contains(errb.String(), "no archive on this host: live streams only") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestHistoryWhen(t *testing.T) {
	now := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		in       string
		end      bool
		want     time.Time
		wantFail bool
	}{
		{"3d", false, now.AddDate(0, 0, -3), false},
		{"90m", false, now.Add(-90 * time.Minute), false},
		{"2026-10-30", false, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), false},
		{"2026-10-30", true, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC).Add(-time.Millisecond), false},
		{"2026-10-30 14:20", false, time.Date(2026, 10, 30, 14, 20, 0, 0, time.UTC), false},
		{"14:20", false, time.Date(2026, 11, 1, 14, 20, 0, 0, time.UTC), false},
		{"yesterday", false, time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, c.end, now, time.UTC)
		if (err != nil) != c.wantFail || !got.Equal(c.want) {
			t.Errorf("%q end=%v: %v %v, want %v", c.in, c.end, got, err, c.want)
		}
	}
}

func TestHistoryRefusesAnUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runHistory([]string{"--bogus"}, historyDeps{Now: time.Now, Loc: time.UTC}, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

// Without --since the archive is read newest first under a cap, and a cut is
// said (foureyes); --since lifts the cap.
func TestHistoryCapsTheArchiveWithoutSince(t *testing.T) {
	at := time.Date(2026, 11, 1, 17, 47, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "history")
	archiveFixture(t, dir, at)
	old := filepath.Join(dir, "2026-10-01.jsonl")
	os.WriteFile(old, []byte(`{"stream":"cmd","id":"`+hid(at.AddDate(0, -1, 0))+`","f":{"command":"old"}}`+"\n"), 0o600)
	st, err := os.Stat(filepath.Join(dir, "2026-11-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	d := historyDeps{Dir: dir, Now: func() time.Time { return at.Add(time.Hour) }, Loc: time.UTC, Cap: st.Size(), // today's file only
		Live: func(context.Context) ([]history.Entry, error) { return nil, nil }}
	var out, errb bytes.Buffer
	runHistory(nil, d, &out, &errb)
	if strings.Contains(out.String(), "old") || !strings.Contains(errb.String(), "older archive not read") {
		t.Fatalf("capped:\n%s\n%s", out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	runHistory([]string{"--since", "60d"}, d, &out, &errb)
	if !strings.Contains(out.String(), "old") || strings.Contains(errb.String(), "older archive") {
		t.Fatalf("--since reads it all:\n%s\n%s", out.String(), errb.String())
	}
}

// A restart line written today for history before yesterday shows under
// --until yesterday (reviewer-codex).
func TestHistoryShowsAGapWrittenAfterTheQueriedDays(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	dir := filepath.Join(t.TempDir(), "history")
	os.MkdirAll(dir, 0o700)
	line := `{"stream":"gap","id":"` + hid(now) + `","f":{"of":"*","from":"unknown","to":"` + hid(now.Add(-36*time.Hour)) + `"}}` + "\n"
	os.WriteFile(filepath.Join(dir, "2026-11-01.jsonl"), []byte(line), 0o600)
	os.WriteFile(filepath.Join(dir, "gaps.jsonl"), []byte(line), 0o600)
	d := historyDeps{Dir: dir, Now: func() time.Time { return now }, Loc: time.UTC,
		Live: func(context.Context) ([]history.Entry, error) { return nil, nil }}
	var out, errb bytes.Buffer
	runHistory([]string{"--until", "2026-10-30"}, d, &out, &errb)
	if !strings.Contains(out.String(), "archive restarted: nothing archived before") {
		t.Fatalf("the restart line is hidden:\n%s\n%s", out.String(), errb.String())
	}
}
