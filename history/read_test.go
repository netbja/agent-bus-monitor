package history

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeDay(t *testing.T, dir, day string, es ...Entry) {
	t.Helper()
	os.MkdirAll(dir, 0o700)
	a := newAppender(dir)
	defer a.close()
	for _, e := range es {
		if err := a.write(e); err != nil {
			t.Fatal(err)
		}
	}
	_ = day
}

func id(at time.Time, seq int) string {
	return strings.Join([]string{itoa(at.UnixMilli()), itoa(int64(seq))}, "-")
}

func TestReadDaysInRangeOldestFirstAndCapped(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	d3 := d1.AddDate(0, 0, 2)
	writeDay(t, dir, "", Entry{Stream: "cmd", ID: id(d1, 0), F: map[string]string{"command": "one"}})
	writeDay(t, dir, "", Entry{Stream: "cmd", ID: id(d2, 0), F: map[string]string{"command": "two"}},
		Entry{Stream: "cmd", ID: id(d2.Add(time.Hour), 0), F: map[string]string{"command": "three"}})
	writeDay(t, dir, "", Entry{Stream: "cmd", ID: id(d3, 0), F: map[string]string{"command": "four"}})
	es, cut, err := ReadDays(dir, d2, d3, 0)
	if err != nil || cut || len(es) != 3 || es[0].F["command"] != "two" || es[2].F["command"] != "four" {
		t.Fatalf("days 2-3: %+v cut %v err %v", es, cut, err)
	}
	// A cap that holds the newest day and one line of the day before: the older
	// day is read from its end.
	size := func(day time.Time) int64 {
		st, _ := os.Stat(filepath.Join(dir, day.Format(dayLayout)+".jsonl"))
		return st.Size()
	}
	cap := size(d3) + size(d2)/2 + 2
	es, cut, err = ReadDays(dir, d1, d3, cap)
	if err != nil || !cut || len(es) != 2 || es[0].F["command"] != "three" || es[1].F["command"] != "four" {
		t.Fatalf("capped: %+v cut %v err %v", es, cut, err)
	}
	// A missing directory is no archive, not an error.
	if es, _, err := ReadDays(filepath.Join(dir, "none"), d1, d3, 0); err != nil || len(es) != 0 {
		t.Fatalf("missing: %v %v", es, err)
	}
}

func TestMergeDedupesByStreamAndID(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	archive := []Entry{
		{Stream: "cmd", ID: id(at, 0), F: map[string]string{"command": "a"}},
		{Stream: "report", ID: id(at, 0), F: map[string]string{"message": "same id, other stream"}},
	}
	live := []Entry{
		{Stream: "cmd", ID: id(at, 0), F: map[string]string{"command": "a"}},
		{Stream: "status", ID: id(at.Add(-time.Minute), 0), F: map[string]string{"state": "idle"}},
		{Stream: "cmd", ID: id(at.Add(time.Minute), 0), F: map[string]string{"command": "b"}},
	}
	got := Merge(archive, live)
	if len(got) != 4 {
		t.Fatalf("want 4 once each: %+v", got)
	}
	if got[0].Stream != "status" || got[3].F["command"] != "b" {
		t.Fatalf("ordered by id: %+v", got)
	}
}

