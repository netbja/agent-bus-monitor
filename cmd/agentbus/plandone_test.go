package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/forge"
)

// onboarding §9.3 (spec docs/superpowers/specs/2026-11-27-project-onboarding-design.md):
// plan done on an agent task with a check reads the merged head itself and
// records done, with that head, only when the deciding approval quoted the
// check at it; a head that cannot be read refuses.

const checkedPlan = `{"plan":"v1","goal":"g","tasks":[
 {"id":"roles","title":"Roles","owner":"coder","board":"roles-impl","check":"go test ./plan -run TestX"},
 {"id":"notes","title":"Notes","owner":"coder","board":"notes-impl"}]}`

const (
	h1 = "1111111111111111111111111111111111111111"
	h2 = "2222222222222222222222222222222222222222"
)

func TestPlanDoneGuard(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.VerdictsKey(b.Project()))
	})
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, checkedPlan)
	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{}); err != nil { // the project exists
		t.Fatal(err)
	}
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	if code, _, errw := runPlanT(t, b, repo, "link", "roles", "--pr", "412"); code != 0 {
		t.Fatalf("link: %s", errw)
	}
	merged, readErr := h2, error(nil)
	old := planMergedPR
	planMergedPR = func(_ context.Context, _ string, _ func(string) string, pr int) (string, error) {
		if pr != 412 {
			t.Errorf("read PR %d", pr)
		}
		return merged, readErr
	}
	t.Cleanup(func() { planMergedPR = old })
	approve := func(head, text string) {
		t.Helper()
		if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: "roles-impl", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: head, Message: text}); err != nil {
			t.Fatal(err)
		}
	}
	done := func() (int, string, string) { return runPlanT(t, b, repo, "done", "roles") }
	refused := func(why, want string) {
		t.Helper()
		code, _, errw := done()
		if code == 0 || !strings.Contains(errw, want) {
			t.Fatalf("%s: %d %q, want %q", why, code, errw, want)
		}
		if f, _ := b.PlanFacts(ctx); f["roles"].Done != 0 {
			t.Fatalf("%s: recorded done: %+v", why, f["roles"])
		}
	}
	refused("no verdict", "the merged head 2222222 was not approved with the check (approved: none)")
	approve(h1, "approve: fine")
	refused("no evidence line", "(approved: none)")
	approve(h1, "check: go test ./plan -run TestX => exit 1 at "+h1)
	refused("exit 1", "roles: check failed at 1111111")
	approve(h1, "check: go test ./plan -run TestX => exit 0 at "+h1)
	refused("merged at H2, approved at H1", "the merged head 2222222 was not approved with the check (approved: 1111111); ask a reviewer to run the check at 2222222")
	readErr = errors.New("gh: not logged in")
	refused("the forge read fails", "cannot read PR 412's merged head: gh: not logged in")
	readErr = nil
	approve(h2, "check: go test ./plan -run TestX => exit 0 at "+h2)
	code, out, errw := done()
	if code != 0 || !strings.Contains(out, "roles recorded done at 2222222") {
		t.Fatalf("re-reviewed at H2: %d %q %q", code, out, errw)
	}
	if f, _ := b.PlanFacts(ctx); f["roles"].Done == 0 || f["roles"].Head != h2 {
		t.Fatalf("the done fact: %+v", f["roles"])
	}
	// A task without a check records done as before, with no head.
	if code, out, _ := runPlanT(t, b, repo, "done", "notes"); code != 0 || out != "notes recorded done\n" {
		t.Fatalf("no check: %d %q", code, out)
	}
	if f, _ := b.PlanFacts(ctx); f["notes"].Head != "" {
		t.Fatalf("a head on an unchecked task: %+v", f["notes"])
	}
}

func TestMergedHeadOf(t *testing.T) {
	if h, err := mergedHeadOf(forge.GitHub, []byte(`{"state":"MERGED","headRefOid":"`+h1+`"}`)); err != nil || h != h1 {
		t.Errorf("github merged: %q %v", h, err)
	}
	if _, err := mergedHeadOf(forge.GitHub, []byte(`{"state":"OPEN","headRefOid":"`+h1+`"}`)); err == nil || !strings.Contains(err.Error(), "open, not merged") {
		t.Errorf("github open: %v", err)
	}
	if h, err := mergedHeadOf(forge.GitLab, []byte(`{"state":"merged","sha":"`+h2+`","headRefOid":"`+h1+`"}`)); err != nil || h != h2 {
		t.Errorf("gitlab merged: %q %v", h, err)
	}
	if _, err := mergedHeadOf(forge.GitHub, []byte(`not json`)); err == nil {
		t.Error("bad JSON read")
	}
}
