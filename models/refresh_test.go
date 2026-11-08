package models

import (
	"context"
	"testing"
	"time"
)

// §3.5: a reader that ignores its deadline never holds the refresh past it;
// its provider keeps the previous rows and is named as a failure.
func TestRefreshAbandonsAHungReader(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{"xai": {OK: true, ReadAt: 1, Rows: []Row{{Provider: "xai", ID: "grok-4.6"}}}}}
	block := make(chan struct{})
	defer close(block)
	readers := []Reader{
		{Provider: "xai", Read: func(context.Context) ([]Row, string, error) { <-block; return nil, "", nil }},
		{Provider: "openai", Read: func(context.Context) ([]Row, string, error) {
			return []Row{{Provider: "openai", ID: "gpt-6.1-sol"}}, "codex", nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	failed := tbl.Refresh(ctx, readers, nil, time.Now())
	if d := time.Since(start); d > time.Second {
		t.Fatalf("refresh waited %v for a hung reader", d)
	}
	if failed["xai"] == "" || len(failed) != 1 {
		t.Fatalf("failures %v, want xai only", failed)
	}
	if p := tbl.Providers["xai"]; len(p.Rows) != 1 || p.Rows[0].ID != "grok-4.6" || p.OK {
		t.Fatalf("hung provider %+v", p)
	}
	if p := tbl.Providers["openai"]; !p.OK || p.Rows[0].ID != "gpt-6.1-sol" {
		t.Fatalf("the other reader was not read: %+v", p)
	}
}

// Every reader runs under the same deadline: a slow first reader does not
// spend the next one's time.
func TestRefreshReadersShareTheDeadline(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{}}
	block := make(chan struct{})
	defer close(block)
	readers := []Reader{
		{Provider: "a", Read: func(context.Context) ([]Row, string, error) { <-block; return nil, "", nil }},
		{Provider: "b", Read: func(ctx context.Context) ([]Row, string, error) {
			select {
			case <-time.After(50 * time.Millisecond):
				return []Row{{ID: "b-1"}}, "", nil
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	failed := tbl.Refresh(ctx, readers, nil, time.Now())
	if _, bad := failed["b"]; bad || !tbl.Providers["b"].OK {
		t.Fatalf("b waited behind a: %v %+v", failed, tbl.Providers["b"])
	}
}

// §3.3: a family order in models.toml is the evidence; the hint is not asked.
func TestInFamily(t *testing.T) {
	c := Config{Families: map[string][]string{"grok": {"grok-4.7", "grok-4.6"}}}
	if !c.InFamily("grok-4.6") || !c.InFamily("grok-4.7[1m]") || c.InFamily("grok-4.5") {
		t.Fatal("InFamily")
	}
}

// The table's age is its newest refresh attempt; an empty table has none.
func TestRefreshedAt(t *testing.T) {
	if (&Table{Providers: map[string]*Provider{}}).RefreshedAt() != 0 {
		t.Fatal("empty")
	}
	tbl := &Table{Providers: map[string]*Provider{"a": {At: 5}, "b": {At: 9}, "c": {At: 2}}}
	if tbl.RefreshedAt() != 9 {
		t.Fatal(tbl.RefreshedAt())
	}
}

// reviewer-codex: with a background context, a reader that ignores its own
// deadline is abandoned at that deadline (`agentbus models --refresh`).
func TestRefreshAbandonsAtTheReadersOwnDeadline(t *testing.T) {
	defer func(d time.Duration) { ReaderTimeout = d }(ReaderTimeout)
	ReaderTimeout = 100 * time.Millisecond
	tbl := &Table{Providers: map[string]*Provider{}}
	block := make(chan struct{})
	defer close(block)
	readers := []Reader{{Provider: "xai", Read: func(context.Context) ([]Row, string, error) { <-block; return nil, "", nil }}}
	done := make(chan map[string]string, 1)
	go func() { done <- tbl.Refresh(context.Background(), readers, nil, time.Now()) }()
	select {
	case failed := <-done:
		if failed["xai"] == "" {
			t.Fatalf("failures %v", failed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh waited past the reader's own deadline")
	}
}

func TestReaderTimeoutIs20s(t *testing.T) {
	if ReaderTimeout != 20*time.Second {
		t.Fatalf("ReaderTimeout %v: an explicit --refresh keeps 20 s per reader (§3.5)", ReaderTimeout)
	}
}

// Each built-in client's provider (a client update refreshes it, §3.5).
func TestProviderOfClient(t *testing.T) {
	for c, p := range map[string]string{"claude-code": "anthropic", "codex": "openai", "grok": "xai", "kimi-cli": "moonshot", "agy": "google", "other": ""} {
		if got := ProviderOfClient(c); got != p {
			t.Errorf("%s: %q", c, got)
		}
	}
}
