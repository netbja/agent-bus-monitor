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
	gap := Entry{Stream: GapStream, ID: id(at, 9), F: map[string]string{"of": "status", "from": "1-0", "to": id(at, 9)}}
	if !(Filter{Kind: "status"}).Match(gap) || (Filter{Kind: "report"}).Match(gap) || (Filter{Thread: "x"}).Match(gap) {
		t.Error("a gap shows for its own stream's kind, never under a thread or a grep")
	}
	if !(Filter{Agent: "coder"}).Match(gap) {
		t.Error("a gap shows under an agent filter: its entries may have been the agent's")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
