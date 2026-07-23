package bus

import (
	"context"
	"testing"
)

func TestBudgetKeyNaming(t *testing.T) {
	if got := BudgetKey("busmon"); got != "busmon:budget" {
		t.Fatalf("BudgetKey = %q, want busmon:budget", got)
	}
}

func TestSetBudgetAndBudgets(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.SetBudget(ctx, "anthropic", BudgetSnapshot{
		SessionPct: 25, WeeklyPct: 44,
		Extra: map[string]float64{"weekly_opus_pct": 12},
		TS:    1000,
	}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	// A second provider must not disturb the first — this hash is the whole
	// point of the provider dimension.
	if err := b.SetBudget(ctx, "openai", BudgetSnapshot{WeeklyPct: 7, TS: 2000}); err != nil {
		t.Fatalf("SetBudget openai: %v", err)
	}

	got, err := b.Budgets(ctx)
	if err != nil {
		t.Fatalf("Budgets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Budgets returned %d providers, want 2: %+v", len(got), got)
	}
	a := got["anthropic"]
	if a.SessionPct != 25 || a.WeeklyPct != 44 || a.Extra["weekly_opus_pct"] != 12 {
		t.Errorf("anthropic = %+v", a)
	}
	// SetBudget stamps Provider from the key, so a caller cannot publish a row
	// whose payload disagrees with the field it is filed under.
	if a.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic (stamped from the key)", a.Provider)
	}
	if got["openai"].WeeklyPct != 7 {
		t.Errorf("openai = %+v", got["openai"])
	}

	// Overwrite semantics: latest wins, no accumulation.
	if err := b.SetBudget(ctx, "anthropic", BudgetSnapshot{SessionPct: 99, TS: 3000}); err != nil {
		t.Fatalf("SetBudget overwrite: %v", err)
	}
	got, _ = b.Budgets(ctx)
	if got["anthropic"].SessionPct != 99 || got["anthropic"].WeeklyPct != 0 {
		t.Errorf("overwrite left stale fields: %+v", got["anthropic"])
	}
}

func TestSetBudgetRejectsBadProvider(t *testing.T) {
	b := dialTest(t)
	if err := b.SetBudget(context.Background(), "Bad Provider", BudgetSnapshot{}); err == nil {
		t.Error("invalid provider name should be rejected")
	}
}

// The session id is ambient identity: the CLI reads it from the environment on
// every status publish, so it lands in the agents hash without the agent ever
// naming it. `agentbus refresh` needs it to find the agent's transcript.
func TestStatusRegistersSessionID(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if _, err := b.Status(ctx, "dev", "working", "x", AgentIdent{Pane: "w1:p1", Session: "sess-abc"}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	agents, err := b.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if got := agents["dev"].Session; got != "sess-abc" {
		t.Errorf("Session = %q, want sess-abc", got)
	}
	if got := agents["dev"].Pane; got != "w1:p1" {
		t.Errorf("Pane = %q, want w1:p1", got)
	}
}
