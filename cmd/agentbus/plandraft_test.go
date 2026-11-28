package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// onboarding §8.1, §8.5 (spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md).

const draftPlanV0 = `{"plan":"v0","goal":"notes","checks":"required","tasks":[
 {"id":"store","title":"Store","owner":"coder","check":"go test ./store/..."}]}`

const draftBuildMD = `# notes: build sheet (v0)
## Goal
A notes app.
## Users
## Constraints
## Stack
## Definition of done
- [dod-1] a note survives a restart — check: go test ./store/...
## Out of scope
## How to run
## How to verify
## Risks
## Decisions
`

// draftRepo is a repository whose commit() writes files and commits them,
// returning the commit.
type draftRepo struct {
	t   *testing.T
	dir string
}

func newDraftRepo(t *testing.T) *draftRepo {
	r := &draftRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q")
	return r
}

func (r *draftRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *draftRepo) commit(files map[string]string) string {
	r.t.Helper()
	for p, body := range files {
		os.MkdirAll(filepath.Join(r.dir, filepath.Dir(p)), 0o755)
		if body == "" {
			os.Remove(filepath.Join(r.dir, p))
			continue
		}
		os.WriteFile(filepath.Join(r.dir, p), []byte(body), 0o644)
	}
	r.git("add", "-A")
	r.git("commit", "-qm", "c", "--allow-empty")
	return r.git("rev-parse", "HEAD")
}

// planAs runs plan as agent ("" the person's shell).
func planAs(t *testing.T, b *bus.Bus, repo, agent string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runPlan(context.Background(), b, repo, func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return agent
		}
		return ""
	}, args, &out, &errw)
	return code, out.String(), errw.String()
}

type draftOut struct {
	QID string `json:"question_id"`
	Ask string `json:"ask"`
}

func mustDraft(t *testing.T, b *bus.Bus, repo, agent string, args ...string) draftOut {
	t.Helper()
	code, out, errw := planAs(t, b, repo, agent, append([]string{"draft"}, args...)...)
	if code != 0 {
		t.Fatalf("plan draft %v as %s: %d %s", args, agent, code, errw)
	}
	var d draftOut
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.QID == "" || d.Ask == "" {
		t.Fatalf("plan draft output %q: %v", out, err)
	}
	return d
}

func draftBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, r := dialMain(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() {
		r.Del(context.Background(), bus.PlanKey(p), bus.PlanFactsKey(p), bus.PlanIgnoreKey(p), bus.PlanDraftKey(p), bus.AsksKey(p))
	})
	t.Setenv("HERDR_ENV", "")
	if _, err := b.Status(ctx, "master", "idle", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	return b
}

func askState(t *testing.T, b *bus.Bus, id string) bus.Ask {
	t.Helper()
	asks, err := b.Asks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no ask %s", id)
	return bus.Ask{}
}

func TestPlanDraftCallers(t *testing.T) {
	b := draftBus(t)
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	ref := "docs/plans/v0.json@" + sha
	for _, agent := range []string{"", "coder", "foureyes"} {
		if code, _, errw := planAs(t, b, repo.dir, agent, "draft", ref); code == 0 || !strings.Contains(errw, "master's or the architect's") {
			t.Errorf("draft as %q: %d %q", agent, code, errw)
		}
		if code, _, errw := planAs(t, b, repo.dir, agent, "draft", "--drop"); code == 0 || !strings.Contains(errw, "master's or the architect's") {
			t.Errorf("drop as %q: %d %q", agent, code, errw)
		}
	}
	if _, ok, _ := b.PlanDraft(context.Background()); ok {
		t.Fatal("a refused caller wrote a draft")
	}
	mustDraft(t, b, repo.dir, "master", ref)
	if code, _, errw := planAs(t, b, repo.dir, "coder", "draft", "show"); code != 0 {
		t.Fatalf("show is anyone's: %d %s", code, errw)
	}
}

