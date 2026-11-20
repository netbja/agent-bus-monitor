package bus

import (
	"context"
	"testing"
)

// views-redesign §4, step 4b: a plan task's priority is the person's, kept
// with the loaded plan's facts on the broker; a request sent for the task
// starts with it.

func TestPlanPriority(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	if _, err := b.SetPlanPriority(ctx, "a", PriorityHigh, "charlie"); err == nil {
		t.Fatal("a priority was recorded with no plan loaded")
	}
	b.Status(ctx, "master", "idle", "x", AgentIdent{})
	if err := b.LoadPlan(ctx, "p.json", "c1", []byte(`{}`), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetPlanPriority(ctx, "zz", PriorityHigh, "charlie"); err == nil {
		t.Fatal("a priority on a task not in the plan")
	}
	if _, err := b.SetPlanPriority(ctx, "a", "urgent", "charlie"); err == nil {
		t.Fatal("an unknown level was accepted")
	}
	if err := b.PlanNote(ctx, "a", "a note"); err != nil {
		t.Fatal(err)
	}
	before, _ := b.PlanFacts(ctx)
	changed, err := b.SetPlanPriority(ctx, "a", PriorityHigh, "charlie")
	if err != nil || !changed {
		t.Fatalf("set high: %v %v", changed, err)
	}
	f, _ := b.PlanFacts(ctx)
	if f["a"].Priority != PriorityHigh || f["a"].PriorityBy != "charlie" || f["a"].PriorityAt == 0 || f["a"].Note != "a note" {
		t.Fatalf("fact %+v", f["a"])
	}
	if f["a"].At != before["a"].At { // a priority is no progress: the task's last fact keeps its time
		t.Fatalf("at moved: %d → %d", before["a"].At, f["a"].At)
	}
	if changed, err := b.SetPlanPriority(ctx, "a", PriorityHigh, "charlie"); err != nil || changed {
		t.Fatalf("same level again: changed %v %v", changed, err)
	}
	if changed, err := b.SetPlanPriority(ctx, "a", PriorityNormal, "charlie"); err != nil || !changed {
		t.Fatalf("back to normal: %v %v", changed, err)
	}
	// normal is the person's level too: stored, so it wins over the plan file's.
	if f, _ := b.PlanFacts(ctx); f["a"].Priority != PriorityNormal || f["a"].PriorityBy != "charlie" || f["a"].Note != "a note" {
		t.Fatalf("normal not stored: %+v", f["a"])
	}
	if changed, _ := b.SetPlanPriority(ctx, "a", PriorityNormal, "charlie"); changed {
		t.Fatal("normal twice changed")
	}
	// A task with no level of the person's: normal is a change (it overrides the file).
	if changed, err := b.SetPlanPriority(ctx, "b", PriorityNormal, "charlie"); err != nil || !changed {
		t.Fatalf("normal on a task with none: %v %v", changed, err)
	}
	b.r.HDel(ctx, PlanFactsKey(b.Project()), "b")
	// A reload keeps the facts of the tasks still in the plan, priority included.
	b.SetPlanPriority(ctx, "b", PriorityLow, "charlie")
	if err := b.LoadPlan(ctx, "p.json", "c2", []byte(`{}`), []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if f, _ := b.PlanFacts(ctx); f["b"].Priority != PriorityLow {
		t.Fatalf("reload dropped b's priority: %+v", f["b"])
	}
}

// A request sent for a plan task starts at the task's priority: the
// broker's, else the plan file's; the plan task is the one whose board field
// names the request's task, else the one with its id.
func TestRequestTakesThePlanTasksPriority(t *testing.T) {
	b := dialTest(t)
	cleanPlan(t, b)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(context.Background(), BoardKey(b.Project()), StreamKey(b.Project(), "cmd")) })
	b.Status(ctx, "master", "idle", "x", AgentIdent{})
	file := `{"plan":"v","tasks":[{"id":"t-high","title":"x","owner":"coder"},` +
		`{"id":"t-file","title":"x","owner":"coder","priority":"low"},` +
		`{"id":"t-board","title":"x","owner":"coder","board":"other-name"},` +
		`{"id":"t-none","title":"x","owner":"coder"},` +
		`{"id":"t-norm","title":"x","owner":"coder","priority":"low"},` +
		`{"id":"t-idboard","title":"x","owner":"coder","board":"elsewhere","priority":"high"}]}`
	if err := b.LoadPlan(ctx, "p.json", "c1", []byte(file), []string{"t-high", "t-file", "t-board", "t-none", "t-norm", "t-idboard"}); err != nil {
		t.Fatal(err)
	}
	b.SetPlanPriority(ctx, "t-high", PriorityHigh, "charlie")
	b.SetPlanPriority(ctx, "t-board", PriorityHigh, "charlie")
	b.SetPlanPriority(ctx, "t-norm", PriorityNormal, "charlie") // the person's normal over the file's low
	for _, c := range []struct{ task, want, by string }{
		{"t-high", PriorityHigh, "charlie"}, // the broker's
		{"t-file", PriorityLow, ""},         // the file's
		{"other-name", PriorityHigh, "charlie"},
		{"t-none", "", ""},
		{"t-norm", "", ""},    // the person's normal wins over the file's low
		{"t-idboard", "", ""}, // its id is not its board task: a request on t-idboard is not its (foureyes P8)
		{"not-in-plan", "", ""},
	} {
		if _, err := b.RequestBy(ctx, c.task, "master", "coder", "", "do it", 0, false); err != nil {
			t.Fatal(err)
		}
		board, _ := b.Board(ctx)
		r := board[c.task].Request
		if r == nil || r.Priority != c.want || r.PriorityBy != c.by || (c.want != "" && r.PriorityAt == 0) {
			t.Fatalf("%s: request %+v, want priority %q by %q", c.task, r, c.want, c.by)
		}
	}
	// A plan with a file busmon cannot read leaves the request at normal.
	b.r.Set(ctx, PlanKey(b.Project()), `{"tasks":["x"],"file":"not json"}`, 0)
	if _, err := b.RequestBy(ctx, "x", "master", "coder", "", "do it", 0, false); err != nil {
		t.Fatalf("a bad plan file broke request send: %v", err)
	}
}
