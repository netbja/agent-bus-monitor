package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ci-loop §4 "Writes": one fenced script per action, its parts' progress in
// {p}:ci, at least once.

const (
	ciDigA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ciDigB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func ciStep(digest string, owner string) CIStep {
	return CIStep{PR: 42, Digest: digest, Cap: 3, Owner: owner, Master: "master",
		Handoff:    [2]string{"CI failed on PR #42, hand-off ", " of 3: build"},
		MasterCopy: [2]string{"copy: PR #42 back to its author, hand-off ", " of 3"},
		StopMaster: "PR #42 stopped after 3 hand-offs",
		StopNotice: "demo: PR #42 still fails CI after 3 hand-offs",
	}
}

type ciEntry struct{ target, command, episode, wake, forWho string }

func ciEntries(t *testing.T, b *Bus, kind string) []ciEntry {
	t.Helper()
	xs, err := b.r.XRange(context.Background(), StreamKey(b.project, kind), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := []ciEntry{}
	for _, x := range xs {
		s := func(k string) string { v, _ := x.Values[k].(string); return v }
		c := s("command")
		if kind == "notify" {
			c = s("message")
		}
		out = append(out, ciEntry{target: s("target"), command: c, episode: s("episode"), wake: s("wake"), forWho: s("for")})
	}
	return out
}

func ciField(t *testing.T, b *Bus) string {
	t.Helper()
	v, _ := b.r.HGet(context.Background(), CIKey(b.project), "pr:42").Result()
	return v
}

func ciLease(t *testing.T) *Bus {
	t.Helper()
	b := dialDispatch(t)
	if ok, err := b.AcquireDispatch(context.Background(), "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	return b
}

// A hand-off writes the owner's entry (woken), master's copy (wake=no) and
// the record; the same episode again writes nothing (same).
func TestCIStepHandoffThenSame(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder"))
	if err != nil || got != CISent {
		t.Fatalf("step = %q, %v", got, err)
	}
	cmds := ciEntries(t, b, "cmd")
	ep := "ci:pr:42=" + ciDigA
	want := []ciEntry{
		{target: "coder", command: "CI failed on PR #42, hand-off 1 of 3: build", episode: ep},
		{target: "master", command: "copy: PR #42 back to its author, hand-off 1 of 3", episode: ep, wake: "no"},
	}
	if len(cmds) != 2 || cmds[0] != want[0] || cmds[1] != want[1] {
		t.Fatalf("cmds %+v, want %+v", cmds, want)
	}
	if f := ciField(t, b); f != ciDigA+"|1|0|om" {
		t.Fatalf("field %q", f)
	}
	if got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); err != nil || got != CISame {
		t.Fatalf("again = %q, %v", got, err)
	}
	if n := len(ciEntries(t, b, "cmd")); n != 2 {
		t.Fatalf("the same episode wrote again: %d", n)
	}
	// A new head counts toward the same cap.
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); got != CISent || ciField(t, b) != ciDigB+"|2|0|om" {
		t.Fatalf("new episode = %q, field %q", got, ciField(t, b))
	}
}

// When master is the owner there is no copy.
func TestCIStepOwnerIsMasterNoCopy(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	if got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "master")); err != nil || got != CISent {
		t.Fatalf("step = %q, %v", got, err)
	}
	if cmds := ciEntries(t, b, "cmd"); len(cmds) != 1 || cmds[0].target != "master" || cmds[0].wake != "" {
		t.Fatalf("cmds %+v", cmds)
	}
	if f := ciField(t, b); f != ciDigA+"|1|0|o" {
		t.Fatalf("field %q", f)
	}
}