// The whole adoption, as the person and master see it.
func TestPlanDraftAdoption(t *testing.T) {
	b := draftBus(t)
	ctx := context.Background()
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	ref := "docs/plans/v0.json@" + sha

	// No draft: a plan with checks required is refused.
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", ref); code == 0 || !strings.Contains(errw, "loaded only as an approved draft") {
		t.Fatalf("checks required, no draft: %d %q", code, errw)
	}
	d := mustDraft(t, b, repo.dir, "architect", ref)
	a := askState(t, b, d.Ask)
	q := a.Questions[0]
	if a.From != "architect" || a.Task != "onboarding" || q.ID != d.QID || !strings.Contains(q.Question, d.QID) ||
		!strings.Contains(q.Question, "docs/plans/v0.json, 1 tasks") || !strings.Contains(q.Context, "A notes app.") || !strings.Contains(q.Context, sha[:7]) {
		t.Fatalf("the question: %+v", a)
	}
	for _, o := range q.Options {
		if o.Recommended {
			t.Fatalf("option %s recommended: adopting is the person's call", o.Label)
		}
	}
	if code, out, _ := planAs(t, b, repo.dir, "", "draft", "show"); code != 0 || !strings.Contains(out, "not approved") || !strings.Contains(out, d.QID) {
		t.Fatalf("show before the yes: %d %s", code, out)
	}
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", ref); code == 0 || !strings.Contains(errw, "awaits the person's yes") {
		t.Fatalf("load before the yes: %d %q", code, errw)
	}
	// An agent's yes is no approval: the answer is the asker's own.
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "architect", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", ref); code == 0 {
		t.Fatalf("the asker's own yes loaded: %s", errw)
	}
	// The architect re-drafts (answered: a new ask), and the person says yes.
	d = mustDraft(t, b, repo.dir, "architect", ref)
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := planAs(t, b, repo.dir, "", "draft", "show", "--json"); code != 0 || !strings.Contains(out, `"approved":"`) {
		t.Fatalf("show after the yes: %d %s", code, out)
	}
	// Another file is not the draft.
	other := repo.commit(map[string]string{"docs/plans/v0.json": strings.Replace(draftPlanV0, "Store", "Storage", 1)})
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", "docs/plans/v0.json@"+other); code == 0 || !strings.Contains(errw, "not the draft the person approved") {
		t.Fatalf("another file: %d %q", code, errw)
	}
	if code, out, errw := planAs(t, b, repo.dir, "master", "load", ref); code != 0 || !strings.Contains(out, "the draft the person approved") {
		t.Fatalf("load: %d %s %s", code, out, errw)
	}
	rec, ok, _ := b.Plan(ctx)
	if !ok || rec.Approved == "" || rec.Commit != sha {
		t.Fatalf("plan %+v", rec)
	}
	if _, ok, _ := b.PlanDraft(ctx); ok {
		t.Fatal("the draft is kept after its load")
	}
}

func TestPlanDraftReadsTheBuildSheet(t *testing.T) {
	b := draftBus(t)
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0})
	if code, _, errw := planAs(t, b, repo.dir, "architect", "draft", "docs/plans/v0.json@"+sha); code == 0 || !strings.Contains(errw, "BUILD.md not found") {
		t.Fatalf("checks required, no BUILD.md: %d %q", code, errw)
	}
	sha = repo.commit(map[string]string{"BUILD.md": strings.Replace(draftBuildMD, " — check: go test ./store/...", "", 1)})
	if code, _, errw := planAs(t, b, repo.dir, "architect", "draft", "docs/plans/v0.json@"+sha); code == 0 || !strings.Contains(errw, "dod-") {
		t.Fatalf("an item without its check: %d %q", code, errw)
	}
	// --build names another file, and the load rereads that one.
	sha = repo.commit(map[string]string{"docs/BUILD-notes.md": draftBuildMD})
	d := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha, "--build", "docs/BUILD-notes.md")
	if _, _, err := b.Answer(context.Background(), d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", "docs/plans/v0.json@"+sha); code != 0 {
		t.Fatalf("load with --build: %s", errw)
	}
	// A plan without checks required drafts without a build sheet.
	repo2 := newDraftRepo(t)
	sha = repo2.commit(map[string]string{"docs/plans/v1.json": strings.Replace(draftPlanV0, `"checks":"required",`, "", 1)})
	b2 := draftBus(t)
	mustDraft(t, b2, repo2.dir, "architect", "docs/plans/v1.json@"+sha)
}

