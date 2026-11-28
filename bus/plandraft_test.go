package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// onboarding §8.1, §8.4, §8.5 (spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md).

const draftFile = `{"plan":"v0","checks":"required","tasks":[{"id":"t1","title":"x","owner":"coder","check":"go test ./..."}]}`
const draftBuild = "# p: build sheet (v0)\n"

// draftFixture records a draft of draftFile by architect with its adoption
// ask open, as plan draft does, and returns it.
func draftFixture(t *testing.T, b *Bus) PlanDraft {
	t.Helper()
	ctx := context.Background()
	cleanPlan(t, b)
	t.Cleanup(func() { b.r.Del(context.Background(), PlanDraftKey(b.Project()), AsksKey(b.Project())) })
	b.Status(ctx, "master", "idle", "x", AgentIdent{})
	d := PlanDraft{Plan: "v0", Path: "docs/plans/v0.json", Commit: "c1", File: draftFile, Build: draftBuild, BuildPath: "BUILD.md", By: "architect"}
	d.QID = PlanDraftQID(d.Path, []byte(d.File), []byte(d.Build), "", nil)
	d.Ask = draftAsk(t, b, "architect", d.QID, "")
	if err := b.SetPlanDraft(ctx, d, PlanDraft{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := b.PlanDraft(ctx)
	if err != nil || !ok {
		t.Fatalf("read back: %v %v", ok, err)
	}
	return got
}

func draftAsk(t *testing.T, b *Bus, from, qid, replaces string) string {
	t.Helper()
	return askQ(t, b, from, adoptQ(qid), replaces)
}

// adoptQ is the tests' adoption question for qid (plan draft builds the
// real one).
func adoptQ(qid string) AskQuestion {
	return AskQuestion{ID: qid, Header: "Adopt v0", Question: "Adopt plan v0? id " + qid, Options: []AskOption{{Label: "yes"}, {Label: "no"}, {Label: "later"}}}
}

func askQ(t *testing.T, b *Bus, from string, q AskQuestion, replaces string) string {
	t.Helper()
	id, _, err := b.Ask(context.Background(), AskDef{From: from, Title: "Adopt plan v0", Task: "onboarding", Questions: []AskQuestion{q}}, replaces)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPlanDraftQIDBindsEveryInput(t *testing.T) {
	base := PlanDraftQID("docs/plans/v0.json", []byte(draftFile), []byte(draftBuild), "", nil)
	if !strings.HasPrefix(base, "plan-") || len(base) != len("plan-")+16 {
		t.Fatalf("qid %q", base)
	}
	if PlanDraftQID("docs/plans/v0.json", []byte(draftFile), []byte(draftBuild), "", nil) != base {
		t.Fatal("the same inputs gave another qid")
	}
	for name, q := range map[string]string{
		"path":     PlanDraftQID("docs/plans/v1.json", []byte(draftFile), []byte(draftBuild), "", nil),
		"plan":     PlanDraftQID("docs/plans/v0.json", []byte(draftFile+" "), []byte(draftBuild), "", nil),
		"build":    PlanDraftQID("docs/plans/v0.json", []byte(draftFile), []byte(draftBuild+" "), "", nil),
		"replaces": PlanDraftQID("docs/plans/v0.json", []byte(draftFile), []byte(draftBuild), "docs/plans/a.json@c0", nil),
		"stops":    PlanDraftQID("docs/plans/v0.json", []byte(draftFile), []byte(draftBuild), "", []string{"t9"}),
		// the separators: bytes moved from the plan to the build sheet
		"split": PlanDraftQID("docs/plans/v0.json", []byte(draftFile[:len(draftFile)-1]), []byte(draftFile[len(draftFile)-1:]+draftBuild), "", nil),
	} {
		if q == base {
			t.Errorf("a changed %s kept the qid", name)
		}
	}
	if PlanDraftQID("p", nil, nil, "", []string{"b", "a"}) != PlanDraftQID("p", nil, nil, "", []string{"a", "b"}) {
		t.Error("stops are sorted: their order changed the qid")
	}
}

// The record is written only over the one its writer read (§8.1 step 7).
func TestSetPlanDraftRefusesAChangedRecord(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	d := draftFixture(t, b)
	if d.At == 0 || d.Build != draftBuild || d.File != draftFile || d.BuildPath != "BUILD.md" {
		t.Fatalf("record %+v", d)
	}
	next := d
	next.Commit = "c2"
	if err := b.SetPlanDraft(ctx, next, PlanDraft{}); !errors.Is(err, ErrDraftChanged) {
		t.Fatalf("over a record it did not read: %v", err)
	}
	if err := b.SetPlanDraft(ctx, next, d); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPlanDraft(ctx, next, d); !errors.Is(err, ErrDraftChanged) {
		t.Fatalf("over a stale read: %v", err)
	}
	if err := b.DropPlanDraft(ctx, d); !errors.Is(err, ErrDraftChanged) {
		t.Fatalf("drop of a stale read: %v", err)
	}
	cur, _, _ := b.PlanDraft(ctx)
	if err := b.DropPlanDraft(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.PlanDraft(ctx); ok || err != nil {
		t.Fatalf("dropped: %v %v", ok, err)
	}
}

func TestPlanDraftKeyKind(t *testing.T) {
	if c, k := ClassOf("demo", PlanDraftKey("demo")); c != KeyRecord || k != "plan:draft" {
		t.Fatalf("%v %q", c, k)
	}
	if aclMasterOnly["plan:draft"] {
		t.Fatal("plan:draft is master's only: the architect could not draft")
	}
}

// ACL: the agent user writes the draft (the architect drafts), never the
// plan (§8.1).
func TestACLPlanDraftIsAgentWritable(t *testing.T) {
	b := dialTest(t)
	p := b.Project()
	if got := aclDryRun(t, b.r, ACLAgent, "set", p+":plan:draft", "x"); got != "OK" {
		t.Errorf("the agent may not write {p}:plan:draft: %s", got)
	}
	if got := aclDryRun(t, b.r, ACLAgent, "set", p+":plan", "x"); got == "OK" {
		t.Error("the agent may write {p}:plan")
	}
}

func loadDraft(b *Bus, d PlanDraft) (string, error) {
	return b.LoadPlanDraft(context.Background(), d.Path, d.Commit, []byte(d.File), []byte(d.Build), []string{"t1"}, adoptQ)
}

func TestLoadPlanDraft(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	d := draftFixture(t, b)
	if _, err := loadDraft(b, d); err == nil || !strings.Contains(err.Error(), "awaits the person's yes") {
		t.Fatalf("unapproved: %v", err)
	}
	other := d
	other.File = strings.Replace(draftFile, `"x"`, `"y"`, 1)
	if _, err := loadDraft(b, other); err == nil || !strings.Contains(err.Error(), "not the draft the person approved") {
		t.Fatalf("another file: %v", err)
	}
	// A plain load is refused while a draft is recorded (the script).
	if err := b.LoadPlan(ctx, d.Path, d.Commit, []byte(d.File), []string{"t1"}); !errors.Is(err, ErrDraftRecorded) {
		t.Fatalf("plain load over a draft: %v", err)
	}
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDraft(b, other); err == nil {
		t.Fatal("another file after the yes: loaded")
	}
	entry, err := loadDraft(b, d)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok, err := b.Plan(ctx)
	if err != nil || !ok || rec.File != draftFile || rec.Approved != entry || entry == "" || rec.Commit != "c1" {
		t.Fatalf("loaded %+v %v %v (entry %s)", rec, ok, err, entry)
	}
	if _, ok, _ := b.PlanDraft(ctx); ok {
		t.Fatal("the draft is still recorded after its load")
	}
	if _, err := loadDraft(b, d); !errors.Is(err, ErrNoDraft) {
		t.Fatalf("a second load: %v", err)
	}
}

// The race (§8.5): between part 1 and the script the draft is replaced or
// dropped; the load writes nothing, and the new draft is kept.
func TestLoadPlanDraftRace(t *testing.T) {
	for _, c := range []struct {
		name string
		hook func(t *testing.T, b *Bus, d PlanDraft)
		kept bool
	}{
		{"re-draft", func(t *testing.T, b *Bus, d PlanDraft) {
			n := d
			n.File = strings.Replace(draftFile, `"x"`, `"z"`, 1)
			n.QID = PlanDraftQID(n.Path, []byte(n.File), []byte(n.Build), "", nil)
			n.Ask = draftAsk(t, b, "architect", n.QID, "")
			if err := b.SetPlanDraft(context.Background(), n, d); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"drop", func(t *testing.T, b *Bus, d PlanDraft) {
			if err := b.DropPlanDraft(context.Background(), d); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			d := draftFixture(t, b)
			if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
				t.Fatal(err)
			}
			afterDraftCheck = func() { c.hook(t, b, d) }
			defer func() { afterDraftCheck = nil }()
			if _, err := loadDraft(b, d); !errors.Is(err, ErrDraftChanged) {
				t.Fatalf("load across a %s: %v", c.name, err)
			}
			if _, ok, _ := b.Plan(ctx); ok {
				t.Fatal("the plan was written")
			}
			cur, ok, _ := b.PlanDraft(ctx)
			if ok != c.kept || (ok && cur.QID == d.QID) {
				t.Fatalf("draft after the %s: %v %+v", c.name, ok, cur)
			}
		})
	}
}

// Each condition of planLoadDraft on its own: the script is run with one
// argument or one broker value changed, and writes nothing.
func TestPlanLoadDraftScriptConditions(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, b *Bus, d *PlanDraft, args *draftLoadArgs)
		want  string
	}{
		{"file", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.file += " " }, "PLAN_DRAFT_CHANGED"},
		{"build", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.build += " " }, "PLAN_DRAFT_CHANGED"},
		{"ask", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.ask = "1-1" }, "PLAN_DRAFT_CHANGED"},
		{"qid", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.qid = "plan-0000000000000000" }, "PLAN_DRAFT_CHANGED"},
		{"path", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.path = "docs/plans/other.json" }, "PLAN_DRAFT_CHANGED"},
		{"kept answer not yes", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.HSet(context.Background(), AsksKey(b.Project()), d.Ask+"|a|"+d.QID, `{"choice":"no","by":"charlie","at":1}`)
		}, "PLAN_NOT_APPROVED"},
		{"kept answer withdrawn", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.HSet(context.Background(), AsksKey(b.Project()), d.Ask+"|a|"+d.QID, `{"choice":"yes","by":"charlie","at":1,"withdrawn":true}`)
		}, "PLAN_NOT_APPROVED"},
		{"kept answer by the asker", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.HSet(context.Background(), AsksKey(b.Project()), d.Ask+"|a|"+d.QID, `{"choice":"yes","by":"architect","at":1}`)
		}, "PLAN_NOT_APPROVED"},
		{"kept answer missing", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.HDel(context.Background(), AsksKey(b.Project()), d.Ask+"|a|"+d.QID)
		}, "PLAN_NOT_APPROVED"},
		{"entry said no", func(t *testing.T, b *Bus, d *PlanDraft, a *draftLoadArgs) {
			a.entry = draftReply(t, b, d, map[string]any{"said": "no"})
		}, "PLAN_NOT_APPROVED"},
		{"entry untyped", func(t *testing.T, b *Bus, d *PlanDraft, a *draftLoadArgs) {
			a.entry = draftReply(t, b, d, map[string]any{"typed": "no"})
		}, "PLAN_NOT_APPROVED"},
		{"entry another qid", func(t *testing.T, b *Bus, d *PlanDraft, a *draftLoadArgs) {
			a.entry = draftReply(t, b, d, map[string]any{"qid": "plan-1111111111111111"})
		}, "PLAN_NOT_APPROVED"},
		{"entry another ask", func(t *testing.T, b *Bus, d *PlanDraft, a *draftLoadArgs) {
			a.entry = draftReply(t, b, d, map[string]any{"ref": "1-1"})
		}, "PLAN_NOT_APPROVED"},
		{"entry not a reply", func(t *testing.T, b *Bus, d *PlanDraft, a *draftLoadArgs) {
			a.entry = draftReply(t, b, d, map[string]any{"type": "directive"})
		}, "PLAN_NOT_APPROVED"},
		{"ask not the recorder's", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			setAskDef(t, b, d.Ask, func(def *AskDef) { def.From = "coder" })
		}, "PLAN_NOT_APPROVED"},
		{"recorder not a drafter", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			setAskDef(t, b, d.Ask, func(def *AskDef) { def.From = "coder" })
			setDraftBy(t, b, "coder")
		}, "PLAN_NOT_APPROVED"},
		{"ask record gone", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.HDel(context.Background(), AsksKey(b.Project()), d.Ask)
		}, "PLAN_NOT_APPROVED"},
		{"another header", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.question.Header = "Adopt v9" }, "PLAN_NOT_APPROVED"},
		{"another wording", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) { a.question.Question += "!" }, "PLAN_NOT_APPROVED"},
		{"other labels", func(_ *testing.T, _ *Bus, _ *PlanDraft, a *draftLoadArgs) {
			a.question.Options = []AskOption{{Label: "yes"}, {Label: "no"}}
		}, "PLAN_NOT_APPROVED"},
		{"a plan loaded since", func(t *testing.T, b *Bus, d *PlanDraft, _ *draftLoadArgs) {
			b.r.Set(context.Background(), PlanKey(b.Project()), `{"path":"docs/plans/a.json","commit":"c0","tasks":[],"file":"{}"}`, 0)
		}, "PLAN_ACTIVE_CHANGED"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			d := draftFixture(t, b)
			_, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", "")
			if err != nil {
				t.Fatal(err)
			}
			entry, err := b.TypedApproval(ctx, d.Ask, d.QID, "yes")
			if err != nil {
				t.Fatal(err)
			}
			a := draftLoadArgs{qid: d.QID, ask: d.Ask, path: d.Path, commit: d.Commit, file: d.File, build: d.Build, entry: entry, tasks: []string{"t1"}, question: adoptQ(d.QID)}
			c.setup(t, b, &d, &a)
			planBefore, _ := b.r.Get(ctx, PlanKey(b.Project())).Result()
			err = b.runPlanLoadDraft(ctx, a)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %s", err, c.want)
			}
			if after, _ := b.r.Get(ctx, PlanKey(b.Project())).Result(); after != planBefore {
				t.Fatal("the plan was written")
			}
			if _, ok, _ := b.PlanDraft(ctx); !ok {
				t.Fatal("the draft was deleted")
			}
		})
	}
	// And the same arguments unchanged pass: the cases above fail on their
	// one condition, not on the fixture.
	b := dialTest(t)
	ctx := context.Background()
	d := draftFixture(t, b)
	b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", "")
	entry, _ := b.TypedApproval(ctx, d.Ask, d.QID, "yes")
	if err := b.runPlanLoadDraft(ctx, draftLoadArgs{qid: d.QID, ask: d.Ask, path: d.Path, commit: d.Commit, file: d.File, build: d.Build, entry: entry, tasks: []string{"t1"}, question: adoptQ(d.QID)}); err != nil {
		t.Fatal(err)
	}
}

