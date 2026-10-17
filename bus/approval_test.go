package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func cleanApproval(t *testing.T, b *Bus) {
	cleanAsks(t, b)
	t.Cleanup(func() { b.r.Del(context.Background(), AgentsKey(b.Project())) })
}

func noApproval(t *testing.T, err error, why string) {
	t.Helper()
	if !errors.Is(err, ErrNoApproval) || !strings.Contains(err.Error(), why) {
		t.Fatalf("err = %v, want ErrNoApproval %q", err, why)
	}
}

func approve(t *testing.T, b *Bus, id, role, by, choice string) {
	t.Helper()
	if _, _, err := b.Answer(context.Background(), id, ApprovalQuestion(role), by, choice, ""); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalSpendsOnce(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	id, err := b.ApprovalAsk(ctx, "master", "deploy", "move /tmp to the volume")
	if err != nil {
		t.Fatal(err)
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 1 || asks[0].Title != "Pop deploy?" || asks[0].Questions[0].ID != "spawn-deploy" ||
		asks[0].Questions[0].Options[0].Label != ApprovalAnswer || !asks[0].Questions[0].Options[0].Recommended ||
		asks[0].Questions[0].Options[1].Label != ApprovalRefuse || asks[0].Questions[0].Context != "move /tmp to the volume" ||
		asks[0].Questions[0].Question != "Pop deploy for "+b.Project()+"?" {
		t.Fatalf("approval ask = %+v", asks)
	}
	approve(t, b, id, "deploy", "bernard", ApprovalAnswer)
	if got, err := b.ApprovalUse(ctx, "master", "deploy", CheckOnly, "", DefaultApprovalTTL); err != nil || got != id {
		t.Fatalf("check = %q %v", got, err)
	}
	if got, err := b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL); err != nil || got != id {
		t.Fatalf("spend = %q %v", got, err)
	}
	if u := b.r.HGet(ctx, AsksKey(b.Project()), id+"|u|spawn-deploy").Val(); !strings.HasPrefix(u, "spawn:") {
		t.Fatalf("spend marker = %q", u)
	}
	_, err = b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL)
	noApproval(t, err, "already used")
}

func TestApprovalRefusals(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	use := func() error {
		_, err := b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL)
		return err
	}
	noApproval(t, use(), "no approval asked")
	id, err := b.ApprovalAsk(ctx, "master", "deploy", "r")
	if err != nil {
		t.Fatal(err)
	}
	noApproval(t, use(), "not answered")
	approve(t, b, id, "deploy", "bernard", ApprovalRefuse)
	noApproval(t, use(), "refused")

	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	if _, err := b.WithdrawQuestion(ctx, "master", id, "spawn-deploy", "not needed"); err != nil {
		t.Fatal(err)
	}
	noApproval(t, use(), "no approval asked") // the withdrawal cleared the index

	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	b.r.HSet(ctx, AgentsKey(b.Project()), "master", "{}")
	approve(t, b, id, "deploy", "master", ApprovalAnswer)
	noApproval(t, use(), "answered by an agent")

	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	approve(t, b, id, "deploy", "bernard", ApprovalAnswer)
	time.Sleep(5 * time.Millisecond)
	_, err = b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", time.Millisecond)
	noApproval(t, err, "expired")
	_, err = b.ApprovalUse(ctx, "coder", "deploy", SpendSpawn, "", DefaultApprovalTTL)
	noApproval(t, err, "no approval asked")
}

func TestApprovalBySwap(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	id, _ := b.ApprovalAsk(ctx, "master", "deploy", "r")
	approve(t, b, id, "deploy", "bernard", ApprovalAnswer)
	if _, err := b.ApprovalUse(ctx, "master", "deploy", SpendSwap, "", DefaultApprovalTTL); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ApprovalUse(ctx, "master", "deploy", BySwap, "1-1", DefaultApprovalTTL); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("another id accepted: %v", err)
	}
	if got, err := b.ApprovalUse(ctx, "master", "deploy", BySwap, id, DefaultApprovalTTL); err != nil || got != id {
		t.Fatalf("hand-over = %q %v", got, err)
	}
	_, err := b.ApprovalUse(ctx, "master", "deploy", BySwap, id, DefaultApprovalTTL)
	noApproval(t, err, "already used")

	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	approve(t, b, id, "deploy", "bernard", ApprovalAnswer)
	if _, err := b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL); err != nil {
		t.Fatal(err)
	}
	_, err = b.ApprovalUse(ctx, "master", "deploy", BySwap, id, DefaultApprovalTTL)
	noApproval(t, err, "not a swap hand-over")
}