// §8.1 step 6: when a re-draft replaces the ask, and when it opens one.
func TestPlanDraftAskRules(t *testing.T) {
	b := draftBus(t)
	ctx := context.Background()
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	d1 := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha)

	// The same files at a new commit: the same qid and wording, the open ask
	// replaced (no SameQuestion refusal).
	sha2 := repo.commit(map[string]string{"NEXT-DIRECTIONS.md": "v1\n"})
	d2 := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha2)
	if d2.QID != d1.QID || d2.Ask == d1.Ask {
		t.Fatalf("same files, new commit: %+v then %+v", d1, d2)
	}
	if a := askState(t, b, d1.Ask); a.State != "replaced" {
		t.Fatalf("the old ask: %s", a.State)
	}
	if askState(t, b, d1.Ask).Questions[0].Question != askState(t, b, d2.Ask).Questions[0].Question {
		t.Fatal("the same qid gave another wording")
	}
	// A changed file while the ask is open: replaced, and the old qid
	// approves nothing.
	sha3 := repo.commit(map[string]string{"docs/plans/v0.json": strings.Replace(draftPlanV0, "Store", "Storage", 1)})
	d3 := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha3)
	if d3.QID == d2.QID || askState(t, b, d2.Ask).State != "replaced" {
		t.Fatalf("a changed file: %+v", d3)
	}
	if _, _, err := b.Answer(ctx, d2.Ask, d2.QID, "charlie", "yes", ""); err == nil {
		t.Fatal("the old qid was answered after its replacement")
	}
	// After a no, a later and free text: a new ask each time (no ASK_NOT_OPEN).
	prev := d3
	for _, ans := range []struct{ choice, text string }{{"no", ""}, {"later", ""}, {"", "drop the store task"}} {
		if _, _, err := b.Answer(ctx, prev.Ask, prev.QID, "charlie", ans.choice, ans.text); err != nil {
			t.Fatal(err)
		}
		next := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha3)
		if next.Ask == prev.Ask || next.QID != prev.QID || askState(t, b, next.Ask).State != "open" {
			t.Fatalf("after %+v: %+v", ans, next)
		}
		prev = next
	}
	// Master may not re-draft the architect's draft; its drop leaves the
	// architect's ask open and says so.
	if code, _, errw := planAs(t, b, repo.dir, "master", "draft", "docs/plans/v0.json@"+sha3); code == 0 || !strings.Contains(errw, "architect's: plan draft --drop it first") {
		t.Fatalf("master over the architect's draft: %d %q", code, errw)
	}
	if code, out, errw := planAs(t, b, repo.dir, "master", "draft", "--drop"); code != 0 || !strings.Contains(out, "left alone") {
		t.Fatalf("master's drop: %d %s %s", code, out, errw)
	}
	if askState(t, b, prev.Ask).State != "open" {
		t.Fatal("master's drop withdrew the architect's ask")
	}
	// The architect has that ask open: its draft is refused, nothing written.
	if code, _, errw := planAs(t, b, repo.dir, "architect", "draft", "docs/plans/v0.json@"+sha3); code == 0 || !strings.Contains(errw, "you have ask "+prev.Ask+" open") {
		t.Fatalf("draft with another ask open: %d %q", code, errw)
	}
	if _, ok, _ := b.PlanDraft(ctx); ok {
		t.Fatal("a refused draft was written")
	}
	// Its own drop withdraws its open ask.
	if err := b.Withdraw(ctx, "architect", prev.Ask); err != nil {
		t.Fatal(err)
	}
	d := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha3)
	if code, out, errw := planAs(t, b, repo.dir, "architect", "draft", "--drop"); code != 0 || !strings.Contains(out, "withdrawn") {
		t.Fatalf("the architect's drop: %d %s %s", code, out, errw)
	}
	if askState(t, b, d.Ask).State != "withdrawn" {
		t.Fatal("the architect's drop kept its ask open")
	}
	// The same bytes at another path: another qid.
	sha4 := repo.commit(map[string]string{"docs/plans/v0b.json": strings.Replace(draftPlanV0, "Store", "Storage", 1)})
	if d4 := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0b.json@"+sha4); d4.QID == d.QID {
		t.Fatal("the qid does not bind the path")
	}
}

// b1 drafts only with no active plan; a load over a draft never takes the
// plain path.
func TestPlanDraftOverAnActivePlanIsStepB2(t *testing.T) {
	b := draftBus(t)
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.22.json": testPlan, "docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	if code, _, errw := planAs(t, b, repo.dir, "master", "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatal(errw)
	}
	if code, _, errw := planAs(t, b, repo.dir, "architect", "draft", "docs/plans/v0.json@"+sha); code == 0 || !strings.Contains(errw, "step b2") {
		t.Fatalf("a draft over an active plan: %d %q", code, errw)
	}
}

func TestPlanDraftChecksMissions(t *testing.T) {
	b := draftBus(t)
	repo := newDraftRepo(t)
	body := `{"plan":"v1","goal":"g","tasks":[{"id":"t1","title":"T1","owner":"coder","mission":"chg/m1","check":"go test ./a"}]}`
	sha := repo.commit(map[string]string{"docs/plans/v1.json": body, "docs/changes/chg/missions.json": `{"change": "chg", "reviewer": "codex", "missions": [
  {"id": "m1", "title": "One", "agent": "coder", "allowed_paths": ["a/**"], "test": "true", "brief": "b"}]}`})
	if code, _, errw := planAs(t, b, repo.dir, "architect", "draft", "docs/plans/v1.json@"+sha); code == 0 || !strings.Contains(errw, "differs from mission chg/m1") {
		t.Fatalf("a differing mission test: %d %q", code, errw)
	}
}
