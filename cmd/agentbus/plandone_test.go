package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/plan"
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
	// A task without a check records done as before, with no head: a plain
	// done clears a head left by an earlier record (the script's else).
	if err := b.PlanDoneAt(ctx, "notes", h1); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runPlanT(t, b, repo, "done", "notes"); code != 0 || out != "notes recorded done\n" {
		t.Fatalf("no check: %d %q", code, out)
	}
	if f, _ := b.PlanFacts(ctx); f["notes"].Head != "" || f["notes"].Done == 0 {
		t.Fatalf("a head kept on an unchecked task: %+v", f["notes"])
	}
	// A plan record Go cannot decode, whose task list the script still reads:
	// refused, never recorded through the unchecked path (reviewer-codex).
	raw, err := r.Get(ctx, bus.PlanKey(b.Project())).Result()
	if err != nil {
		t.Fatal(err)
	}
	r.Set(ctx, bus.PlanKey(b.Project()), strings.Replace(raw, `"loaded_at":`, `"loaded_at":"not a time","x":`, 1), 0)
	r.HDel(ctx, bus.PlanFactsKey(b.Project()), "roles")
	if code, _, errw := runPlanT(t, b, repo, "done", "roles"); code == 0 || !strings.Contains(errw, "reading the plan") {
		t.Fatalf("an undecodable plan record: %d %q", code, errw)
	}
	if f, _ := b.PlanFacts(ctx); f["roles"].Done != 0 {
		t.Fatalf("recorded done from an unread plan: %+v", f["roles"])
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

const personPlan = `{"plan":"v1","goal":"g","tasks":[
 {"id":"release","title":"Release","owner":"person","check":"open busmon, the tab shows v1"}]}`

// onboarding §9.4: plan done on a person's task with a check passes only on
// the person's typed passes to the question id of the active plan's check,
// and records the entry and the id.
func TestPlanDonePerson(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.AsksKey(b.Project()))
	})
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, personPlan)
	if _, err := b.Status(ctx, "master", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	f, err := plan.Parse([]byte(personPlan))
	if err != nil {
		t.Fatal(err)
	}
	qid := plan.PersonCheckQID(f, f.Tasks[0])
	refused := func(why string) {
		t.Helper()
		code, _, errw := runPlanT(t, b, repo, "done", "release")
		if code == 0 || !strings.Contains(errw, "release: the person has not answered passes to its current check (id "+qid+")") {
			t.Fatalf("%s: %d %q", why, code, errw)
		}
		if fs, _ := b.PlanFacts(ctx); fs["release"].Done != 0 {
			t.Fatalf("%s: recorded done", why)
		}
	}
	refused("no ask")
	var askAbout func(from, id, task string) string
	ask := func(from string, id string) string { return askAbout(from, id, "release") }
	askAbout = func(from, id, task string) string {
		t.Helper()
		a, _, err := b.Ask(ctx, bus.AskDef{From: from, Title: "Release check", Task: task, Questions: []bus.AskQuestion{
			{ID: id, Header: "Check", Question: "open busmon, the tab shows v1: does it pass?", Options: []bus.AskOption{{Label: "passes"}, {Label: "fails"}}}}}, "")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	failed := ask("master", qid)
	if _, _, err := b.Answer(ctx, failed, qid, "charlie", "fails", ""); err != nil {
		t.Fatal(err)
	}
	refused("fails")
	wrong := ask("architect", "check-0000000000000000") // an older check's id
	if _, _, err := b.Answer(ctx, wrong, "check-0000000000000000", "charlie", "passes", ""); err != nil {
		t.Fatal(err)
	}
	refused("passes to another id")
	other := askAbout("docs", qid, "notes") // the same id, asked about another task
	if _, _, err := b.Answer(ctx, other, qid, "charlie", "passes", ""); err != nil {
		t.Fatal(err)
	}
	refused("passes on an ask about another task")
	passed := ask("sentinel", qid)
	if _, _, err := b.Answer(ctx, passed, qid, "charlie", "passes", ""); err != nil {
		t.Fatal(err)
	}
	code, out, errw := runPlanT(t, b, repo, "done", "release")
	if code != 0 || !strings.Contains(out, "release recorded done (the person's passes") {
		t.Fatalf("passes: %d %q %q", code, out, errw)
	}
	fs, _ := b.PlanFacts(ctx)
	if rel := fs["release"]; rel.Done == 0 || rel.ApprovedQID != qid || rel.Approved == "" || rel.Head != "" {
		t.Fatalf("the done fact: %+v", rel)
	}
	// Stale check: a reload changes the check (same task id), so the passes
	// given to the old one no longer answers it.
	repo2, sha2 := planRepo(t, strings.Replace(personPlan, "shows v1", "shows v2", 1))
	if code, _, errw := runPlanT(t, b, repo2, "load", "docs/plans/v0.22.json@"+sha2); code != 0 {
		t.Fatalf("reload: %s", errw)
	}
	r.HDel(ctx, bus.PlanFactsKey(b.Project()), "release")
	if code, _, errw := runPlanT(t, b, repo2, "done", "release"); code == 0 || !strings.Contains(errw, "has not answered passes to its current check") {
		t.Fatalf("a changed check: %d %q", code, errw)
	}
	repo = repo2
	// The record expired (7 days): the ask is gone, so nothing approves.
	r.HDel(ctx, bus.PlanFactsKey(b.Project()), "release")
	r.Del(ctx, bus.AsksKey(b.Project()))
	if code, _, _ := runPlanT(t, b, repo, "done", "release"); code == 0 {
		t.Fatal("the record expired: recorded done")
	}
}

// onboarding §7.3, end to end: plan load reads the mission's test at the
// plan's commit and refuses a check that differs, and a missions.json it
// cannot read.
func TestPlanLoadChecksMissions(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project())) })
	t.Setenv("HERDR_ENV", "")
	if _, err := b.Status(ctx, "master", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	withMissions := func(check string, missions bool) (string, string) {
		t.Helper()
		body := `{"plan":"v1","goal":"g","tasks":[{"id":"t1","title":"T1","owner":"coder","mission":"chg/m1","check":"` + check + `"}]}`
		dir, _ := planRepo(t, body)
		if missions {
			os.MkdirAll(filepath.Join(dir, "docs/changes/chg"), 0o755)
			os.WriteFile(filepath.Join(dir, "docs/changes/chg/missions.json"), []byte(`{"change": "chg", "reviewer": "codex", "missions": [
  {"id": "m1", "title": "One", "agent": "coder", "allowed_paths": ["a/**"], "test": "true", "brief": "b"}]}`), 0o644)
			for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "missions"}} {
				c := exec.Command("git", args...)
				c.Dir = dir
				c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
				if out, err := c.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
		}
		return dir, "docs/plans/v0.22.json@HEAD"
	}
	dir, ref := withMissions("true", true)
	if code, _, errw := runPlanT(t, b, dir, "load", ref); code != 0 {
		t.Fatalf("matching: %s", errw)
	}
	dir, ref = withMissions("go test ./a", true)
	if code, _, errw := runPlanT(t, b, dir, "load", ref); code == 0 || !strings.Contains(errw, `its check differs from mission chg/m1's test "true"`) {
		t.Fatalf("a differing check: %d %q", code, errw)
	}
	dir, ref = withMissions("true", false)
	if code, _, errw := runPlanT(t, b, dir, "load", ref); code == 0 || !strings.Contains(errw, "cannot read chg's missions.json") {
		t.Fatalf("no missions.json: %d %q", code, errw)
	}
}