// The cap stops the loop once: master's directive (wake=no) and the person's
// notice; afterwards a new head or failing set is stopped-before and writes
// nothing.
func TestCIStepCapStopsOnceAndStaysStopped(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	b.r.HSet(ctx, CIKey(b.project), "pr:42", ciDigA+"|3|0|om")
	got, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder"))
	if err != nil || got != CIStopped {
		t.Fatalf("step = %q, %v", got, err)
	}
	cmds, notes := ciEntries(t, b, "cmd"), ciEntries(t, b, "notify")
	if len(cmds) != 1 || cmds[0].target != "master" || cmds[0].wake != "no" || cmds[0].command != "PR #42 stopped after 3 hand-offs" {
		t.Fatalf("cmds %+v", cmds)
	}
	if len(notes) != 1 || notes[0].forWho != ForPerson || notes[0].command != "demo: PR #42 still fails CI after 3 hand-offs" {
		t.Fatalf("notes %+v", notes)
	}
	if f := ciField(t, b); f != ciDigB+"|3|1|mn" {
		t.Fatalf("field %q", f)
	}
	for _, d := range []string{ciDigB, ciDigA} {
		if got, err := b.FencedCIStep(ctx, "tok", ciStep(d, "coder")); err != nil || got != CIStoppedBefore {
			t.Fatalf("after the stop (%s) = %q, %v", d[:4], got, err)
		}
	}
	if len(ciEntries(t, b, "cmd")) != 1 || len(ciEntries(t, b, "notify")) != 1 {
		t.Fatal("a stopped PR wrote again")
	}
}

// 0 hand-offs: every red PR goes straight to the stop.
func TestCIStepCapZeroStops(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	s := ciStep(ciDigA, "coder")
	s.Cap = 0
	if got, _ := b.FencedCIStep(ctx, "tok", s); got != CIStopped || ciField(t, b) != ciDigA+"|0|1|mn" {
		t.Fatalf("got %q field %q", got, ciField(t, b))
	}
}

// An unfinished stop is finished for its own digest only (reviewer-codex, r6).
func TestCIStepUnfinishedStopFinishesForItsDigestOnly(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	b.r.HSet(ctx, CIKey(b.project), "pr:42", ciDigA+"|3|1|m")
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); got != CIStoppedBefore {
		t.Fatalf("another digest = %q", got)
	}
	if n := len(ciEntries(t, b, "notify")); n != 0 {
		t.Fatal("another digest finished the stop")
	}
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); got != CIStopped {
		t.Fatalf("its digest = %q", got)
	}
	if len(ciEntries(t, b, "cmd")) != 0 || len(ciEntries(t, b, "notify")) != 1 || ciField(t, b) != ciDigA+"|3|1|mn" {
		t.Fatalf("cmds %v notes %v field %q", ciEntries(t, b, "cmd"), ciEntries(t, b, "notify"), ciField(t, b))
	}
}

// A script failure after the owner's part: the retry writes master's copy
// only, and the count does not move. A refused done-mark: the part is
// written again, the same text and episode, and the count does not move.
func TestCIStepRetryWritesOnlyMissingParts(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	b.r.HSet(ctx, CIKey(b.project), "pr:42", ciDigA+"|1|0|o")
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); got != CISent {
		t.Fatalf("retry = %q", got)
	}
	if cmds := ciEntries(t, b, "cmd"); len(cmds) != 1 || cmds[0].target != "master" || !strings.Contains(cmds[0].command, "hand-off 1 of 3") {
		t.Fatalf("cmds %+v", cmds)
	}
	if f := ciField(t, b); f != ciDigA+"|1|0|om" {
		t.Fatalf("field %q", f)
	}
	b2, ctx := ciLease(t), context.Background()
	b2.r.HSet(ctx, CIKey(b2.project), "pr:42", ciDigA+"|1|0|")
	if got, _ := b2.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); got != CISent {
		t.Fatalf("resend = %q", got)
	}
	if cmds := ciEntries(t, b2, "cmd"); len(cmds) != 2 || cmds[0].command != "CI failed on PR #42, hand-off 1 of 3: build" || cmds[0].episode != "ci:pr:42="+ciDigA {
		t.Fatalf("cmds %+v", cmds)
	}
	if f := ciField(t, b2); f != ciDigA+"|1|0|om" {
		t.Fatalf("field %q", f)
	}
}

