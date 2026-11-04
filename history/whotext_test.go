package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWhoTextByStream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		e         Entry
		who, text string
	}{
		{"cmd", Entry{Stream: "cmd", F: map[string]string{"from": "master", "target": "coder", "command": "go"}}, "master → coder", "go"},
		{"report", Entry{Stream: "report", F: map[string]string{"agent": "coder", "message": "done"}}, "coder", "done"},
		{"status", Entry{Stream: "status", F: map[string]string{"agent": "coder", "state": "idle", "message": "waiting"}}, "coder", "idle: waiting"},
		{"notify", Entry{Stream: "notify", F: map[string]string{"from": "dispatch", "message": "hello"}}, "dispatch", "hello"},
		{"verdicts", Entry{Stream: "verdicts", F: map[string]string{"reviewer": "reviewer", "author": "coder",
			"decision": "approve", "subject": "pr:3", "message": "ok"}}, "reviewer → coder", "approve pr:3: ok"},
		{"health", Entry{Stream: "health:log", F: map[string]string{"check": "disk", "previous": "ok", "status": "warn"}}, "disk", "ok → warn"},
		{"swaps", Entry{Stream: "swaps", F: map[string]string{"role": "coder", "event": "begin"}}, "coder", "begin"},
		{"gap", Entry{Stream: GapStream, F: map[string]string{"of": "cmd"}}, GapStream, ""},
		{"unknown stream", Entry{Stream: "other"}, "other", ""},
	} {
		who, text := WhoText(tc.e)
		if who != tc.who || text != tc.text {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, who, text, tc.who, tc.text)
		}
	}
}

func TestKindByStream(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		want string
	}{
		{Entry{Stream: "cmd", F: map[string]string{"type": "directive"}}, "directive"},
		{Entry{Stream: "cmd", F: map[string]string{"type": "verdict"}}, "verdict-notice"},
		{Entry{Stream: "verdicts"}, "verdict"},
		{Entry{Stream: "health:log"}, "health"},
		{Entry{Stream: "swaps"}, "swap"},
		{Entry{Stream: "report"}, "report"},
		{Entry{Stream: GapStream}, GapStream},
	} {
		if got := Kind(tc.e); got != tc.want {
			t.Errorf("Kind(%s %v) = %q, want %q", tc.e.Stream, tc.e.F, got, tc.want)
		}
	}
}

func TestGapCovers(t *testing.T) {
	for _, tc := range []struct {
		of, kind string
		want     bool
	}{
		{"*", "report", true},
		{"*", "directive", true},
		{"report", "report", true},
		{"status", "report", false},
		{"status", "status", true},
		{"notify", "notify", true},
		{"health:log", "health", true},
		{"cmd", "health", false},
		{"swaps", "swap", true},
		{"report", "swap", false},
		{"verdicts", "verdict", true},
		{"cmd", "verdict", false},
		{"cmd", "directive", true},
		{"cmd", "verdict-notice", true},
		{"report", "directive", false},
	} {
		if got := gapCovers(tc.of, tc.kind); got != tc.want {
			t.Errorf("gapCovers(%q, %q) = %v, want %v", tc.of, tc.kind, got, tc.want)
		}
	}
}

// A gap line passes a kind filter only when its stream may hide that kind.
func TestFilterKindOnGapLines(t *testing.T) {
	gap := func(of string) Entry {
		return Entry{Stream: GapStream, ID: "1700000000000-0", F: map[string]string{"of": of}}
	}
	for _, tc := range []struct {
		of, kind string
		want     bool
	}{
		{"report", "report", true},
		{"report", "status", false},
		{"swaps", "swap", true},
		{"*", "verdict", true},
	} {
		if got := (Filter{Kind: tc.kind}).Match(gap(tc.of)); got != tc.want {
			t.Errorf("gap of %q, kind %q: got %v, want %v", tc.of, tc.kind, got, tc.want)
		}
	}
}

// Gap lines are keyed by the stream they cover: two gaps with one id in two
// streams are both kept, the same one twice is kept once.
func TestMergeKeysGapsByTheirStream(t *testing.T) {
	a := Entry{Stream: GapStream, ID: "5-0", F: map[string]string{"of": "cmd"}}
	b := Entry{Stream: GapStream, ID: "5-0", F: map[string]string{"of": "report"}}
	got := Merge([]Entry{a, b}, []Entry{a})
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
}

func TestDirIsUnderTheStateDir(t *testing.T) {
	if got, want := Dir("/state/p"), filepath.Join("/state/p", "history"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadGapsWithNoFileIsEmpty(t *testing.T) {
	es, err := ReadGaps(t.TempDir())
	if err != nil || len(es) != 0 {
		t.Fatalf("got %v, %v", es, err)
	}
}

// saveCursor replaces the cursor whole through a rename, leaving no .tmp.
func TestSaveCursorRenamesAndFailsOnAMissingDir(t *testing.T) {
	dir := t.TempDir()
	w := &Writer{Dir: dir, pos: map[string]string{"cmd": "1-0"}}
	if err := w.saveCursor(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, cursorFile))
	if err != nil || string(b) != `{"cmd":"1-0"}` {
		t.Fatalf("cursor %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, cursorFile+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
	w.Dir = filepath.Join(dir, "gone")
	if err := w.saveCursor(); err == nil {
		t.Fatal("a missing directory must fail")
	}
}

func TestAtReadsTheIDsMillisecond(t *testing.T) {
	at := time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)
	if got := At(Entry{ID: id(at, 0)}); !got.Equal(at) {
		t.Fatalf("got %v, want %v", got, at)
	}
}
