package quota

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The status line records the context fill Claude Code gives it
// (context_window.used_percentage) per session; null or absent records
// nothing, so the agent reads unknown, never 0.
func TestRecordContext(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(1_800_000_000_000)
	in := []byte(`{"session_id":"7f0c-aa","context_window":{"used_percentage":84.6,"context_window_size":200000}}`)
	if err := RecordContext(dir, in, now); err != nil {
		t.Fatal(err)
	}
	r, ok := ReadContext(dir, "7f0c-aa")
	if !ok || r.Pct != 84.6 || r.ObservedAt != now.UnixMilli() || r.Session != "7f0c-aa" {
		t.Fatalf("read %+v %v", r, ok)
	}
	if info, err := os.Stat(filepath.Join(dir, "7f0c-aa.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", info, err)
	}
	for _, s := range []string{
		`{"session_id":"s2","context_window":{"used_percentage":null}}`,
		`{"session_id":"s2","context_window":{}}`,
		`{"session_id":"s2"}`,
		`{"context_window":{"used_percentage":50}}`,                          // no session
		`{"session_id":"../escape","context_window":{"used_percentage":50}}`, // not a file name
	} {
		if err := RecordContext(dir, []byte(s), now); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files %v: only the reading with a number and a safe session id is kept", entries)
	}
	if _, ok := ReadContext(dir, "s2"); ok {
		t.Fatal("an unknown fill read as a value")
	}
	if _, ok := ReadContext(dir, "../7f0c-aa"); ok {
		t.Fatal("a session id that is not a file name was read")
	}
}

// Files of sessions not seen for a day are pruned on each record, so the
// directory does not grow forever.
func TestRecordContextPrunes(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(1_800_000_000_000)
	old := now.Add(-25 * time.Hour)
	RecordContext(dir, []byte(`{"session_id":"old","context_window":{"used_percentage":10}}`), old)
	RecordContext(dir, []byte(`{"session_id":"recent","context_window":{"used_percentage":20}}`), now.Add(-time.Hour))
	RecordContext(dir, []byte(`{"session_id":"new","context_window":{"used_percentage":30}}`), now)
	for s, want := range map[string]bool{"old": false, "recent": true, "new": true} {
		if _, ok := ReadContext(dir, s); ok != want {
			t.Errorf("%s kept %v, want %v", s, ok, want)
		}
	}
}
