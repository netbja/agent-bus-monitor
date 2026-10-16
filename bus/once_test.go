package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// v0.15 §3.3: the entry and its mark go together; the same episode writes
// nothing more, another episode writes again, and a dispatcher without the
// lease writes nothing at all.
func TestFencedEntryOnce(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	count := func(kind string) int64 {
		n, _ := b.r.XLen(ctx, StreamKey(b.project, kind)).Result()
		return n
	}
	for i, ep := range []string{"100", "100", "200"} {
		sent, err := b.FencedCmdOnce(ctx, "tok", "resumed:openai:coder", ep, "dispatch", "coder", CmdDirective, "resume", i == 2)
		if err != nil || sent != (i != 1) {
			t.Fatalf("cmd %d (episode %s) = %v, %v", i, ep, sent, err)
		}
	}
	if n := count("cmd"); n != 2 {
		t.Fatalf("cmd entries = %d, want 2", n)
	}
	if sent, err := b.FencedNotifyOnce(ctx, "tok", "longhold:openai", "100", "dispatch", "long pause"); err != nil || !sent {
		t.Fatalf("notify = %v, %v", sent, err)
	}
	if sent, _ := b.FencedNotifyOnce(ctx, "tok", "longhold:openai", "100", "dispatch", "long pause"); sent {
		t.Fatal("notify sent twice in one episode")
	}
	if m, _ := b.QuotaMarks(ctx); m["resumed:openai:coder"] != "200" || m["longhold:openai"] != "100" {
		t.Fatalf("marks = %v", m)
	}
	if _, err := b.FencedCmdOnce(ctx, "other", "resumed:openai:kimi", "100", "dispatch", "kimi", CmdDirective, "resume", false); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("without the lease: %v, want ErrNotHolder", err)
	}
	if m, _ := b.QuotaMarks(ctx); m["resumed:openai:kimi"] != "" {
		t.Fatal("a mark was written without the lease")
	}
	if n := count("cmd"); n != 2 {
		t.Fatalf("cmd entries = %d after a fenced-out write", n)
	}
}

// v0.15 §6.1.3: no request or challenge opens on an agent being swapped, on
// either side of the challenge.
func TestNoWorkOpensOnASwappingAgent(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSwap(ctx, "foureyes", SwapIntent{ToProfile: "opus", By: "bernard"}); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("second BeginSwap = %v", err)
	}
	if err := b.OpenChallenge(ctx, "foureyes", "pr-1", "coder|review"); err == nil {
		t.Fatal("a challenge gated a swapping agent")
	}
	if err := b.OpenChallenge(ctx, "coder", "pr-2", "foureyes|fix"); err == nil {
		t.Fatal("a swapping agent opened a challenge")
	}
	if _, err := b.Request(ctx, "task-1", "master", "foureyes", "", "do it", 0); err == nil {
		t.Fatal("a request reached a swapping agent")
	}
	if err := b.EndSwap(ctx, "foureyes"); err != nil {
		t.Fatal(err)
	}
	if err := b.OpenChallenge(ctx, "foureyes", "pr-1", "coder|review"); err != nil {
		t.Fatalf("after the swap: %v", err)
	}
}
