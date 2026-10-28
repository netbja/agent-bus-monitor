package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var paris = time.FixedZone("CET", 3600)

func cLine(ts, msgID, reqID string, in, write, read, out int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":%q,"sessionId":"s1","message":{"id":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		ts, reqID, msgID, in, write, read, out) + "\n"
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(s)
}

var big = Budget{Files: 64, Bytes: 128 << 20}

// One request's usage repeats on several lines (one per content block): it
// counts once. Lines without usage, of another type or malformed count nothing.
// Days are the host's local dates: 23:30 UTC on the 27th is the 28th in Paris.
func TestClaudeCountsEachRequestOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proj", "s1.jsonl")
	write(t, p, cLine("2026-10-27T10:00:00Z", "m1", "r1", 10, 100, 1000, 5)+
		cLine("2026-10-27T10:00:00Z", "m1", "r1", 10, 100, 1000, 5)+
		cLine("2026-10-27T10:00:01Z", "m1", "r1", 10, 100, 1000, 5)+
		`{"type":"user","timestamp":"2026-10-27T10:00:02Z","message":{"content":"hi"}}`+"\n"+
		`{"type":"assistant","timestamp":"2026-10-27T10:00:03Z","message":{"id":"m9"}}`+"\n"+
		"{not json\n"+
		cLine("2026-10-27T23:30:00Z", "m2", "r2", 1, 2, 3, 4))
	s := NewClaudeScan(paris)
	if partial := s.Pass([]string{p}, big); partial {
		t.Error("partial with room to spare")
	}
	d := s.Sums(p)
	if got := d["2026-10-27"]; got != (Usage{In: 10, Write: 100, Read: 1000, Out: 5, N: 1}) {
		t.Errorf("27th %+v, want the request once", got)
	}
	if got := d["2026-10-28"]; got != (Usage{In: 1, Write: 2, Read: 3, Out: 4, N: 1}) {
		t.Errorf("28th (local) %+v", got)
	}
}

// A pass reads only the bytes appended since the last one; a half-written
// last line waits; a duplicate line of the request open at the offset still
// counts once; a shrunk or replaced file is rescanned from 0.
func TestClaudeIsIncremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s1.jsonl")
	write(t, p, cLine("2026-10-27T10:00:00Z", "m1", "r1", 1, 0, 0, 1))
	s := NewClaudeScan(paris)
	s.Pass([]string{p}, big)
	half := cLine("2026-10-27T11:00:00Z", "m2", "r2", 2, 0, 0, 2)
	appendTo(t, p, cLine("2026-10-27T10:00:00Z", "m1", "r1", 1, 0, 0, 1)+half[:20])
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got.N != 1 || got.In != 1 {
		t.Fatalf("after a duplicate and half a line: %+v", got)
	}
	appendTo(t, p, half[20:])
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got.N != 2 || got.In != 3 {
		t.Fatalf("after the line completed: %+v", got)
	}
	write(t, p, cLine("2026-10-26T10:00:00Z", "m7", "r7", 7, 0, 0, 7)) // shrunk: rescanned
	s.Pass([]string{p}, big)
	d := s.Sums(p)
	if d["2026-10-27"].N != 0 || d["2026-10-26"].In != 7 {
		t.Fatalf("after the file shrank: %+v", d)
	}
}

// The per-pass budget leaves the rest for the next pass, partial until done.
func TestClaudeBudgetSetsPartial(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", i))
		write(t, p, cLine("2026-10-27T10:00:00Z", fmt.Sprint("m", i), "r", 1, 0, 0, 1))
		paths = append(paths, p)
	}
	s := NewClaudeScan(paris)
	if !s.Pass(paths, Budget{Files: 2, Bytes: 1 << 20}) {
		t.Error("two files of three, yet not partial")
	}
	if s.Pass(paths, Budget{Files: 2, Bytes: 1 << 20}) {
		t.Error("the next pass finished them, yet partial")
	}
	n := 0
	for _, p := range paths {
		n += int(s.Sums(p)["2026-10-27"].N)
	}
	if n != 3 {
		t.Errorf("counted %d requests, want 3", n)
	}
}