// An episode that changes while its action is unfinished: the new action is
// decided, the old one's missing part is not sent.
func TestCIStepEpisodeChangeReplacesUnfinished(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	b.r.HSet(ctx, CIKey(b.project), "pr:42", ciDigA+"|1|0|o")
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); got != CISent {
		t.Fatalf("got %q", got)
	}
	for _, c := range ciEntries(t, b, "cmd") {
		if c.episode != "ci:pr:42="+ciDigB || !strings.Contains(c.command, "hand-off 2 of 3") {
			t.Fatalf("an old episode's part, or a wrong count: %+v", c)
		}
	}
	if f := ciField(t, b); f != ciDigB+"|2|0|om" {
		t.Fatalf("field %q", f)
	}
}

// A stale token, a swap in progress or a wrong-typed key writes nothing.
func TestCIStepRefusesAndWritesNothing(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	if _, err := b.FencedCIStep(ctx, "stale", ciStep(ciDigA, "coder")); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("stale token: %v", err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("owner swapping: %v", err)
	}
	if ciField(t, b) != "" || len(ciEntries(t, b, "cmd")) != 0 {
		t.Fatal("a refused step wrote")
	}
	b.r.Del(ctx, SwapKey(b.project, "coder"))
	if got, _ := b.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); got != CISent {
		t.Fatalf("after the swap = %q", got)
	}

	// Master being swapped holds the stop.
	b2, ctx := ciLease(t), context.Background()
	b2.r.HSet(ctx, CIKey(b2.project), "pr:42", ciDigA+"|3|0|om")
	if err := b2.BeginSwap(ctx, "master", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.FencedCIStep(ctx, "tok", ciStep(ciDigB, "coder")); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("master swapping: %v", err)
	}
	if ciField(t, b2) != ciDigA+"|3|0|om" || len(ciEntries(t, b2, "notify")) != 0 {
		t.Fatal("a stop held by master's swap wrote")
	}

	for _, key := range []string{"ci", "cmd", "notify"} {
		b3, ctx := ciLease(t), context.Background()
		k := CIKey(b3.project)
		if key != "ci" {
			k = StreamKey(b3.project, key)
		}
		b3.r.Set(ctx, k, "x", 0)
		if _, err := b3.FencedCIStep(ctx, "tok", ciStep(ciDigA, "coder")); !errors.Is(err, ErrCIBadType) {
			t.Fatalf("%s wrong-typed: %v", key, err)
		}
		if key != "ci" && ciField(t, b3) != "" {
			t.Fatalf("%s wrong-typed: the record was written", key)
		}
	}
}

// CIRecord reads the fields; FencedCIForget drops the closed PRs' fields;
// ResetCI deletes one PR's field.
func TestCIRecordForgetAndReset(t *testing.T) {
	b, ctx := ciLease(t), context.Background()
	b.r.HSet(ctx, CIKey(b.project), "pr:42", ciDigA+"|2|0|om", "pr:7", ciDigB+"|3|1|mn", "junk", "x")
	rec, err := b.CIRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r := rec[42]; r.Digest != ciDigA || r.Count != 2 || r.Stopped || r.Done != "om" {
		t.Fatalf("42 %+v", r)
	}
	if r := rec[7]; !r.Stopped || r.Count != 3 || len(rec) != 2 {
		t.Fatalf("7 %+v, %d fields", r, len(rec))
	}
	if err := b.FencedCIForget(ctx, "stale", []int{7}); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("stale forget: %v", err)
	}
	if err := b.FencedCIForget(ctx, "tok", []int{7}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.CIRecord(ctx); len(rec) != 1 || rec[42].Count != 2 {
		t.Fatalf("after forget %+v", rec)
	}
	if ok, err := b.ResetCI(ctx, 42); err != nil || !ok {
		t.Fatalf("reset = %v, %v", ok, err)
	}
	if ok, _ := b.ResetCI(ctx, 42); ok {
		t.Fatal("a second reset found a field")
	}
	if rec, _ := b.CIRecord(ctx); len(rec) != 0 {
		t.Fatalf("after reset %+v", rec)
	}
}
