package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeSource is a set of streams held in memory, oldest first.
type fakeSource struct {
	streams map[string][]Entry
	reads   int
}

func (f *fakeSource) add(stream string, ms int64, fields map[string]string) string {
	id := fmt.Sprintf("%d-0", ms)
	for _, e := range f.streams[stream] {
		if e.ID == id {
			id = fmt.Sprintf("%d-%d", ms, len(f.streams[stream]))
		}
	}
	f.streams[stream] = append(f.streams[stream], Entry{Stream: stream, ID: id, F: fields})
	return id
}

func (f *fakeSource) trim(stream string, keep int) {
	s := f.streams[stream]
	if len(s) > keep {
		f.streams[stream] = append([]Entry(nil), s[len(s)-keep:]...)
	}
}

func (f *fakeSource) After(_ context.Context, stream, after string, count int64) ([]Entry, error) {
	f.reads++
	var out []Entry
	for _, e := range f.streams[stream] {
		if after == "" || idLess(after, e.ID) {
			out = append(out, e)
			if int64(len(out)) == count {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeSource) Oldest(_ context.Context, stream string) (string, bool, error) {
	s := f.streams[stream]
	if len(s) == 0 {
		return "", false, nil
	}
	return s[0].ID, true, nil
}

// day0 is a fixed UTC day; entries are stamped within it.
var day0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func ms(d time.Duration) int64 { return day0.Add(d).UnixMilli() }

func newSource() *fakeSource { return &fakeSource{streams: map[string][]Entry{}} }

// lines reads every archived line, in file order (days ascending).
func lines(t *testing.T, dir string) []Entry {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Strings(files)
	var out []Entry
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if l == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(l), &e); err != nil {
				t.Fatalf("line %q: %v", l, err)
			}
			out = append(out, e)
		}
	}
	return out
}

func count(es []Entry, stream string) int {
	n := 0
	for _, e := range es {
		if e.Stream == stream {
			n++
		}
	}
	return n
}

func pass(t *testing.T, w *Writer, src Source, now int64) int {
	t.Helper()
	n, err := w.Pass(context.Background(), src, now)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPassAppendsEachEntryOnceAndRestartsWithoutDuplicates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"from": "master", "target": "coder", "type": "directive", "command": "go"})
	src.add("report", ms(0), map[string]string{"agent": "coder", "kind": "note", "message": "done"}) // same id as the cmd
	src.add("status", ms(time.Second), map[string]string{"agent": "coder", "state": "idle", "message": "ok"})
	w := &Writer{Dir: dir}
	if n := pass(t, w, src, ms(time.Minute)); n != 3 {
		t.Fatalf("first pass archived %d, want 3", n)
	}
	es := lines(t, dir)
	if count(es, "cmd") != 1 || count(es, "report") != 1 || count(es, "status") != 1 {
		t.Fatalf("a cmd and a report sharing an id are both kept: %+v", es)
	}
	if count(es, "gap") != 1 || es[0].Stream != "gap" || es[0].F["of"] != "*" || es[0].F["from"] != "unknown" {
		t.Fatalf("a first archive starts with the 'archive restarted' line: %+v", es)
	}
	if n := pass(t, w, src, ms(2*time.Minute)); n != 0 {
		t.Fatalf("a second pass archived %d, want 0", n)
	}
	src.add("cmd", ms(3*time.Second), map[string]string{"from": "coder", "type": "reply", "command": "ok"})
	// A restart reads cursor.json.
	if n := pass(t, &Writer{Dir: dir}, src, ms(3*time.Minute)); n != 1 {
		t.Fatalf("after a restart %d archived, want the one new entry", n)
	}
	// cursor.json deleted: rebuilt from the day files, still no duplicate, no restart line.
	if err := os.Remove(filepath.Join(dir, "cursor.json")); err != nil {
		t.Fatal(err)
	}
	if n := pass(t, &Writer{Dir: dir}, src, ms(4*time.Minute)); n != 0 {
		t.Fatalf("a missing cursor with day files archived %d again", n)
	}
	if es := lines(t, dir); len(es) != 5 || count(es, "gap") != 1 {
		t.Fatalf("want 4 entries and one restart line: %+v", es)
	}
}

