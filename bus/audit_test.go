package bus

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The audit stream (spec 2026-11-24-audit-stream-design.md; the person's
// answers Q1 90 days, Q2 every act, Q3 B a trail with a hash chain).

func auditOK(action string) AuditEntry {
	return AuditEntry{Action: action, Actor: "master", Target: "pr 761", Ref: "1795574727848-0", Outcome: AuditOK, Detail: "head 9a37b101"}
}

func auditLen(t *testing.T, b *Bus) int64 {
	t.Helper()
	n, err := b.r.XLen(context.Background(), AuditKey(b.project)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// §5: an unknown action, an unknown outcome, a bad actor, a control
// character or an oversized field is refused and nothing is written.
func TestAuditRefusesAndWritesNothing(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	bad := map[string]func(*AuditEntry){
		"unknown action":         func(e *AuditEntry) { e.Action = "delete-everything" },
		"no action":              func(e *AuditEntry) { e.Action = "" },
		"unknown outcome":        func(e *AuditEntry) { e.Outcome = "maybe" },
		"no actor":               func(e *AuditEntry) { e.Actor = "" },
		"actor not a name":       func(e *AuditEntry) { e.Actor = "Master Bob" },
		"control in detail":      func(e *AuditEntry) { e.Detail = "head\n9a37" },
		"control in target":      func(e *AuditEntry) { e.Target = "pr\x1b[31m 1" },
		"detail over 300 runes":  func(e *AuditEntry) { e.Detail = strings.Repeat("é", AuditDetailMax+1) },
		"target over its bound":  func(e *AuditEntry) { e.Target = strings.Repeat("x", auditFieldMax+1) },
		"human not yes or empty": func(e *AuditEntry) { e.Human = "maybe" },
		"typed not yes or empty": func(e *AuditEntry) { e.Typed = "no" },
	}
	for name, mutate := range bad {
		e := auditOK("merge")
		mutate(&e)
		if _, err := b.Audit(ctx, e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := auditLen(t, b); n != 0 {
		t.Fatalf("%d entries written by refused audits", n)
	}
	for _, a := range AuditActions { // every action of §2 is taken
		if _, err := b.Audit(ctx, auditOK(a)); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	want := []string{"approve", "reject", "answer", "merge", "push", "deliver", "force", "ci-handoff", "update", "reinstall",
		"quota-pause", "pause", "resume", "suspend", "retire", "retro-apply"}
	if !slices.Equal(AuditActions, want) {
		t.Fatalf("actions %v, want §2's %v", AuditActions, want)
	}
}

// §3: a token-shaped value is stored as exactly [redacted], the whole field,
// and the line is still written so the act is not hidden.
func TestAuditRedactsTheWholeField(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	e := auditOK("push")
	e.Detail = "commit ghp_0123456789abcdefghijABCDEFGHIJ012345 pushed"
	e.Target = "branch Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlc2lnbmF0dXJl"
	if _, err := b.Audit(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := b.AuditEntries(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("entries %v %v", got, err)
	}
	if got[0].Detail != "[redacted]" || got[0].Target != "[redacted]" || got[0].Actor != "master" || got[0].Action != "push" {
		t.Fatalf("entry %+v", got[0])
	}
}

// §3, Q1: every append trims by age, MINID at Redis TIME minus the retention;
// the test sets no clock: two entries written with ids 91 and 89 days before
// the server's TIME, then an audit: the 89-day one and the new one stay.
func TestAuditTrimsByAge(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	now, err := b.r.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	for _, age := range []time.Duration{91 * day, 89 * day} {
		id := strconv.FormatInt(now.Add(-age).UnixMilli(), 10) + "-0"
		if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: AuditKey(b.project), ID: id, Values: []string{"action", "merge", "age", age.String()}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.AuditWith(ctx, auditOK("merge"), 90*day); err != nil {
		t.Fatal(err)
	}
	all, _ := b.r.XRange(ctx, AuditKey(b.project), "-", "+").Result()
	if len(all) != 2 || all[0].Values["age"] != (89*day).String() || all[1].Values["action"] != "merge" || all[1].Values["hash"] == "" {
		t.Fatalf("after the trim: %v", all)
	}
	// foureyes: a retention of 0 or below is the default's 90 days, never
	// none: a day-old entry stays.
	b.r.Del(ctx, AuditKey(b.project))
	dayOld := strconv.FormatInt(now.Add(-day).UnixMilli(), 10) + "-0"
	if err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: AuditKey(b.project), ID: dayOld, Values: []string{"action", "merge"}}).Err(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []time.Duration{0, -time.Hour} {
		if _, err := b.AuditWith(ctx, auditOK("merge"), r); err != nil {
			t.Fatal(err)
		}
	}
	if kept, _ := b.r.XRange(ctx, AuditKey(b.project), dayOld, dayOld).Result(); len(kept) != 1 {
		t.Fatal("a retention of 0 trimmed a day-old entry")
	}
	if DefaultAuditRetention != 90*day {
		t.Fatalf("the default retention %v, want 90 days (Q1)", DefaultAuditRetention)
	}
}

// Q3 B: each entry carries the previous entry's hash; verify checks the
// chain: whole, it passes; a deleted entry in the middle and an entry
// appended without the writer break it, named at the entry that no longer
// follows. A cut of the tail is not detected (said, and asserted so the
// claim stays true).
func TestAuditHashChain(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	var ids []string
	for i := range 4 {
		e := auditOK("approve")
		e.Detail = fmt.Sprintf("verdict %d", i)
		id, err := b.Audit(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got, _ := b.AuditEntries(ctx, 0) // newest first
	for i := 0; i+1 < len(got); i++ {
		if got[i].Prev != got[i+1].Hash || got[i].Hash != AuditHash(got[i]) {
			t.Fatalf("entry %s does not chain: %+v", got[i].ID, got[i])
		}
	}
	if c, err := b.AuditVerify(ctx); err != nil || !c.OK || c.Entries != 4 {
		t.Fatalf("a whole chain: %+v %v", c, err)
	}
	// The tail cut: undetected, as the spec says.
	b.r.XDel(ctx, AuditKey(b.project), ids[3])
	if c, _ := b.AuditVerify(ctx); !c.OK || c.Entries != 3 {
		t.Fatalf("a tail cut: %+v (the spec says it is not detected)", c)
	}
	// A middle entry deleted: the next no longer follows.
	b.r.XDel(ctx, AuditKey(b.project), ids[1])
	if c, _ := b.AuditVerify(ctx); c.OK || c.BrokenAt != ids[2] || !strings.Contains(c.Reason, "previous") {
		t.Fatalf("a middle deletion: %+v", c)
	}
}

// An entry appended outside bus.Audit (any user allowed XADD can: the trail
// is no proof, §3) carries no valid hash: verify names it.
func TestAuditVerifyNamesAForeignEntry(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	if _, err := b.Audit(ctx, auditOK("merge")); err != nil {
		t.Fatal(err)
	}
	last, _ := b.AuditEntries(ctx, 0)
	// It follows the chain (prev is the last hash), so only its own hash betrays it.
	forged, err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: AuditKey(b.project), Values: []string{"at", "1", "action", "approve", "actor", "coder",
		"outcome", "ok", "prev", last[0].Hash, "hash", "0000000000000000000000000000000000000000"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Audit(ctx, auditOK("merge")); err != nil {
		t.Fatal(err)
	}
	c, _ := b.AuditVerify(ctx)
	if c.OK || c.BrokenAt != forged || !strings.Contains(c.Reason, "hash") {
		t.Fatalf("a forged entry: %+v", c)
	}
}

// Writers race: the chain is read and extended in one script, so concurrent
// audits still chain in stream order.
func TestAuditChainsUnderConcurrentWriters(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := auditOK("merge")
			e.Detail = "n " + strconv.Itoa(i)
			if _, err := b.Audit(ctx, e); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c, err := b.AuditVerify(ctx); err != nil || !c.OK || c.Entries != 20 {
		t.Fatalf("after 20 concurrent writers: %+v %v", c, err)
	}
}

// §3: {p}:audit survives busmon --reset (not a reset kind), is a record
// kind (backups keep it) and a project kind.
func TestAuditSurvivesReset(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	if _, err := b.Audit(ctx, auditOK("merge")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Purge(ctx, ResetKinds); err != nil {
		t.Fatal(err)
	}
	if n := auditLen(t, b); n != 1 {
		t.Fatalf("reset left %d audit entries", n)
	}
	if !slices.Contains(SurvivesReset, "audit") || slices.Contains(ResetKinds, "audit") || !projectKinds["audit"] {
		t.Fatal("audit is not listed as surviving a reset")
	}
	if c, kind := ClassOf("demo", AuditKey("demo")); c != KeyRecord || kind != "audit" {
		t.Fatalf("class %v %q", c, kind)
	}
}

// AuditEntries reads newest first, from since (Unix ms).
func TestAuditEntriesSince(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	if _, err := b.Audit(ctx, auditOK("merge")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	mid := time.Now().UnixMilli()
	time.Sleep(5 * time.Millisecond)
	if _, err := b.Audit(ctx, auditOK("push")); err != nil {
		t.Fatal(err)
	}
	all, _ := b.AuditEntries(ctx, 0)
	if len(all) != 2 || all[0].Action != "push" || all[0].At == 0 {
		t.Fatalf("all %+v", all)
	}
	recent, _ := b.AuditEntries(ctx, mid)
	if len(recent) != 1 || recent[0].Action != "push" {
		t.Fatalf("since %+v", recent)
	}
}

// reviewer-codex: verify pages a live stream; an append's retention trim
// that passes its cursor between two pages is ordinary, not a break: the
// check starts again from the new head (and says inconclusive if the head
// keeps moving). A real deletion in the middle is still named.
func TestAuditVerifyAcrossARetentionTrim(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	var ids []string
	for range 6 {
		id, err := b.Audit(ctx, auditOK("merge"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	page, hook := auditVerifyPage, auditBetweenPages
	t.Cleanup(func() { auditVerifyPage, auditBetweenPages = page, hook })
	auditVerifyPage = 2
	trims := 0
	auditBetweenPages = func() { // the head trimmed past the cursor, once
		if trims++; trims == 1 {
			b.r.XTrimMinID(ctx, AuditKey(b.project), ids[3])
		}
	}
	c, err := b.AuditVerify(ctx)
	if err != nil || !c.OK || c.Inconclusive || c.First != ids[3] || c.Entries != 3 {
		t.Fatalf("a trim between pages: %+v %v", c, err)
	}
	// A head that keeps moving: inconclusive, never a break.
	auditBetweenPages = func() { // three appends, then the head trimmed to the last three: past any cursor
		for range 3 {
			if _, err := b.Audit(ctx, auditOK("merge")); err != nil {
				t.Error(err)
			}
		}
		b.r.XTrimMaxLen(ctx, AuditKey(b.project), 3)
	}
	if c, _ := b.AuditVerify(ctx); c.OK || !c.Inconclusive || c.BrokenAt != "" {
		t.Fatalf("a moving head: %+v", c)
	}
	// A middle deletion between pages is still a break.
	auditBetweenPages = nil
	b.r.Del(ctx, AuditKey(b.project))
	ids = ids[:0]
	for range 6 {
		id, _ := b.Audit(ctx, auditOK("merge"))
		ids = append(ids, id)
	}
	b.r.XDel(ctx, AuditKey(b.project), ids[2])
	if c, _ := b.AuditVerify(ctx); c.OK || c.Inconclusive || c.BrokenAt != ids[3] {
		t.Fatalf("a middle deletion: %+v", c)
	}
}
