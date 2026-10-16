package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

// The conductor's fenced paths refuse a swapping agent too, in the script.
func TestFencedWorkRefusesASwappingAgent(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireConductor(ctx, "c-tok", 15*time.Second); err != nil || !ok {
		t.Skipf("conductor lease: %v %v", ok, err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedRequest(ctx, "c-tok", "m-1", "missions", "coder", "", "do it"); err == nil || !strings.Contains(err.Error(), "being swapped") {
		t.Fatalf("FencedRequest = %v", err)
	}
	if err := b.FencedChallenge(ctx, "c-tok", "coder", "rev-1", "missions", "review"); err == nil || !strings.Contains(err.Error(), "being swapped") {
		t.Fatalf("FencedChallenge = %v", err)
	}
}

// The swap check is in the same script as the resume write.
func TestFencedCmdOnceRefusesASwappingRecipient(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedCmdOnce(ctx, "tok", "resumed:openai:coder", "1", "dispatch", "coder", CmdDirective, "resume", false); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("err = %v, want ErrSwapInProgress", err)
	}
	if m, _ := b.QuotaMarks(ctx); m["resumed:openai:coder"] != "" {
		t.Fatal("marked although not sent")
	}
}

// A mark already set does not hide a swap: the release stays incomplete.
func TestFencedCmdOnceSwapBeforeMark(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "tok", 15*time.Second); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if _, err := b.FencedCmdOnce(ctx, "tok", "resumed:openai:coder", "1", "dispatch", "coder", CmdDirective, "resume", false); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginSwap(ctx, "coder", SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FencedCmdOnce(ctx, "tok", "resumed:openai:coder", "1", "dispatch", "coder", CmdDirective, "resume", false); !errors.Is(err, ErrSwapInProgress) {
		t.Fatalf("err = %v, want ErrSwapInProgress even with the mark set", err)
	}
}

// EpisodeEntry reads the whole retained stream, past any page size.
func TestEpisodeEntryScansTheWholeStream(t *testing.T) {
	b := dialDispatch(t)
	ctx := context.Background()
	stream := StreamKey(b.project, "cmd")
	first, err := b.r.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: []any{"target", "coder", "episode", "m=1"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		b.r.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: []any{"target", "other"}})
	}
	if id, err := b.EpisodeEntry(ctx, "coder", "m", "1"); err != nil || id != first {
		t.Fatalf("EpisodeEntry = %q, %v; want %q", id, err, first)
	}
}