// A crash between the append and the cursor's rename: the cursor is behind the
// files, and the files win.
func TestStaleCursorBehindTheFilesAppendsNothingTwice(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	w := &Writer{Dir: dir}
	pass(t, w, src, ms(time.Minute))
	stale, _ := os.ReadFile(filepath.Join(dir, "cursor.json"))
	src.add("cmd", ms(time.Second), map[string]string{"command": "b"})
	pass(t, w, src, ms(2*time.Minute))
	if err := os.WriteFile(filepath.Join(dir, "cursor.json"), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := pass(t, &Writer{Dir: dir}, src, ms(3*time.Minute)); n != 0 {
		t.Fatalf("a stale cursor re-archived %d", n)
	}
}

func TestTornLastLineIsTruncatedOnStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	pass(t, &Writer{Dir: dir}, src, ms(time.Minute))
	day := filepath.Join(dir, "2026-11-01.jsonl")
	f, _ := os.OpenFile(day, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"stream":"cmd","id":"17`) // a crash mid-append
	f.Close()
	src.add("cmd", ms(time.Second), map[string]string{"command": "b"})
	pass(t, &Writer{Dir: dir}, src, ms(2*time.Minute))
	if es := lines(t, dir); count(es, "cmd") != 2 { // lines() fails on a glued line
		t.Fatalf("want both entries: %+v", es)
	}
}

func TestATrimmedStreamWritesOneGapLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("status", ms(0), map[string]string{"state": "idle"})
	w := &Writer{Dir: dir}
	pass(t, w, src, ms(time.Minute))
	for i := 1; i <= 5; i++ {
		src.add("status", ms(time.Duration(i)*time.Hour), map[string]string{"state": "idle"})
	}
	src.trim("status", 2) // the dispatcher was down past the stream's reach
	pass(t, w, src, ms(6*time.Hour))
	pass(t, w, src, ms(7*time.Hour))
	es := lines(t, dir)
	var gaps []Entry
	for _, e := range es {
		if e.Stream == "gap" && e.F["of"] == "status" {
			gaps = append(gaps, e)
		}
	}
	if len(gaps) != 1 || gaps[0].F["from"] != fmt.Sprintf("%d-0", ms(0)) || gaps[0].F["to"] != fmt.Sprintf("%d-0", ms(4*time.Hour)) {
		t.Fatalf("want one gap from the cursor to the oldest retained: %+v", gaps)
	}
	if count(es, "status") != 3 {
		t.Fatalf("the retained entries are archived: %+v", es)
	}
}

func TestDeletedDirectoryWritesTheRestartLineFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	w := &Writer{Dir: dir} // the same running writer: the person deletes the directory under it
	pass(t, w, src, ms(time.Minute))
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	src.add("cmd", ms(time.Second), map[string]string{"command": "b"})
	pass(t, w, src, ms(2*time.Minute))
	es := lines(t, dir)
	if len(es) != 3 || es[0].Stream != "gap" || es[0].F["of"] != "*" || es[0].F["to"] != fmt.Sprintf("%d-0", ms(0)) {
		t.Fatalf("the restart line first, then what the stream still holds: %+v", es)
	}
}

func TestFilesAre0600InA0700Directory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	pass(t, &Writer{Dir: dir}, src, ms(time.Minute))
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("directory %v", st.Mode().Perm())
	}
	for _, n := range []string{"2026-11-01.jsonl", "cursor.json"} {
		if st, err := os.Stat(filepath.Join(dir, n)); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", n, err, st)
		}
	}
}

func TestASecondWriterIsHeldOffByTheLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := (&Writer{Dir: dir}).Pass(context.Background(), src, ms(time.Minute)); err != ErrLocked {
		t.Fatalf("a locked directory: err %v, want ErrLocked", err)
	}
	if es := lines(t, dir); len(es) != 0 {
		t.Fatalf("wrote while locked: %+v", es)
	}
}

func TestTheEntryBudgetLeavesTheRestForTheNextPass(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	for i := 0; i < 25; i++ {
		src.add("cmd", ms(time.Duration(i)*time.Second), map[string]string{"command": "x"})
	}
	w := &Writer{Dir: dir, Budget: 10, Page: 4}
	if n := pass(t, w, src, ms(time.Minute)); n != 10 {
		t.Fatalf("first pass %d, want the budget 10", n)
	}
	pass(t, w, src, ms(2*time.Minute))
	pass(t, w, src, ms(3*time.Minute))
	if es := lines(t, dir); count(es, "cmd") != 25 {
		t.Fatalf("all 25 in the end, once: %d", count(es, "cmd"))
	}
}

