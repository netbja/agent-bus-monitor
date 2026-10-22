package bus

import (
	"context"
	"testing"
)

func cleanPlan(t *testing.T, b *Bus) {
	t.Cleanup(func() { b.r.Del(context.Background(), PlanKey(b.Project()), PlanFactsKey(b.Project())) })
}

func TestPlanLoadIsRefusedForAnUnknownProject(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	if err := b.LoadPlan(ctx, "docs/plans/v.json", "abc", []byte(`{}`), []string{"a"}); err == nil {
		t.Fatal("a plan was loaded for a project with no key")
	}
	if _, err := b.Status(ctx, "master", "idle", "x", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.LoadPlan(ctx, "docs/plans/v.json", "abc", []byte(`{"plan":"v","tasks":[{"after":[]}]}`), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := b.Plan(ctx)
	if err != nil || !ok || rec.Commit != "abc" || rec.Path != "docs/plans/v.json" || rec.LoadedAt == 0 || rec.File != `{"plan":"v","tasks":[{"after":[]}]}` {
		t.Fatalf("plan %+v %v %v", rec, ok, err)
	}
}

func TestPlanKeysDoNotMakeAProjectAppear(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	b.r.Set(ctx, PlanKey(b.Project()), `{}`, 0)
	b.r.HSet(ctx, PlanFactsKey(b.Project()), "a", `{}`)
	ps, err := Projects(ctx, b.r)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Project == b.Project() {
			t.Fatal("plan keys alone listed the project")
		}
	}
}

func TestPlanFacts(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	if err := b.PlanLink(ctx, "a", 1); err == nil {
		t.Fatal("a fact was recorded with no plan loaded")
	}
	b.Status(ctx, "master", "idle", "x", AgentIdent{})
	if err := b.LoadPlan(ctx, "p.json", "c1", []byte(`{}`), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := b.PlanLink(ctx, "a", 102); err != nil {
		t.Fatal(err)
	}
	if err := b.PlanNote(ctx, "a", "waits for the tag"); err != nil {
		t.Fatal(err)
	}
	if err := b.PlanDone(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"link":  b.PlanLink(ctx, "zz", 1),
		"done":  b.PlanDone(ctx, "zz"),
		"note":  b.PlanNote(ctx, "zz", "x"),
		"pr 0":  b.PlanLink(ctx, "a", 0),
		"long":  b.PlanNote(ctx, "a", string(make([]byte, 201))),
		"lines": b.PlanNote(ctx, "a", "x\ny"),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	facts, err := b.PlanFacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, bb := facts["a"], facts["b"]
	if a.PR != 102 || a.Note != "waits for the tag" || a.Linked == 0 || a.At == 0 || a.Done != 0 {
		t.Fatalf("a: %+v", a)
	}
	if bb.Done == 0 || bb.At != bb.Done {
		t.Fatalf("b: %+v", bb)
	}

	// A reload keeps the facts of tasks still in the plan and drops the others.
	if err := b.LoadPlan(ctx, "p.json", "c2", []byte(`{}`), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	facts, _ = b.PlanFacts(ctx)
	if _, ok := facts["b"]; ok || facts["a"].PR != 102 {
		t.Fatalf("after reload: %+v", facts)
	}

	if err := b.ClearPlan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Plan(ctx); ok {
		t.Fatal("plan still there after clear")
	}
	if facts, _ := b.PlanFacts(ctx); len(facts) != 0 {
		t.Fatalf("facts after clear: %+v", facts)
	}
}
