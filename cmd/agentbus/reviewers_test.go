package main

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
)

func TestReviewCandidates(t *testing.T) {
	now := time.Now()
	fresh, stale := now.Add(-time.Hour).UnixMilli(), now.Add(-8*24*time.Hour).UnixMilli()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"anthropic": {OK: true, ReadAt: fresh, Rows: []models.Row{{Provider: "anthropic", Client: "claude-code", ID: "opus", Tier: models.Frontier}}},
		"openai": {OK: true, ReadAt: fresh, Rows: []models.Row{
			{Provider: "openai", Client: "codex", ID: "gpt-mini", Tier: models.Fast},
			{Provider: "openai", Client: "codex", ID: "gpt-x", Tier: models.Frontier, Default: true}}},
		"moonshot": {OK: true, ReadAt: fresh, Rows: []models.Row{{Provider: "moonshot", Client: "kimi-cli", ID: "k2", Tier: models.Workhorse}}},
		"xai":      {OK: true, ReadAt: stale, Rows: []models.Row{{Provider: "xai", Client: "grok", ID: "grok-4", Tier: models.Frontier}}},
		"google":   {OK: true, ReadAt: fresh, Rows: []models.Row{{Provider: "google", Client: "agy", ID: "g", Tier: models.Frontier}}},
		// agy also serves the author's own maker: that is no independent review.
		"google2": {OK: true, ReadAt: fresh, Rows: []models.Row{
			{Provider: "google2", Client: "agy2", ID: "claude-opus", Family: "anthropic", Tier: models.Frontier},
			{Provider: "google2", Client: "agy2", ID: "gemini-flash", Tier: models.Workhorse}}},
	}}
	installed := func(client string) bool { return client != "agy" }
	got := reviewCandidates(tbl, "anthropic", installed, now)
	want := []reviewCandidate{
		{Provider: "google2", Client: "agy2", Model: "gemini-flash", Tier: models.Workhorse, As: "second reviewer"},
		{Provider: "moonshot", Client: "kimi-cli", Model: "k2", Tier: models.Workhorse, As: "second reviewer"},
		{Provider: "openai", Client: "codex", Model: "gpt-x", Tier: models.Frontier, As: "foureyes"},
	}
	if len(got) != len(want) {
		t.Fatalf("candidates %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if c := reviewCandidates(&models.Table{}, "anthropic", installed, now); len(c) != 0 {
		t.Fatalf("an empty table offered %+v", c)
	}
}