func TestApprovalOutsideOpenAsk(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	other, _, err := b.Ask(ctx, AskDef{From: "master", Title: "GO?", Questions: []AskQuestion{q("go", "Go", "Yes", "No")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.ApprovalAsk(ctx, "master", "deploy", "r")
	if err != nil {
		t.Fatalf("approval behind an open ask: %v", err)
	}
	if got := b.r.HGet(ctx, AsksKey(b.Project()), "open:master").Val(); got != other {
		t.Fatalf("open:master = %q, want %q", got, other)
	}
	second, err := b.ApprovalAsk(ctx, "master", "deploy", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if s := b.r.HGet(ctx, AsksKey(b.Project()), first+"|s").Val(); s != "replaced" {
		t.Fatalf("first approval state = %q", s)
	}
	if _, _, err := b.Answer(ctx, first, "spawn-deploy", "bernard", ApprovalAnswer, ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("answer to the replaced approval: %v", err)
	}
	approve(t, b, second, "deploy", "bernard", ApprovalAnswer)
	if got, err := b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL); err != nil || got != second {
		t.Fatalf("use = %q %v", got, err)
	}
	if got := b.r.HGet(ctx, AsksKey(b.Project()), "open:master").Val(); got != other {
		t.Fatalf("open:master after the answer = %q", got)
	}
}

func TestApprovalIndexInAskScripts(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	idx := "approval:master:deploy"
	id, _ := b.ApprovalAsk(ctx, "master", "deploy", "r")
	if err := b.Withdraw(ctx, "master", id); err != nil {
		t.Fatalf("withdraw an approval ask: %v", err)
	}
	if b.r.HExists(ctx, AsksKey(b.Project()), idx).Val() {
		t.Fatal("withdraw kept the index")
	}
	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	if _, err := b.WithdrawQuestion(ctx, "master", id, "spawn-deploy", "n"); err != nil {
		t.Fatalf("withdraw the approval question: %v", err)
	}
	if b.r.HExists(ctx, AsksKey(b.Project()), idx).Val() {
		t.Fatal("withdrawing the question kept the index")
	}
	other, _, _ := b.Ask(ctx, AskDef{From: "master", Title: "GO?", Questions: []AskQuestion{q("go", "Go", "Yes", "No")}}, "")
	id, _ = b.ApprovalAsk(ctx, "master", "deploy", "r")
	def := AskDef{From: "master", Title: "Pop deploy?", Questions: []AskQuestion{{ID: "spawn-deploy", Header: "Pop deploy",
		Question: "Pop deploy for " + b.Project() + "?", Options: []AskOption{{Label: ApprovalAnswer}, {Label: ApprovalRefuse}}}}}
	repl, _, err := b.Ask(ctx, def, id)
	if err != nil {
		t.Fatalf("replace an approval ask: %v", err)
	}
	h := b.r.HGetAll(ctx, AsksKey(b.Project())).Val()
	if h[idx] != repl || h["open:master"] != other {
		t.Fatalf("after the replacement: index %q (want %q), open %q (want %q)", h[idx], repl, h["open:master"], other)
	}
	def.Questions[0].ID = "other"
	if _, _, err := b.Ask(ctx, def, repl); err != nil {
		t.Fatal(err)
	}
	if b.r.HExists(ctx, AsksKey(b.Project()), idx).Val() {
		t.Fatal("a replacement without the approval question kept the index")
	}
}

func TestApprovalSweepAndListing(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	id, _ := b.ApprovalAsk(ctx, "master", "deploy", "r")
	approve(t, b, id, "deploy", "bernard", ApprovalAnswer)
	asks, err := b.Asks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if strings.HasPrefix(a.ID, "approval:") {
			t.Fatalf("the index is listed as an ask: %+v", a)
		}
	}
	b.r.HSet(ctx, AsksKey(b.Project()), id+"|e", "1")                         // expired long ago
	if _, err := b.ApprovalAsk(ctx, "master", "architect", "r"); err != nil { // any write sweeps
		t.Fatal(err)
	}
	if b.r.HExists(ctx, AsksKey(b.Project()), "approval:master:deploy").Val() {
		t.Fatal("the sweep kept the index of a dead ask")
	}
	_, err = b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL)
	noApproval(t, err, "no approval asked")
}

// Codex v0.17 review: a generic --replace of an approval ask with the same
// question and another reason must not carry an answer from the old panel.
func TestApprovalReplaceCarriesNothing(t *testing.T) {
	b := dialTest(t)
	cleanApproval(t, b)
	ctx := context.Background()
	old, err := b.ApprovalAsk(ctx, "master", "deploy", "move /tmp to the volume")
	if err != nil {
		t.Fatal(err)
	}
	def := AskDef{From: "master", Title: "Pop deploy?", Questions: []AskQuestion{{ID: "spawn-deploy", Header: "Pop deploy",
		Question: "Pop deploy for " + b.Project() + "?", Context: "restart the bot",
		Options: []AskOption{{Label: ApprovalAnswer, Recommended: true}, {Label: ApprovalRefuse}}}}}
	if _, _, err := b.Ask(ctx, def, old); err != nil {
		t.Fatal(err)
	}
	if c := b.r.HGet(ctx, AsksKey(b.Project()), old+"|c").Val(); c != "" {
		t.Fatalf("carried %q", c)
	}
	if _, _, err := b.Answer(ctx, old, "spawn-deploy", "bernard", ApprovalAnswer, ""); !errors.Is(err, ErrAskRefused) {
		t.Fatalf("an answer on the old panel reached the new reason: %v", err)
	}
	_, err = b.ApprovalUse(ctx, "master", "deploy", SpendSpawn, "", DefaultApprovalTTL)
	noApproval(t, err, "not answered")
}
