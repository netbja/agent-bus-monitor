package health

import (
	"strings"
	"testing"
	"time"
)

// The daily review's digest (sentinel-warn-digest): checks in warn for more
// than 24 h, one line each with their age and detail, in one message to
// master. A person's accept of the current warn keeps it out for a week;
// a week on, it is reported again, with the accept, until fixed or accepted
// again.
func TestWarnDigest(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC).UnixMilli()
	h := func(d time.Duration) int64 { return now - d.Milliseconds() }
	rec := func(id string, st Status, since time.Duration, detail string) Record {
		return Record{ID: id, Status: st, Since: h(since), ObservedAt: h(time.Minute), Detail: detail}
	}
	records := map[string]Record{
		"disk":         rec("disk", Warn, 50*time.Hour, "data at 91%"),
		"backup":       rec("backup", Warn, 23*time.Hour, "no backup today"),   // under 24 h: not yet
		"plan-drift":   rec("plan-drift", Warn, 30*time.Hour, "plan-drift: 1 merges not in the plan: #460"),
		"merge-queue":  rec("merge-queue", Fail, 72*time.Hour, "a fail is sentinel's other path"),
		"context": rec("context", Warn, 9*24*time.Hour, "at or above 80%: coder 85%"),
		"review-queue": rec("review-queue", Warn, 40*time.Hour, "accepted, but for an older warn"),
	}
	stale := records["plan-drift"]
	stale.ObservedAt = h(3 * time.Hour) // not measured lately: unknown, not warn
	records["plan-drift"] = stale
	records["notifications"] = rec("notifications", Warn, 26*time.Hour, "no channel")
	staleSvc := rec("services", Warn, 30*time.Hour, "stale: not measured for days")
	staleSvc.ObservedAt = h(72 * time.Hour) // unknown, not warn
	records["services"] = staleSvc
	accepts := map[string]Note{
		"disk":          {ID: "disk", Author: "charlie", At: h(2 * 24 * time.Hour), Text: "the volume grows on purpose"},  // this episode, 2 days old: held back
		"context":  {ID: "context", Author: "charlie", At: h(8 * 24 * time.Hour), Text: "coder runs long"},  // a week old: reported again
		"review-queue":  {ID: "review-queue", Author: "charlie", At: h(50 * time.Hour), Text: "before this warn began"}, // older than the warn's since
	}
	d := WarnDigest(records, accepts, now)
	text := strings.Join(d.Lines, "\n")
	for _, want := range []string{
		"notifications warn 1d2h: no channel",
		"review-queue warn 1d16h: accepted, but for an older warn",
		"context warn 9d0h: at or above 80%: coder 85% (accepted 8d0h ago by charlie: coder runs long; still warn: fix it or accept it again)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("digest lacks %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"disk", "backup", "plan-drift", "merge-queue", "services"} {
		if strings.Contains(text, not+" warn") {
			t.Errorf("digest lists %s:\n%s", not, text)
		}
	}
	if d.Accepted != 1 || len(d.Lines) != 3 {
		t.Fatalf("accepted %d, lines %d:\n%s", d.Accepted, len(d.Lines), text)
	}
	msg := d.Message()
	if !strings.HasPrefix(msg, "Health digest: 3 checks in warn over 24 h") || !strings.Contains(msg, "1 accepted by the person this week (not repeated)") {
		t.Fatalf("message %q", msg)
	}
	if (WarnDigest(map[string]Record{}, nil, now)).Message() != "" {
		t.Fatal("an empty digest has a message")
	}
}
