package tokens

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// context-escalation §3.5: the last automatic compact_boundary of a
// session after a time: its preTokens; a manual one, an earlier one, or none
// is not found; another session's file is not read.
func TestAutoCompactAfter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-data-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 15, 21, 30, 0, 0, time.UTC)
	line := func(at time.Time, trigger string, pre int) string {
		return `{"type":"system","subtype":"compact_boundary","timestamp":"` + at.Format(time.RFC3339Nano) +
			`","compactMetadata":{"trigger":"` + trigger + `","preTokens":` + itoa(pre) + `}}` + "\n"
	}
	body := `{"type":"user","timestamp":"2026-11-15T21:00:00Z","message":{"content":"compact_boundary in a user's text"}}` + "\n" +
		line(t0.Add(-time.Hour), "auto", 100000) +
		line(t0.Add(10*time.Minute), "manual", 200000) +
		line(t0.Add(20*time.Minute), "auto", 412345) +
		"not json\n"
	if err := os.WriteFile(filepath.Join(dir, "93ff25bd-34d8-4077-bf6c-65e6cb135bbf.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pre, at, ok, err := AutoCompactAfter(root, "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", t0.UnixMilli())
	if err != nil || !ok || pre != 412345 || at != t0.Add(20*time.Minute).UnixMilli() {
		t.Fatalf("after the alert: %d %d %v %v", pre, at, ok, err)
	}
	if _, _, ok, _ := AutoCompactAfter(root, "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", t0.Add(30*time.Minute).UnixMilli()); ok {
		t.Fatal("found a boundary before the time")
	}
	if _, _, ok, err := AutoCompactAfter(root, "0aff25bd-34d8-4077-bf6c-65e6cb135bbf", 0); ok || err != nil {
		t.Fatalf("another session: %v %v", ok, err)
	}
	if _, _, _, err := AutoCompactAfter(root, "../x", 0); err == nil {
		t.Fatal("an invalid session id was accepted")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
