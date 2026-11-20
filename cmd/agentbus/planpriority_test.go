package main

import (
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// plan priority <task> high|normal|low --as <person> (views-redesign §4, step
// 4b): the person's act, as request priority is; a request sent for the task
// starts at its level.
func TestPlanPriorityCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.PlanIgnoreKey(b.Project()), bus.BoardKey(b.Project()))
	})
	t.Setenv("HERDR_ENV", "")
	person(t)
	repo, _ := planRepo(t, testPlan)
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@HEAD"); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	if code, out, errw := runPlanT(t, b, repo, "priority", "roles", "high", "--as", "charlie"); code != 0 || out != "roles: high (was normal)\n" {
		t.Fatalf("high: %d %q %s", code, out, errw)
	}
	if code, out, _ := runPlanT(t, b, repo, "priority", "roles", "high", "--as", "charlie"); code != 0 || out != "roles: already high\n" {
		t.Fatalf("again: %d %q", code, out)
	}
	for _, bad := range [][]string{{"roles", "high"}, {"roles", "urgent", "--as", "charlie"}, {"roles", "--as", "charlie"}, {"roles", "high", "--as", "Not A Name"}} {
		if code, _, _ := runPlanT(t, b, repo, append([]string{"priority"}, bad...)...); code == 0 {
			t.Fatalf("%v accepted", bad)
		}
	}
	if code, _, errw := runPlanT(t, b, repo, "priority", "nope", "high", "--as", "charlie"); code == 0 || !strings.Contains(errw, "not in the loaded plan") {
		t.Fatalf("unknown task: %d %s", code, errw)
	}
	// roles names the board task roles-impl: a request for it starts high.
	var out strings.Builder
	if err := runRequest(ctx, b, "master", []string{"send", "roles-impl", "coder", "build", "it"}, &out); err != nil {
		t.Fatal(err)
	}
	board, _ := b.Board(ctx)
	if p := board["roles-impl"].Request.Priority; p != bus.PriorityHigh {
		t.Fatalf("the request's priority %q, want the plan task's high", p)
	}
	// Once a request exists, the plan's level no longer moves it: say so.
	if code, out, _ := runPlanT(t, b, repo, "priority", "roles", "low", "--as", "charlie"); code != 0 || !strings.Contains(out, "roles: low (was high)") ||
		!strings.Contains(out, "agentbus request priority roles-impl") {
		t.Fatalf("low with a request: %d %q", code, out)
	}
	// A done request orders nothing: no hint (foureyes P13).
	if err := b.AcceptRequest(ctx, "roles-impl", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "roles-impl", "coder", "built"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runPlanT(t, b, repo, "priority", "roles", "high", "--as", "charlie"); code != 0 || strings.Contains(out, "request priority") {
		t.Fatalf("a done request got a hint: %d %q", code, out)
	}
	// "was" is the level in force: the plan file's when the person set none.
	if code, out, _ := runPlanT(t, b, repo, "priority", "release", "normal", "--as", "charlie"); code != 0 || out != "release: normal (was low)\n" {
		t.Fatalf("normal over the file's low: %d %q", code, out)
	}
}

// Only a person: an agent is refused before anything is written.
func TestPlanPriorityRefusesAnAgent(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.PlanIgnoreKey(b.Project()))
	})
	t.Setenv("HERDR_ENV", "")
	repo, _ := planRepo(t, testPlan)
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@HEAD"); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	old := requestDeps
	t.Cleanup(func() { requestDeps = old })
	requestDeps = requestCallerDeps{Getenv: func(string) string { return "" }, AgentCaller: func(context.Context) (string, error) { return "master", nil }}
	if code, _, errw := runPlanT(t, b, repo, "priority", "roles", "high", "--as", "charlie"); code == 0 || !strings.Contains(errw, "the person's act") {
		t.Fatalf("an agent: %d %s", code, errw)
	}
	if f, _ := b.PlanFacts(ctx); f["roles"].Priority != "" {
		t.Fatalf("an agent set %q", f["roles"].Priority)
	}
}

// The person's plan priority (--as) passes the identity guard in an agent's
// terminal; without --as it does not.
func TestPlanPriorityIdentityGuard(t *testing.T) {
	pane := func(k string) string {
		if k == "AGENTBUS_TERM_ID" {
			return "p1"
		}
		return ""
	}
	if err := identityGuard(pane, "plan", []string{"priority", "roles", "high", "--as", "charlie"}); err != nil {
		t.Fatalf("the person's priority: %v", err)
	}
	if err := identityGuard(pane, "plan", []string{"priority", "roles", "high"}); err == nil {
		t.Fatal("a priority without --as passed")
	}
}