func TestAnExpiredPassDeadlineLeavesTheRest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, _ := (&Writer{Dir: dir}).Pass(ctx, src, ms(time.Minute)); n != 0 {
		t.Fatalf("a spent deadline archived %d", n)
	}
}

func TestEveryFieldIsRedactedWhole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("report", ms(0), map[string]string{"agent": "coder", "message": "set it", "full": "the key: REDIS_PASSWORD=\"hunter2\" and more"})
	pass(t, &Writer{Dir: dir}, src, ms(time.Minute))
	b, _ := os.ReadFile(filepath.Join(dir, "2026-11-01.jsonl"))
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "and more") {
		t.Fatalf("the secret or its field's rest is archived: %s", b)
	}
	es := lines(t, dir)
	r := es[len(es)-1]
	if r.F["full"] != "[redacted]" || r.F["message"] != "set it" {
		t.Fatalf("the whole field redacted, the others unchanged: %+v", r.F)
	}
}

func TestEntriesGoToTheirIDsUTCDay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC).UnixMilli(), map[string]string{"command": "late"})
	src.add("cmd", time.Date(2026, 11, 1, 0, 1, 0, 0, time.UTC).UnixMilli(), map[string]string{"command": "early"})
	pass(t, &Writer{Dir: dir}, src, ms(time.Minute))
	for _, d := range []string{"2026-10-31", "2026-11-01"} {
		b, err := os.ReadFile(filepath.Join(dir, d+".jsonl"))
		if err != nil || !strings.Contains(string(b), `"stream":"cmd"`) {
			t.Errorf("%s: %v %s", d, err, b)
		}
	}
}

func TestPruneKeepsDaysAndTheCapNeverToday(t *testing.T) {
	dir := t.TempDir()
	today := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		os.WriteFile(filepath.Join(dir, d+".jsonl"), []byte(strings.Repeat("x", 1000)+"\n"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "cursor.json"), []byte("{}"), 0o600)
	if err := Prune(dir, 4, 0, today); err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		fs, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		sort.Strings(fs)
		for i := range fs {
			fs[i] = filepath.Base(fs[i])
		}
		return fs
	}
	if got := names(); strings.Join(got, ",") != "2026-10-29.jsonl,2026-10-30.jsonl,2026-10-31.jsonl,2026-11-01.jsonl" {
		t.Fatalf("keep 4 days: %v", got)
	}
	if err := Prune(dir, 4, 2500, today); err != nil { // room for two files
		t.Fatal(err)
	}
	if got := names(); strings.Join(got, ",") != "2026-10-31.jsonl,2026-11-01.jsonl" {
		t.Fatalf("the oldest go first under the cap: %v", got)
	}
	if err := Prune(dir, 4, 1, today); err != nil {
		t.Fatal(err)
	}
	if got := names(); strings.Join(got, ",") != "2026-11-01.jsonl" {
		t.Fatalf("today is never pruned: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "cursor.json")); err != nil {
		t.Fatal("the cursor is not a day file")
	}
}

// Every day file pruned (the cap) but the cursor kept: the archive goes on
// from the cursor; nothing was lost by the writer, so no restart line.
func TestPrunedDaysWithACursorWriteNoRestartLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	src := newSource()
	src.add("cmd", ms(0), map[string]string{"command": "a"})
	pass(t, &Writer{Dir: dir}, src, ms(time.Minute))
	os.Remove(filepath.Join(dir, "2026-11-01.jsonl"))
	src.add("cmd", ms(time.Second), map[string]string{"command": "b"})
	pass(t, &Writer{Dir: dir}, src, ms(2*time.Minute))
	if es := lines(t, dir); len(es) != 1 || es[0].Stream != "cmd" || es[0].F["command"] != "b" {
		t.Fatalf("only the new entry: %+v", es)
	}
}

// Prune takes the writer's lock: it deletes nothing while a writer appends
// (foureyes).
func TestPruneWaitsForTheWritersLock(t *testing.T) {
	dir := t.TempDir()
	today := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	old := filepath.Join(dir, "2026-01-01.jsonl")
	os.WriteFile(old, []byte("x\n"), 0o600)
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Prune(dir, 4, 0, today); err != ErrLocked {
		t.Fatalf("a locked directory: %v, want ErrLocked", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("pruned under a writer's lock")
	}
	unlock()
	if err := Prune(filepath.Join(dir, "none"), 4, 0, today); err != nil {
		t.Fatalf("no archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "none")); err == nil {
		t.Fatal("Prune created the directory")
	}
}
