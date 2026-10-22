package bus

import (
	"context"
	"testing"
)

// The ledger records the author's and the reviewer's providers when known
// (specialist roles spec, part C): additive, empty when unknown.
func TestVerdictLedgerProviders(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.AppendVerdict(ctx, Verdict{Subject: "pr:9", Author: "coder", Reviewer: "foureyes", Decision: "approve",
		AuthorProvider: "anthropic", ReviewerProvider: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AppendVerdict(ctx, Verdict{Subject: "pr:9", Author: "coder", Reviewer: "foureyes", Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	vs, err := b.Verdicts(ctx, "pr:9")
	if err != nil || len(vs) != 2 {
		t.Fatalf("%v %v", vs, err)
	}
	if vs[0].AuthorProvider != "anthropic" || vs[0].ReviewerProvider != "openai" || vs[1].AuthorProvider != "" || vs[1].ReviewerProvider != "" {
		t.Fatalf("providers %+v", vs)
	}
}