func TestFilterMatches(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	dir := Entry{Stream: "cmd", ID: id(at, 0), F: map[string]string{"from": "master", "target": "coder", "type": "directive", "command": "Build the History page"}}
	reply := Entry{Stream: "cmd", ID: id(at.Add(time.Minute), 0), F: map[string]string{"from": "coder", "target": "master", "type": "reply", "ref": dir.ID, "command": "queued"}}
	rep := Entry{Stream: "report", ID: id(at, 1), F: map[string]string{"agent": "coder", "kind": "note", "message": "done"}}
	st := Entry{Stream: "status", ID: id(at, 2), F: map[string]string{"agent": "foureyes", "state": "idle"}}
	hl := Entry{Stream: "health:log", ID: id(at, 3), F: map[string]string{"check": "github", "status": "warn"}}
	cases := []struct {
		f    Filter
		want []Entry
	}{
		{Filter{Agent: "coder"}, []Entry{dir, reply, rep}},
		{Filter{Thread: dir.ID}, []Entry{dir, reply}},
		{Filter{Kind: "reply"}, []Entry{reply}},
		{Filter{Kind: "status"}, []Entry{st}},
		{Filter{Kind: "health"}, []Entry{hl}},
		{Filter{Grep: "HISTORY page"}, []Entry{dir}},
		{Filter{Since: at.Add(30 * time.Second)}, []Entry{reply}},
		{Filter{Until: at.Add(30 * time.Second)}, []Entry{dir, rep, st, hl}},
	}
	for _, c := range cases {
		var got []Entry
		for _, e := range []Entry{dir, reply, rep, st, hl} {
			if c.f.Match(e) {
				got = append(got, e)
			}
		}
		if len(got) != len(c.want) {
			t.Errorf("%+v: got %d %+v", c.f, len(got), got)
			continue
		}
		for i := range got {
			if got[i].ID != c.want[i].ID || got[i].Stream != c.want[i].Stream {
				t.Errorf("%+v: [%d] %s/%s", c.f, i, got[i].Stream, got[i].ID)
			}
		}
	}
	// The cmd copy of a verdict is a notice; the ledger's entry is the verdict
	// (foureyes: --kind verdict printed each twice).
	vcmd := Entry{Stream: "cmd", ID: id(at, 5), F: map[string]string{"from": "foureyes", "target": "coder", "type": "verdict", "command": "approve"}}
	vled := Entry{Stream: "verdicts", ID: id(at, 6), F: map[string]string{"reviewer": "foureyes", "author": "coder", "decision": "approve"}}
	if (Filter{Kind: "verdict"}).Match(vcmd) || !(Filter{Kind: "verdict"}).Match(vled) || Kind(vcmd) != "verdict-notice" {
		t.Error("--kind verdict is the ledger's entry only")
	}
	// --grep reads the text, not the names (foureyes).
	if (Filter{Grep: "master"}).Match(dir) || !(Filter{Grep: "history"}).Match(dir) {
		t.Error("--grep matched a name field, or missed the text")
	}
	gap := Entry{Stream: GapStream, ID: id(at, 9), F: map[string]string{"of": "status", "from": "1-0", "to": id(at, 9)}}
	if !(Filter{Kind: "status"}).Match(gap) || (Filter{Kind: "report"}).Match(gap) || (Filter{Thread: "x"}).Match(gap) {
		t.Error("a gap shows for its own stream's kind, never under a thread or a grep")
	}
	if !(Filter{Agent: "coder"}).Match(gap) {
		t.Error("a gap shows under an agent filter: its entries may have been the agent's")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A gap matches the time bounds by the stretch it covers, not by when it was
// written (reviewer-codex: a restart line written on Nov 1 for history before
// Oct 31 hid under --until Oct 31).
func TestGapMatchesByItsCoveredInterval(t *testing.T) {
	written := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	restart := Entry{Stream: GapStream, ID: id(written, 0), F: map[string]string{"of": "*", "from": "unknown", "to": id(to, 0)}}
	if !(Filter{Until: time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)}).Match(restart) {
		t.Error("--until inside the unarchived stretch hides the restart line")
	}
	if (Filter{Since: written.Add(time.Hour)}).Match(restart) {
		t.Error("--since after the stretch shows it")
	}
	from := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	trim := Entry{Stream: GapStream, ID: id(to, 0), F: map[string]string{"of": "status", "from": id(from, 0), "to": id(to, 0)}}
	if !(Filter{Since: from.Add(time.Hour), Until: from.Add(2 * time.Hour)}).Match(trim) {
		t.Error("a window inside a trim's stretch hides it")
	}
	if (Filter{Until: from.Add(-time.Hour)}).Match(trim) {
		t.Error("a window before the stretch shows it")
	}
}

// A day file is not in id order: a pass writes one stream's page, then the
// next. A capped read followed by older reads by position shows every line
// once (foureyes: reading below the oldest id lost the newer-id head).
func TestReadBackByPositionLosesNothing(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	var es []Entry
	for i := 10; i < 20; i++ { // cmd's page first, newer ids
		es = append(es, Entry{Stream: "cmd", ID: id(day.Add(time.Duration(i)*time.Second), 0), F: map[string]string{"command": strings.Repeat("c", 40)}})
	}
	for i := 0; i < 10; i++ { // then status's page, older ids
		es = append(es, Entry{Stream: "status", ID: id(day.Add(time.Duration(i)*time.Second), 0), F: map[string]string{"state": strings.Repeat("s", 40)}})
	}
	prev := Entry{Stream: "cmd", ID: id(day.Add(-24*time.Hour), 0), F: map[string]string{"command": "the day before"}}
	writeDay(t, dir, "", prev)
	writeDay(t, dir, "", es...)
	seen := map[string]int{}
	got, cur, cut, err := ReadBack(dir, day.AddDate(0, 0, -3), Cursor{Day: "2026-11-01", Offset: -1}, 600)
	for rounds := 0; ; rounds++ {
		if err != nil || rounds > 20 {
			t.Fatalf("round %d: %v", rounds, err)
		}
		for _, e := range got {
			seen[key(e)]++
		}
		if !cut && cur.Offset == 0 && cur.Day == "2026-10-31" {
			break
		}
		got, cur, cut, err = ReadBack(dir, day.AddDate(0, 0, -3), cur, 600)
	}
	for _, e := range append(es, prev) {
		if seen[key(e)] != 1 {
			t.Errorf("%s seen %d times", key(e), seen[key(e)])
		}
	}
}

// A bare millisecond cursor matches the entries whose id (or ref) starts
// with it, as Activity's filter and agentbus thread read it (reviewer-codex).
func TestFilterThreadTakesABareCursor(t *testing.T) {
	e := Entry{Stream: "cmd", ID: "1793553996000-0", F: map[string]string{"type": "directive"}}
	r := Entry{Stream: "cmd", ID: "1793553997000-0", F: map[string]string{"type": "reply", "ref": "1793553996000-0"}}
	other := Entry{Stream: "cmd", ID: "1793553996001-0", F: map[string]string{"type": "directive"}}
	f := Filter{Thread: "1793553996000"}
	if !f.Match(e) || !f.Match(r) || f.Match(other) {
		t.Fatalf("bare cursor: root %v reply %v other %v", f.Match(e), f.Match(r), f.Match(other))
	}
}