// A session's files: its transcript and its subagents', under any project
// directory; an id that is not a session id builds no path.
func TestSessionFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "-a", "0198e5c4-1111-2222-3333-444455556666.jsonl"), "")
	write(t, filepath.Join(root, "-a", "0198e5c4-1111-2222-3333-444455556666", "subagents", "agent-x.jsonl"), "")
	write(t, filepath.Join(root, "-b", "other.jsonl"), "")
	got, err := SessionFiles(root, "0198e5c4-1111-2222-3333-444455556666")
	if err != nil || len(got) != 2 {
		t.Fatalf("files %v %v, want the transcript and its subagent", got, err)
	}
	for _, bad := range []string{"../-b/other", "*", "", "a/b", "short"} {
		if got, err := SessionFiles(root, bad); err == nil || len(got) != 0 {
			t.Errorf("%q built paths %v", bad, got)
		}
	}
}

func codexMeta(id string) string {
	return fmt.Sprintf(`{"timestamp":"2026-10-27T09:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/x","creator_account_id":"acct-SECRET-1","creator_user_id":"user-SECRET-2"}}`, id) + "\n"
}

func codexCount(ts string, total, last [4]int) string {
	u := func(v [4]int) string {
		return fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d,"total_tokens":%d}`, v[0], v[1], v[2], v[3], v[0]+v[2])
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s,"last_token_usage":%s}}}`, ts, u(total), u(last)) + "\n"
}

// Codex: each token_count adds its delta from the previous one, to the day
// of its own timestamp; the baseline crosses midnight; a one-event day counts
// that event; the first event of a session adds last_token_usage; a repeat
// adds 0; a reset adds last_token_usage; info null is skipped. In/Read/Out
// are input minus cached, cached, output.
func TestCodexDeltas(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-27T10-00-00-a.jsonl")
	write(t, p, codexMeta("sess-a")+
		codexCount("2026-10-27T22:00:00Z", [4]int{100, 40, 10, 2}, [4]int{100, 40, 10, 2})+ // 27th local 23:00
		codexCount("2026-10-27T22:30:00Z", [4]int{100, 40, 10, 2}, [4]int{100, 40, 10, 2})+ // repeat: 0
		`{"timestamp":"2026-10-27T22:40:00Z","type":"event_msg","payload":{"type":"token_count","info":null}}`+"\n"+
		codexCount("2026-10-27T23:30:00Z", [4]int{250, 90, 30, 5}, [4]int{150, 50, 20, 3})+ // 28th local, against the 27th's last
		codexCount("2026-10-28T23:30:00Z", [4]int{60, 0, 6, 0}, [4]int{60, 0, 6, 0})) // reset (lower): last, the 29th local
	s := NewCodexScan(paris)
	s.Pass([]string{p}, big)
	d := s.Sums(p)
	want := map[string]Usage{
		"2026-10-27": {In: 60, Read: 40, Out: 10, N: 1},
		"2026-10-28": {In: 100, Read: 50, Out: 20, N: 1},
		"2026-10-29": {In: 60, Read: 0, Out: 6, N: 1},
	}
	for day, w := range want {
		if d[day] != w {
			t.Errorf("%s: %+v, want %+v", day, d[day], w)
		}
	}
	if len(d) != len(want) {
		t.Errorf("days %v", d)
	}
	if dump := fmt.Sprintf("%+v %+v", s, d); strings.Contains(dump, "SECRET") {
		t.Error("an account identifier reached the scanner's state")
	}
}

