package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// foureyes on audit-step2: the fenced writers' fields go through step 1's
// checks (auditArgs: the bounds, control characters, whole-field
// redaction) in Go; the script only puts in the count it computes, at
// AuditCountMark. A refused entry gives the script no line (said on
// AuditWarn), never refusing its act.
func TestFencedAuditBlock(t *testing.T) {
	var warned []error
	old := AuditWarn
	AuditWarn = func(err error) { warned = append(warned, err) }
	t.Cleanup(func() { AuditWarn = old })
	e := AuditEntry{Action: "ci-handoff", Actor: "dispatch", Target: "pr 42", Outcome: AuditOK,
		Detail: "hand-off " + AuditCountMark + " of 3 to coder, head aaaaaaaa"}
	got := fencedAudit(e)
	if len(got) != fencedAuditArgs || got[0] != "1" || got[6] != "pr 42" || got[9] != "hand-off " || got[10] != " of 3 to coder, head aaaaaaaa" || got[11] != "1" {
		t.Fatalf("block %q", got)
	}
	for name, bad := range map[string]AuditEntry{
		"a control character": {Action: "quota-pause", Actor: "dispatch", Target: "anthropic", Outcome: AuditOK, Detail: "paused\nuntil"},
		"a target over 200":   {Action: "merge", Actor: "dispatch", Target: strings.Repeat("b", 201), Outcome: AuditOK},
		"a detail over 300":   {Action: "merge", Actor: "dispatch", Target: "x", Outcome: AuditOK, Detail: strings.Repeat("d", 301)},
	} {
		warned = nil
		if got := fencedAudit(bad); len(got) != fencedAuditArgs || got[0] != "0" || len(warned) != 1 {
			t.Errorf("%s: block %q, warned %v", name, got, warned)
		}
	}
	// A credential shape: the field whole, and no count put into a redacted detail.
	tok := "ghp_0123456789abcdefghijABCDEFGHIJ012345"
	got = fencedAudit(AuditEntry{Action: "merge", Actor: "dispatch", Target: tok, Outcome: AuditOK, Detail: "head " + tok + " #" + AuditCountMark})
	if got[0] != "1" || got[6] != "[redacted]" || got[9] != "[redacted]" || got[10] != "" || got[11] != "0" {
		t.Fatalf("redacted block %q", got)
	}
}

// End to end: a merge whose branch holds a credential shape writes its line
// with the target redacted whole; one whose branch is over 200 runes writes
// none and still closes the request.
func TestRequestMergedAuditChecksItsFields(t *testing.T) {
	var warned []error
	old := AuditWarn
	AuditWarn = func(err error) { warned = append(warned, err) }
	t.Cleanup(func() { AuditWarn = old })
	b := ciLease(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	tokBranch := "ghp_0123456789abcdefghijABCDEFGHIJ012345"
	inReview(t, b, "t1", ReviewOpts{Branch: tokBranch, ReviewBase: mBase})
	if got, err := b.FencedRequestMerged(ctx, "tok", "t1", tokBranch, mHead, "dev", 1); err != nil || got != MergedClosed {
		t.Fatalf("%q %v", got, err)
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 1 || es[0].Target != "[redacted]" || strings.Contains(es[0].Detail, "ghp_") {
		t.Fatalf("the merge's line %+v", es)
	}
	long := "coder/" + strings.Repeat("b", 200)
	inReview(t, b, "t2", ReviewOpts{Branch: long, ReviewBase: mBase})
	if got, err := b.FencedRequestMerged(ctx, "tok", "t2", long, mHead, "dev", 1); err != nil || got != MergedClosed {
		t.Fatalf("a long branch: %q %v", got, err)
	}
	if es, _ = b.AuditEntries(ctx, 0); len(es) != 1 || len(warned) == 0 {
		t.Fatalf("a long branch's line written (%d lines), or not said (%v)", len(es), warned)
	}
	if _, err := b.FencedRequestMerged(ctx, "stale", "t2", long, mHead, "dev", 1); !errors.Is(err, ErrNotHolder) {
		t.Fatal(err)
	}
}
