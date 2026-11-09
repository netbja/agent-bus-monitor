package models

import (
	"testing"
	"time"
)

// The person (model-detection §6 Q3): Kimi is one family with one tier;
// the highspeed variants are not told apart; a tier profile on moonshot
// resolves to the family's default model, kimi-for-coding.
func TestKimiOneTier(t *testing.T) {
	tiers := DefaultTiers()
	for _, id := range []string{"kimi-code/kimi-for-coding", "kimi-code/kimi-for-coding-highspeed", "kimi-code/k3",
		"kimi-code/k3-256k", "kimi-code/k3.1-pro", "kimi-code/k3.1-pro-highspeed", "k3", "kimi-for-coding"} {
		if got := tiers.Tier(Row{ID: id}); got != Frontier {
			t.Errorf("%s: tier %q, want frontier", id, got)
		}
	}
	// the rule is anchored (foureyes P3): another provider's id with a Kimi
	// name inside is not frontier
	for _, id := range []string{"gpt-6-astra", "kimchi", "acme-k2-turbo", "acme/kimi-code/k3", "my-kimi-for-coding"} {
		if got := tiers.Tier(Row{ID: id}); got == Frontier {
			t.Errorf("%s: frontier, not a Kimi model", id)
		}
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	rows := func(ids ...string) []Row {
		var out []Row
		for i, id := range ids {
			out = append(out, Row{Provider: "moonshot", ID: id, Tier: tiers.Tier(Row{ID: id}), Default: i == 0})
		}
		return out
	}
	tbl := &Table{Providers: map[string]*Provider{"moonshot": {OK: true, At: now.UnixMilli(), ReadAt: now.UnixMilli(),
		Rows: rows("kimi-code/k3", "kimi-code/kimi-for-coding-highspeed", "kimi-code/kimi-for-coding")}}}
	if r, ok := tbl.Resolve("moonshot", Frontier, now); !ok || r.ID != "kimi-code/kimi-for-coding" {
		t.Fatalf("resolve: %+v %v, want the family's default over Kimi's own k3", r, ok)
	}
	if r, ok := tbl.Resolve("moonshot", Fast, now); ok { // the family has one tier: no fast model
		t.Fatalf("fast resolved to %+v", r)
	}
	tbl.Providers["moonshot"].Rows = rows("kimi-code/k3", "kimi-code/k3.1-pro")
	if r, ok := tbl.Resolve("moonshot", Frontier, now); !ok || r.ID != "kimi-code/k3" {
		t.Fatalf("without kimi-for-coding: %+v %v, want the source's default", r, ok)
	}
}