// A resumed session, in the two shapes Codex could write it (no real resumed
// rollout exists on this host): appended to the same file, or a new file
// whose first event replays the previous one's last total. Neither counts the
// replayed turn twice; the new turn counts once.
func TestCodexResumedSessionCountsNoTurnTwice(t *testing.T) {
	first := codexMeta("sess-r") + codexCount("2026-10-27T10:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0})
	replay := codexCount("2026-10-27T12:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0})
	next := codexCount("2026-10-27T12:01:00Z", [4]int{180, 0, 15, 0}, [4]int{80, 0, 5, 0})
	want := Usage{In: 180, Out: 15, N: 2}

	dir := t.TempDir()
	same := filepath.Join(dir, "rollout-2026-10-27T10-00-00-r.jsonl")
	write(t, same, first+codexMeta("sess-r")+replay+next)
	s := NewCodexScan(paris)
	s.Pass([]string{same}, big)
	if got := s.Sums(same)["2026-10-27"]; got != want {
		t.Errorf("appended resume: %+v, want %+v", got, want)
	}

	dir = t.TempDir()
	a := filepath.Join(dir, "rollout-2026-10-27T10-00-00-r.jsonl")
	b := filepath.Join(dir, "rollout-2026-10-27T12-00-00-r.jsonl")
	write(t, a, first)
	write(t, b, codexMeta("sess-r")+replay+next)
	s = NewCodexScan(paris)
	s.Pass([]string{b, a}, big) // given in any order: scanned oldest first
	sum := s.Sums(a)["2026-10-27"]
	sum.Add(s.Sums(b)["2026-10-27"])
	if sum != want {
		t.Errorf("resume in a new file: %+v, want %+v", sum, want)
	}
}

// Incremental Codex: a pass resumes from the kept total.
func TestCodexIsIncremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-27T10-00-00-i.jsonl")
	write(t, p, codexMeta("sess-i")+codexCount("2026-10-27T10:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0}))
	s := NewCodexScan(paris)
	s.Pass([]string{p}, big)
	appendTo(t, p, codexCount("2026-10-27T10:05:00Z", [4]int{130, 0, 12, 0}, [4]int{30, 0, 2, 0}))
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got != (Usage{In: 130, Out: 12, N: 2}) {
		t.Errorf("%+v", got)
	}
}

// A forked subagent rollout carries its parent's session_meta second (seen on
// real rollouts); its own id is the first one, so it starts its own baseline.
func TestCodexForkHasItsOwnBaseline(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "rollout-2026-10-27T10-00-00-p.jsonl")
	fork := filepath.Join(dir, "rollout-2026-10-27T10-05-00-f.jsonl")
	write(t, parent, codexMeta("sess-p")+codexCount("2026-10-27T10:00:00Z", [4]int{10, 0, 1, 0}, [4]int{10, 0, 1, 0}))
	write(t, fork, codexMeta("sess-f")+codexMeta("sess-p")+codexCount("2026-10-27T10:06:00Z", [4]int{20, 0, 2, 0}, [4]int{20, 0, 2, 0}))
	s := NewCodexScan(paris)
	s.Pass([]string{parent, fork}, big)
	if got := s.Sums(fork)["2026-10-27"]; got != (Usage{In: 20, Out: 2, N: 1}) {
		t.Errorf("fork %+v, want its own first event", got)
	}
}

// A file replaced by one starting with the request it last counted counts
// that request again: the replaced file's content is new.
func TestClaudeReplacedFileForgetsItsLastRequest(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s1.jsonl")
	write(t, p, cLine("2026-10-27T10:00:00Z", "m1", "r1", 5, 0, 0, 5)+cLine("2026-10-27T10:00:00Z", "m1", "r1", 5, 0, 0, 5))
	s := NewClaudeScan(paris)
	s.Pass([]string{p}, big)
	os.Remove(p)
	write(t, p, cLine("2026-10-27T10:00:00Z", "m1", "r1", 5, 0, 0, 5))
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got.N != 1 {
		t.Errorf("replaced file: %+v, want its one request", got)
	}
}

// A pass whose new bytes hold no newline (half a line, or a budget smaller
// than one line) moves no offset: the line counts once, when complete.
func TestHalfALineOnlyMovesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s1.jsonl")
	line := cLine("2026-10-27T10:00:00Z", "m1", "r1", 3, 0, 0, 3)
	write(t, p, line[:10])
	s := NewClaudeScan(paris)
	if !s.Pass([]string{p}, big) {
		t.Error("half a line, yet not partial")
	}
	appendTo(t, p, line[10:])
	if !s.Pass([]string{p}, Budget{Files: 1, Bytes: 5}) { // room smaller than the line
		t.Error("a budget smaller than the line, yet not partial")
	}
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got.N != 1 || got.In != 3 {
		t.Errorf("%+v, want the line once", got)
	}
}

// A rollout with no session_meta is its own session (keyed by its path):
// two such files never chain their totals.
func TestCodexFilesWithoutMetaAreApart(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "rollout-2026-10-27T10-00-00-a.jsonl")
	b := filepath.Join(dir, "rollout-2026-10-27T11-00-00-b.jsonl")
	write(t, a, codexCount("2026-10-27T10:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0}))
	write(t, b, codexCount("2026-10-27T11:00:00Z", [4]int{150, 0, 15, 0}, [4]int{150, 0, 15, 0}))
	s := NewCodexScan(paris)
	s.Pass([]string{a, b}, big)
	if got := s.Sums(b)["2026-10-27"]; got != (Usage{In: 150, Out: 15, N: 1}) {
		t.Errorf("b %+v, want its own first event, not a delta from a", got)
	}
}

// A Codex file replaced or truncated is rescanned with its session's
// baseline rebuilt: the same content counts the same, and the session's
// later file is recounted against the new totals.
func TestCodexReplacedFileRebuildsItsSession(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "rollout-2026-10-27T10-00-00-r.jsonl")
	b := filepath.Join(dir, "rollout-2026-10-27T12-00-00-r.jsonl")
	replace := func(path, content string) { // a new inode, as an editor or a copy would make
		tmp := path + ".new"
		write(t, tmp, content)
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	contentA := codexMeta("sess-r") + codexCount("2026-10-27T10:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0})
	write(t, a, contentA)
	write(t, b, codexMeta("sess-r")+codexCount("2026-10-27T12:01:00Z", [4]int{180, 0, 15, 0}, [4]int{80, 0, 5, 0}))
	s := NewCodexScan(paris)
	s.Pass([]string{a, b}, big)
	replace(a, contentA)
	s.Pass([]string{a, b}, big)
	if got := s.Sums(a)["2026-10-27"]; got != (Usage{In: 100, Out: 10, N: 1}) {
		t.Errorf("replaced file %+v, want its count rebuilt", got)
	}
	if got := s.Sums(b)["2026-10-27"]; got != (Usage{In: 80, Out: 5, N: 1}) {
		t.Errorf("the session's other file %+v, want unchanged", got)
	}
	replace(a, codexMeta("sess-r")+codexCount("2026-10-27T10:00:00Z", [4]int{50, 0, 5, 0}, [4]int{50, 0, 5, 0}))
	s.Pass([]string{a, b}, big)
	if got := s.Sums(b)["2026-10-27"]; got != (Usage{In: 130, Out: 10, N: 1}) {
		t.Errorf("after a was replaced with a lower total, b %+v, want its delta from a's new total", got)
	}
}

// A one-event session replaced with the same content is counted again: the
// rebuild forgets the session's last total, so its first event is new.
func TestCodexReplacedSingleEventSessionCountsAgain(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-27T10-00-00-o.jsonl")
	content := codexMeta("sess-o") + codexCount("2026-10-27T10:00:00Z", [4]int{100, 0, 10, 0}, [4]int{100, 0, 10, 0})
	write(t, p, content)
	s := NewCodexScan(paris)
	s.Pass([]string{p}, big)
	tmp := p + ".new"
	write(t, tmp, content)
	os.Rename(tmp, p)
	s.Pass([]string{p}, big)
	if got := s.Sums(p)["2026-10-27"]; got != (Usage{In: 100, Out: 10, N: 1}) {
		t.Errorf("%+v, want the event counted again", got)
	}
}
