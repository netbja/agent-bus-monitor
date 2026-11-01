package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestShutdownBlockers(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fresh := now.Add(-1 * time.Minute).UnixMilli()
	stale := now.Add(-30 * time.Minute).UnixMilli()

	t.Run("clear when board done and peers idle", func(t *testing.T) {
		board := map[string]bus.BoardEntry{
			"task-20": {Owner: "coder", State: "done", Updated: 1},
		}
		agents := map[string]bus.AgentSnapshot{
			"master": {State: "working", TS: fresh}, // self: excluded
			"coder":  {State: "idle", TS: fresh},
		}
		if got := shutdownBlockers(board, agents, "master", now); len(got) != 0 {
			t.Fatalf("want no blockers, got %v", got)
		}
	})

	t.Run("empty board and idle peers is clear", func(t *testing.T) {
		agents := map[string]bus.AgentSnapshot{"coder": {State: "done", TS: fresh}}
		if got := shutdownBlockers(nil, agents, "master", now); len(got) != 0 {
			t.Fatalf("want no blockers, got %v", got)
		}
	})

	t.Run("non-done task blocks", func(t *testing.T) {
		board := map[string]bus.BoardEntry{
			"task-20": {Owner: "foureyes", State: "review", Updated: 1},
		}
		got := shutdownBlockers(board, nil, "master", now)
		if len(got) != 1 || !strings.Contains(got[0], "task-20") || !strings.Contains(got[0], "review") {
			t.Fatalf("want task blocker, got %v", got)
		}
	})

	t.Run("busy peer blocks, stale busy peer does not", func(t *testing.T) {
		agents := map[string]bus.AgentSnapshot{
			"coder":    {State: "working", TS: fresh},
			"oldcoder": {State: "working", TS: stale}, // crashed mid-work: dead, not busy
			"reviewer": {State: "blocked", TS: fresh},
		}
		got := shutdownBlockers(nil, agents, "master", now)
		if len(got) != 2 {
			t.Fatalf("want 2 blockers, got %v", got)
		}
		joined := strings.Join(got, " ")
		if !strings.Contains(joined, "coder") || !strings.Contains(joined, "reviewer") {
			t.Fatalf("missing busy peers: %v", got)
		}
		if strings.Contains(joined, "oldcoder") {
			t.Fatalf("stale peer should not block: %v", got)
		}
	})
}

func TestShutdownUsesNoWake(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.StreamKey(b.Project(), "cmd"))
	agents := map[string]bus.AgentSnapshot{"dev": {}, "master": {}}
	if n := broadcastShutdown(ctx, b, "master", agents, "bye", io.Discard); n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
	msgs, err := r.XRange(ctx, bus.StreamKey(b.Project(), "cmd"), "-", "+").Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("entries: %v %v", msgs, err)
	}
	if msgs[0].Values["wake"] != "no" || msgs[0].Values["target"] != "dev" {
		t.Fatalf("shutdown entry = %v", msgs[0].Values)
	}
	// A shutdown left unread must not stop the agent's next session: it
	// expires 10 minutes after it was sent, measured on Redis time.
	now, err := r.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	exp, _ := strconv.ParseInt(fmt.Sprint(msgs[0].Values["expires_at"]), 10, 64)
	if d := exp - now.Add(10*time.Minute).UnixMilli(); d < -2000 || d > 2000 {
		t.Fatalf("expires_at = %d, want ≈ server time + 10m", exp)
	}
}

func TestExpiredShutdownDrainsAsPayloadlessError(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer bus.DeleteProject(ctx, r, b.Project())
	old := shutdownTTL
	shutdownTTL = time.Millisecond
	defer func() { shutdownTTL = old }()
	if n := broadcastShutdown(ctx, b, "master", map[string]bus.AgentSnapshot{"dev": {}}, "shutdown", io.Discard); n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
	time.Sleep(5 * time.Millisecond)
	var out bytes.Buffer
	if code := runDrain(ctx, b, "dev", "host-1", &out, false); code != 0 {
		t.Fatalf("drain exit = %d, out=%s", code, out.String())
	}
	var ev map[string]any
	if err := json.Unmarshal(out.Bytes(), &ev); err != nil {
		t.Fatalf("drain output %q: %v", out.String(), err)
	}
	if ev["event"] != "error" || ev["delivery"] != "expired" {
		t.Fatalf("event = %v, want error/expired", ev)
	}
	if _, ok := ev["body"]; ok {
		t.Fatalf("expired shutdown carried a body: %v", ev)
	}
}

func TestCmdNoWakeFlag(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.StreamKey(b.Project(), "cmd"))
	var out bytes.Buffer
	if err := runCmd(ctx, b, "me", []string{"--no-wake", "dev", "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	msgs, _ := r.XRange(ctx, bus.StreamKey(b.Project(), "cmd"), id, id).Result()
	if len(msgs) != 1 || msgs[0].Values["wake"] != "no" || msgs[0].Values["command"] != "hi" {
		t.Fatalf("entry = %v", msgs)
	}
	out.Reset()
	if err := runCmd(ctx, b, "me", []string{"dev", "plain"}, &out); err != nil {
		t.Fatal(err)
	}
	id = strings.TrimSpace(out.String())
	msgs, _ = r.XRange(ctx, bus.StreamKey(b.Project(), "cmd"), id, id).Result()
	if _, ok := msgs[0].Values["wake"]; ok {
		t.Fatal("plain cmd wrote wake")
	}
}
