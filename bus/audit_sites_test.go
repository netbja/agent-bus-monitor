package bus

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// audit step 2 (spec §2, master's answers): each act writes exactly one line
// after it, from fixed parts; a refused act writes none here (refused acts of
// the CLI write outcome refused at their call site).

// A verdict, approve or reject (reject added to §2's list), whoever writes it.
func TestVerdictAudits(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project), VerdictsKey(b.project)) })
	if !slices.Contains(AuditActions, "reject") {
		t.Fatal("reject is not an audit action")
	}
	head := strings.Repeat("ab", 20)
	id, err := b.AppendVerdict(ctx, Verdict{Subject: "audit-step2", Author: "coder2", Reviewer: "foureyes", Decision: "approve",
		Message: "looks right, ghp_0123456789abcdefghijABCDEFGHIJ012345", Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AppendVerdict(ctx, Verdict{Subject: "audit-step2", Author: "coder2", Reviewer: "reviewer-codex", Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AppendVerdict(ctx, Verdict{Subject: "", Author: "coder2", Reviewer: "foureyes", Decision: "approve"}); err == nil {
		t.Fatal("a verdict with no subject accepted")
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 2 {
		t.Fatalf("%d lines for two verdicts and a refused one, want 2: %+v", len(es), es)
	}
	a, r := es[1], es[0]
	if a.Action != "approve" || a.Actor != "foureyes" || a.Target != "audit-step2" || a.Ref != id || a.Outcome != AuditOK ||
		a.Detail != "on coder2's work, head abababab" {
		t.Fatalf("the approve's line %+v", a)
	}
	if r.Action != "reject" || r.Actor != "reviewer-codex" || r.Detail != "on coder2's work" {
		t.Fatalf("the reject's line %+v", r)
	}
	for _, e := range es { // the message, free text, never in the trail
		if strings.Contains(e.Detail+e.Target+e.Ref, "looks right") || strings.Contains(e.Detail, "ghp_") {
			t.Fatalf("free text in the trail: %+v", e)
		}
	}
}

// An answer to an ask, by the person: one line, typed as the answer is.
func TestAnswerAudits(t *testing.T) {
	b := dialTest(t)
	cleanAsks(t, b)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, AuditKey(b.project)) })
	def := AskDef{From: "master", Title: "Opening", Questions: []AskQuestion{q("amend", "Amendment", "Yes", "No")}}
	id, _, err := b.Ask(ctx, def, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, id, "amend", "charlie", "Maybe", ""); err == nil {
		t.Fatal("not an option, accepted")
	}
	reply, _, err := b.Answer(ctx, id, "amend", "charlie", "", "free text, my secret thoughts")
	if err != nil {
		t.Fatal(err)
	}
	es, _ := b.AuditEntries(ctx, 0)
	if len(es) != 1 {
		t.Fatalf("%d lines, want 1: %+v", len(es), es)
	}
	e := es[0]
	if e.Action != "answer" || e.Actor != "charlie" || e.Target != "ask "+id || e.Ref != reply || e.Outcome != AuditOK ||
		e.Human != "yes" || e.Typed != "yes" || e.Detail != "question amend" {
		t.Fatalf("the answer's line %+v", e)
	}
}