// draftReply adds a reply entry to the draft's ask, typed yes unless fields
// say otherwise, and returns its id.
func draftReply(t *testing.T, b *Bus, d *PlanDraft, fields map[string]any) string {
	t.Helper()
	f := map[string]any{"from": "charlie", "target": "architect", "type": "reply", "ref": d.Ask, "command": "yes",
		"human": "yes", "typed": "yes", "said": "yes", "qid": d.QID, "text_complete": "yes"}
	for k, v := range fields {
		f[k] = v
	}
	id, err := b.r.XAdd(context.Background(), &redis.XAddArgs{Stream: StreamKey(b.Project(), "cmd"), Values: f}).Result()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// setAskDef rewrites ask id's stored definition (as a local process could:
// the asks hash is agent-writable).
func setAskDef(t *testing.T, b *Bus, id string, edit func(*AskDef)) {
	t.Helper()
	def, err := b.askDef(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	edit(&def)
	raw, _ := json.Marshal(def)
	b.r.HSet(context.Background(), AsksKey(b.Project()), id, raw)
}

// setDraftBy rewrites the record's recorder in place (the key is agent-writable).
func setDraftBy(t *testing.T, b *Bus, by string) {
	t.Helper()
	ctx := context.Background()
	raw, err := b.r.Get(ctx, PlanDraftKey(b.Project())).Result()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(raw), &m)
	m["by"] = by
	out, _ := json.Marshal(m)
	b.r.Set(ctx, PlanDraftKey(b.Project()), out, 0)
}

// reviewer-codex on b1: the record is agent-writable, so a typed yes to the
// plan's qid adopts it only in the adoption ask: asked by the recorder, a
// drafter, with the adoption question. An agent's own ask carrying the qid,
// a recorder outside the drafters, or another wording adopts nothing, at
// show and at load.
func TestPlanDraftApprovalIsBoundToTheAdoptionAsk(t *testing.T) {
	for _, c := range []struct {
		name string
		ask  func(t *testing.T, b *Bus, qid string) string // the ask the person says yes to
		by   string                                        // the record's recorder
	}{
		{"an agent's ask with the qid", func(t *testing.T, b *Bus, qid string) string {
			return askQ(t, b, "coder", AskQuestion{ID: qid, Header: "Quick check", Question: "Is the build green?", Options: []AskOption{{Label: "yes"}, {Label: "no"}}}, "")
		}, "coder"},
		{"an agent's ask, recorded as the architect's", func(t *testing.T, b *Bus, qid string) string {
			return askQ(t, b, "coder", adoptQ(qid), "")
		}, "architect"},
		{"a drafter's ask, another wording", func(t *testing.T, b *Bus, qid string) string {
			q := adoptQ(qid)
			q.Question = "Is the build green? " + qid
			return askQ(t, b, "architect", q, "")
		}, "architect"},
		{"a drafter's ask, recorded by an agent", func(t *testing.T, b *Bus, qid string) string {
			return askQ(t, b, "architect", adoptQ(qid), "")
		}, "coder"},
		{"an agent's own adoption ask, recorded by it", func(t *testing.T, b *Bus, qid string) string {
			return askQ(t, b, "coder", adoptQ(qid), "")
		}, "coder"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			cleanPlan(t, b)
			t.Cleanup(func() { b.r.Del(context.Background(), PlanDraftKey(b.Project()), AsksKey(b.Project())) })
			b.Status(ctx, "master", "idle", "x", AgentIdent{})
			d := PlanDraft{Plan: "v0", Path: "docs/plans/v0.json", Commit: "c1", File: draftFile, Build: draftBuild, BuildPath: "BUILD.md", By: c.by}
			d.QID = PlanDraftQID(d.Path, []byte(d.File), []byte(d.Build), "", nil)
			d.Ask = c.ask(t, b, d.QID)
			if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := b.TypedApproval(ctx, d.Ask, d.QID, "yes"); err != nil {
				t.Fatalf("the fixture's yes is a real typed yes: %v", err)
			}
			if err := b.SetPlanDraft(ctx, d, PlanDraft{}); err != nil {
				t.Fatal(err)
			}
			cur, _, _ := b.PlanDraft(ctx)
			if _, err := b.PlanDraftApproved(ctx, cur, adoptQ(cur.QID)); err == nil {
				t.Error("show: approved")
			}
			// part 1 refuses with its reason, before the script's bare code
			if _, err := loadDraft(b, cur); err == nil || !strings.Contains(err.Error(), "awaits the person's yes") {
				t.Errorf("load: %v", err)
			}
			if _, ok, _ := b.Plan(ctx); ok {
				t.Error("the plan was written")
			}
		})
	}
}

// The adoption ask must be kept: after its record is swept, the yes cannot
// be bound to its question, and the draft is asked again.
func TestPlanDraftApprovalNeedsTheKeptAsk(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	d := draftFixture(t, b)
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PlanDraftApproved(ctx, d, adoptQ(d.QID)); err != nil {
		t.Fatalf("kept: %v", err)
	}
	b.r.HDel(ctx, AsksKey(b.Project()), d.Ask, d.Ask+"|q", d.Ask+"|s", d.Ask+"|a|"+d.QID)
	if _, err := b.PlanDraftApproved(ctx, d, adoptQ(d.QID)); err == nil || !strings.Contains(err.Error(), "no longer kept") {
		t.Fatalf("swept: %v", err)
	}
}
