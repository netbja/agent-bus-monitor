package bus

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// HeldReason (login-updates r9 §3.6): every reader that excuses a held
// agent learns both holds: quota from hold:<provider>, login from
// hold-login:<agent>; neither is "".
func TestHeldReason(t *testing.T) {
	for _, c := range []struct {
		marks           map[string]string
		agent, provider string
		want            string
	}{
		{map[string]string{"hold:anthropic": "1"}, "coder", "anthropic", HeldQuota},
		{map[string]string{"hold-login:coder": "1"}, "coder", "anthropic", HeldLogin},
		{map[string]string{"hold-login:coder": "1"}, "coder", "", HeldLogin},
		{map[string]string{"hold:anthropic": "1", "hold-login:coder": "1"}, "coder", "anthropic", HeldQuota},
		{map[string]string{"hold-login:coder2": "1", "hold:openai": "1"}, "coder", "anthropic", ""},
		{map[string]string{"hold:": "1"}, "coder", "", ""},
		{map[string]string{"hold-login:coder": ""}, "coder", "anthropic", ""},
		{nil, "coder", "anthropic", ""},
	} {
		if got := HeldReason(c.marks, c.agent, c.provider); got != c.want {
			t.Errorf("%v %s/%s: %q, want %q", c.marks, c.agent, c.provider, got, c.want)
		}
	}
	if LoginHoldField("coder") != "hold-login:coder" {
		t.Fatal(LoginHoldField("coder"))
	}
}

// {p}:held (run-budget §4, written for a login hold too): one field per held
// agent, fenced by the dispatch lease; deleted by its release or by forget.
func TestHeldRecordFencedAndForgotten(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.project
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), HeldKey(p), QuotaKey(p), AgentsKey(p)) })
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("acquire")
	}
	rec := HeldRecord{Provider: "login", Since: 1794777000000, Tasks: []string{"wiki-3"}, Branches: []string{"feat/wiki-3"}, Report: "1794776000000-0"}
	if err := b.FencedPutHeld(ctx, "other", "coder", rec); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("unfenced write: %v", err)
	}
	if err := b.FencedPutHeld(ctx, "tok", "coder", rec); err != nil {
		t.Fatal(err)
	}
	got, err := b.Held(ctx)
	if err != nil || !reflect.DeepEqual(got, map[string]HeldRecord{"coder": rec}) {
		t.Fatalf("%+v %v", got, err)
	}
	raw, _ := b.r.HGet(ctx, HeldKey(p), "coder").Result()
	if raw != `{"provider":"login","since":1794777000000,"tasks":["wiki-3"],"branches":["feat/wiki-3"],"report":"1794776000000-0"}` {
		t.Fatalf("stored %s", raw)
	}
	if err := b.FencedDelHeld(ctx, "other", "coder"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("unfenced delete: %v", err)
	}
	if err := b.FencedDelHeld(ctx, "tok", "coder"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Held(ctx); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
	// retire --forget deletes the held record and the login hold.
	if err := b.FencedPutHeld(ctx, "tok", "coder", rec); err != nil {
		t.Fatal(err)
	}
	if err := b.FencedQuotaSet(ctx, "tok", LoginHoldField("coder"), "1794777000000", LoginHoldField("coder2"), "1", "hold:anthropic", "2"); err != nil {
		t.Fatal(err)
	}
	b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(p, "cmd"), Values: map[string]any{"target": "coder2"}}) // forget destroys its group in it
	t.Cleanup(func() { b.r.Del(ctx, StreamKey(p, "cmd")) })
	if found, err := b.ForgetAgent(ctx, "coder"); err != nil || !found {
		t.Fatalf("forget: %v %v", found, err)
	}
	marks, _ := b.QuotaMarks(ctx)
	if got, _ := b.Held(ctx); len(got) != 0 || marks[LoginHoldField("coder")] != "" || marks[LoginHoldField("coder2")] == "" || marks["hold:anthropic"] == "" {
		t.Fatalf("after forget: held %+v, marks %v", got, marks)
	}
}
