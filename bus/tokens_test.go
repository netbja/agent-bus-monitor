package bus

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The three token keys are fenced: a dispatcher without the lease writes
// none of them; {p}:tokens is replaced whole with its meta; the samples are
// read back in order.
func TestTokenKeysAreFenced(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), SessionsKey(p), TokensKey(p), QuotaHistKey(p)) })
	if ok, _ := b.AcquireDispatch(ctx, "tok", time.Minute); !ok {
		t.Fatal("lease")
	}
	rec := map[string]SessionRecord{"0198e5c4-aaaa": {Agent: "coder", Provider: "anthropic", Client: "claude"}}
	rows := map[string]TokenRow{TokenKey("2026-10-27", "coder", "anthropic"): {In: 1, Out: 2, N: 1}}
	sample := []QuotaSample{{Provider: "anthropic", Window: "5h", Pct: 46}}
	if err := b.FencedRecordSessions(ctx, "other", rec); err != ErrNotHolder {
		t.Fatalf("sessions without the lease: %v", err)
	}
	if err := b.FencedPutTokens(ctx, "other", rows, false); err != ErrNotHolder {
		t.Fatalf("tokens without the lease: %v", err)
	}
	if err := b.FencedAddQuotaHist(ctx, "other", sample); err != ErrNotHolder {
		t.Fatalf("quota hist without the lease: %v", err)
	}
	if n, _ := b.r.Exists(ctx, SessionsKey(p), TokensKey(p), QuotaHistKey(p)).Result(); n != 0 {
		t.Fatalf("%d keys written without the lease", n)
	}

	if err := b.FencedRecordSessions(ctx, "tok", rec); err != nil {
		t.Fatal(err)
	}
	s, _ := b.Sessions(ctx)
	first := s["0198e5c4-aaaa"]
	if first.Agent != "coder" || first.Provider != "anthropic" || first.First == 0 || first.Last != first.First {
		t.Fatalf("session %+v", first)
	}
	time.Sleep(5 * time.Millisecond)
	b.FencedRecordSessions(ctx, "tok", rec)
	s, _ = b.Sessions(ctx)
	if again := s["0198e5c4-aaaa"]; again.First != first.First || again.Last <= first.Last {
		t.Fatalf("a proved session again keeps its first and moves its last: %+v then %+v", first, again)
	}

	if err := b.FencedPutTokens(ctx, "tok", rows, true); err != nil {
		t.Fatal(err)
	}
	got, meta, ok, _ := b.Tokens(ctx)
	if !ok || len(got) != 1 || got[TokenKey("2026-10-27", "coder", "anthropic")].Out != 2 || !meta.Partial || meta.At == 0 {
		t.Fatalf("tokens %v %+v %v", got, meta, ok)
	}
	b.FencedPutTokens(ctx, "tok", map[string]TokenRow{TokenKey("2026-10-28", "master", "anthropic"): {In: 5}}, false)
	got, meta, _, _ = b.Tokens(ctx)
	if len(got) != 1 || got[TokenKey("2026-10-28", "master", "anthropic")].In != 5 || meta.Partial {
		t.Fatalf("tokens not replaced whole: %v %+v", got, meta)
	}

	b.FencedAddQuotaHist(ctx, "tok", append(sample, QuotaSample{Provider: "openai", Window: "5h", Pct: 98.5}))
	h, err := b.QuotaHist(ctx, 0)
	if err != nil || len(h) != 2 || h[1].Provider != "openai" || h[1].Pct != 98.5 || h[0].At == 0 {
		t.Fatalf("hist %+v %v", h, err)
	}
}

// Sessions older than 8 days go, and beyond the cap the oldest last goes first.
func TestSessionsArePruned(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), SessionsKey(p)) })
	b.AcquireDispatch(ctx, "tok", time.Minute)
	now := time.Now().UnixMilli()
	b.r.HSet(ctx, SessionsKey(p), "old-session-1", fmt.Sprintf(`{"agent":"coder","first":1,"last":%d}`, now-SessionsKeep-60000))
	for i := 0; i < SessionsCap; i++ {
		b.r.HSet(ctx, SessionsKey(p), fmt.Sprintf("kept-session-%04d", i), fmt.Sprintf(`{"agent":"coder","first":1,"last":%d}`, now-int64(SessionsCap-i)*1000))
	}
	b.FencedRecordSessions(ctx, "tok", map[string]SessionRecord{"new-session-1": {Agent: "master"}})
	s, _ := b.Sessions(ctx)
	if _, ok := s["old-session-1"]; ok {
		t.Error("a session older than 8 days was kept")
	}
	if len(s) != SessionsCap {
		t.Errorf("%d sessions, want the cap %d", len(s), SessionsCap)
	}
	if _, ok := s["kept-session-0000"]; ok {
		t.Error("the oldest last survived the cap")
	}
	if _, ok := s["new-session-1"]; !ok {
		t.Error("the new session was dropped")
	}
}

// None of the token keys revives a deleted project.
func TestTokenKeysDiscoverNoProject(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { b.r.Del(ctx, DispatchKey(p), SessionsKey(p), TokensKey(p), QuotaHistKey(p)) })
	b.AcquireDispatch(ctx, "tok", time.Minute)
	b.FencedRecordSessions(ctx, "tok", map[string]SessionRecord{"0198e5c4-aaaa": {Agent: "coder"}})
	b.FencedPutTokens(ctx, "tok", map[string]TokenRow{"d|a|p": {In: 1}}, false)
	b.FencedAddQuotaHist(ctx, "tok", []QuotaSample{{Provider: "x", Window: "5h", Pct: 1}})
	if has, err := b.HasKeysBesidesDispatch(ctx); err != nil || has {
		t.Errorf("token keys count as a project's keys: %v %v", has, err)
	}
	b.r.Del(ctx, DispatchKey(p)) // the lease itself is a project's key: without it, only the token keys remain
	ps, _ := Projects(ctx, b.r)
	for _, pr := range ps {
		if pr.Project == p {
			t.Errorf("the token keys made %s a project", p)
		}
	}
}
