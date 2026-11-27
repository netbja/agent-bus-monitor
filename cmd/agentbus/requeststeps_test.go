package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// sibling-enforce §3.1 in the CLI: the open steps judged in the agent's
// checkout with the dispatcher's own test, one head per step.

// judgerIn is a stepJudger over repo with base dev.
func judgerIn(r reviewRepo, forgeSetting string) stepJudger {
	d := mergequeue.NewDelivery()
	return stepJudger{git: reviewGitIn(r.dir), forge: forgeSetting, base: "dev",
		delivered: func(ctx context.Context, baseTip string, heads []string) (map[string]bool, error) {
			return d.Delivered(ctx, r.dir, os.Environ(), baseTip, heads)
		}}
}

func mergedOf(s bus.StepsJudged) []string {
	var out []string
	for _, m := range s.Merged {
		out = append(out, m.Branch)
	}
	return out
}

// Local mode: a step not in the base is not judged merged; squash-merged,
// it is (the local tip judged); the step of the review itself, and every
// step under parallel, are not judged; done judges every one.
func TestJudgeStepsLocal(t *testing.T) {
	r, _, tip := newReviewRepo(t) // coder/t1 one commit beyond dev
	j := judgerIn(r, "local")
	ctx := context.Background()
	req := &bus.RequestInfo{ReviewN: 3, OpenSteps: []bus.OpenStep{{Branch: "coder/t1", Commit: tip}}}
	s, err := j.judge(ctx, req, "coder/t2", false)
	if err != nil || len(s.Merged) != 0 || s.N != 3 {
		t.Fatalf("not merged: %+v %v", s, err)
	}
	if s, _ := j.judge(ctx, req, "coder/t1", false); len(s.Merged) != 0 {
		t.Fatalf("a review of the step itself judged it: %+v", s)
	}
	r.git("checkout", "-q", "dev")
	r.git("merge", "-q", "--squash", "coder/t1")
	r.git("commit", "-q", "-m", "squash t1")
	s, err = j.judge(ctx, req, "coder/t2", false)
	if err != nil || !slices.Equal(mergedOf(s), []string{"coder/t1"}) || s.Merged[0].Head != tip || s.Merged[0].Base != r.git("rev-parse", "dev") {
		t.Fatalf("squash-merged: %+v %v", s, err)
	}
	if s, _ := j.judge(ctx, &bus.RequestInfo{OpenSteps: req.OpenSteps, Parallel: &bus.ParallelMark{By: "master"}}, "coder/t2", false); len(s.Merged) != 0 {
		t.Fatalf("parallel judged: %+v", s)
	}
	if s, _ := j.judge(ctx, req, "", true); !slices.Equal(mergedOf(s), []string{"coder/t1"}) {
		t.Fatalf("done: %+v", s)
	}
	// No base to judge against: an error, nothing judged.
	j.base = ""
	if _, err := j.judge(ctx, req, "coder/t2", false); err == nil || !strings.Contains(err.Error(), "no delivery base") {
		t.Fatalf("no base: %v", err)
	}
}

// With a remote, one head per step, the newest that exists: the remote tip
// (a fix pushed after the review is judged as it stands: the recorded commit
// in the base does not clear it), else the local tip once the remote is
// deleted (a local fix ahead of the recorded commit), else the commit.
func TestJudgeStepsRemote(t *testing.T) {
	r, _, h1 := newReviewRepo(t)
	origin := reviewRepo{t: t, dir: t.TempDir()}
	origin.git("init", "-q", "--bare")
	r.git("remote", "add", "origin", origin.dir)
	r.git("push", "-q", "origin", "dev", "coder/t1")
	req := &bus.RequestInfo{OpenSteps: []bus.OpenStep{{Branch: "coder/t1", Commit: h1}}}
	j := judgerIn(r, "none")
	ctx := context.Background()
	// A fix pushed after the review (h2); dev takes h1 alone.
	h2 := r.commit("c")
	r.git("push", "-q", "origin", "coder/t1")
	r.git("push", "-q", "origin", h1+":refs/heads/dev")
	if s, err := j.judge(ctx, req, "coder/t2", false); err != nil || len(s.Merged) != 0 {
		t.Fatalf("the recorded commit in the base cleared a newer tip: %+v %v", s, err)
	}
	// The remote branch deleted, the local tip (h2) still not in the base.
	r.git("push", "-q", "origin", ":coder/t1")
	if s, err := j.judge(ctx, req, "coder/t2", false); err != nil || len(s.Merged) != 0 {
		t.Fatalf("the local tip ahead of the base: %+v %v", s, err)
	}
	// h2 merged into origin's dev: judged merged on the local tip.
	r.git("push", "-q", "origin", h2+":refs/heads/dev")
	s, err := j.judge(ctx, req, "coder/t2", false)
	if err != nil || !slices.Equal(mergedOf(s), []string{"coder/t1"}) || s.Merged[0].Head != h2 {
		t.Fatalf("h2 merged: %+v %v", s, err)
	}
	// reviewer-codex: a stale local ref behind the reviewed commit never
	// clears it. Reviewed at h3, local back at h2 (in the base), remote
	// gone: h3 is judged, not merged.
	h3 := r.commit("e")
	r.git("reset", "-q", "--hard", h2)
	stale := &bus.RequestInfo{OpenSteps: []bus.OpenStep{{Branch: "coder/t1", Commit: h3}}}
	if s, err := j.judge(ctx, stale, "coder/t2", false); err != nil || len(s.Merged) != 0 {
		t.Fatalf("a stale local tip cleared the reviewed commit: %+v %v", s, err)
	}
	// Diverged from the reviewed commit: refused, nothing judged.
	r.git("checkout", "-q", "-b", "side", h1)
	other := r.commit("f")
	r.git("checkout", "-q", "coder/t1")
	r.git("reset", "-q", "--hard", other)
	if _, err := j.judge(ctx, stale, "coder/t2", false); err == nil || !strings.Contains(err.Error(), "diverge") {
		t.Fatalf("a diverged local tip: %v", err)
	}
	r.git("reset", "-q", "--hard", h2)
	// No local branch either: the recorded commit is judged.
	r.git("checkout", "-q", "dev")
	r.git("branch", "-q", "-D", "coder/t1")
	if s, _ := j.judge(ctx, req, "coder/t2", false); !slices.Equal(mergedOf(s), []string{"coder/t1"}) || s.Merged[0].Head != h1 {
		t.Fatalf("the recorded commit: %+v", s)
	}
	// A fetch that fails is an error.
	r.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere"))
	if _, err := j.judge(ctx, req, "coder/t2", false); err == nil {
		t.Fatal("a failed fetch judged")
	}
}

// The commands end to end, in the agent's checkout (local mode, base dev):
// step 2 is refused while step 1 is not merged; once it is squash-merged,
// step 2 passes and one step-merged audit line names it; done waits on step
// 2 the same way; report --done refuses before it publishes.
func TestRequestReviewEnforcesSiblingSteps(t *testing.T) {
	b, _ := bustest.Open(t)
	ctx := context.Background()
	r, _, _ := newReviewRepo(t)
	t.Chdir(r.dir)
	for name, v := range map[string]string{"delivery.forge": "local", "delivery.base": "dev"} {
		k, _ := config.Lookup(name)
		t.Setenv(k.Env, v)
	}
	r.git("checkout", "-q", "-b", "coder/t2", "dev")
	r.commit("d")
	if _, err := b.Request(ctx, "t1", "master", "coder", "", "build", time.Hour); err != nil {
		t.Fatal(err)
	}
	accept := func() {
		if err := runRequest(ctx, b, "coder", []string{"accept", "t1"}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	accept()
	if err := runRequest(ctx, b, "coder", []string{"review", "t1", "--branch", "coder/t1", "--more", "step", "1"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	accept()
	err := runRequest(ctx, b, "coder", []string{"review", "t1", "--branch", "coder/t2", "--more", "step", "2"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "step coder/t1 of t1 is not merged yet") || !strings.Contains(err.Error(), "ask master for agentbus request parallel t1") {
		t.Fatalf("step 2 before step 1 merged: %v", err)
	}
	r.git("checkout", "-q", "dev")
	r.git("merge", "-q", "--squash", "coder/t1")
	r.git("commit", "-q", "-m", "squash t1")
	if err := runRequest(ctx, b, "coder", []string{"review", "t1", "--branch", "coder/t2", "--more", "step", "2"}, io.Discard); err != nil {
		t.Fatalf("step 2 after step 1 merged: %v", err)
	}
	m, _ := b.Board(ctx)
	if q := m["t1"].Request; len(q.OpenSteps) != 1 || q.OpenSteps[0].Branch != "coder/t2" || len(q.MergedSteps) != 1 {
		t.Fatalf("after step 1: %+v", q)
	}
	lines, err := b.AuditLast(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(lines, func(e bus.AuditEntry) bool {
		return e.Action == "step-merged" && e.Actor == "coder" && e.Target == "coder/t1" && strings.HasPrefix(e.Detail, "request t1: head ")
	}) {
		t.Fatalf("no step-merged line: %+v", lines)
	}
	accept()
	// done waits on step 2; report --done refuses before publishing.
	if err := runRequest(ctx, b, "coder", []string{"done", "t1", "finished"}, io.Discard); err == nil || !strings.Contains(err.Error(), "step coder/t2 of t1 is not merged yet: done once it merges") {
		t.Fatalf("done with step 2 open: %v", err)
	}
	before, _, _ := b.Recent(ctx, []string{"report"}, 50)
	if _, err := sendReport(ctx, b, []string{"coder", "--task", "t1", "--done", "finished"}, io.Discard); err == nil || !strings.Contains(err.Error(), "step coder/t2 of t1 is not merged yet") {
		t.Fatalf("report --done with step 2 open: %v", err)
	}
	if after, _, _ := b.Recent(ctx, []string{"report"}, 50); len(after) != len(before) {
		t.Fatal("report --done published before it refused")
	}
	r.git("merge", "-q", "--squash", "coder/t2")
	r.git("commit", "-q", "-m", "squash t2")
	if err := runRequest(ctx, b, "coder", []string{"done", "t1", "finished"}, io.Discard); err != nil {
		t.Fatalf("done after step 2 merged: %v", err)
	}
}

// parallel and step --abandon: the sender's acts, the target's never; an
// agent cannot claim --as.
func TestRequestParallelAndAbandonCommands(t *testing.T) {
	b, _ := bustest.Open(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "t1", "master", "coder", "", "build", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "t1", "coder"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReviewRequestOpts(ctx, "t1", "coder", bus.ReviewOpts{Branch: "coder/b", Commit: strings.Repeat("3", 40), ReviewBase: strings.Repeat("2", 40), More: true}); err != nil {
		t.Fatal(err)
	}
	if err := runRequest(ctx, b, "coder", []string{"parallel", "t1", "mine"}, io.Discard); err == nil || !strings.Contains(err.Error(), "only the request's sender or the person may") {
		t.Fatalf("the target: %v", err)
	}
	var out strings.Builder
	if err := runRequest(ctx, b, "master", []string{"parallel", "t1", "b", "and", "c", "share", "no", "file"}, &out); err != nil || !strings.Contains(out.String(), "t1: parallel (b and c share no file)") {
		t.Fatalf("the sender: %q %v", out.String(), err)
	}
	t.Setenv("AGENT_BUS_AGENT", "coder")
	if err := runRequest(ctx, b, "coder", []string{"parallel", "t1", "--off", "--as", "charlie"}, io.Discard); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("an agent as the person: %v", err)
	}
	if err := runRequest(ctx, b, "coder", []string{"step", "t1", "coder/b", "--abandon", "closed"}, io.Discard); err == nil || !strings.Contains(err.Error(), "only the request's sender or the person may") {
		t.Fatalf("the target abandons: %v", err)
	}
	out.Reset()
	if err := runRequest(ctx, b, "master", []string{"step", "t1", "coder/b", "--abandon", "PR", "closed"}, &out); err != nil || !strings.Contains(out.String(), "t1: step coder/b abandoned at 33333333") {
		t.Fatalf("the sender abandons: %q %v", out.String(), err)
	}
	if err := runRequest(ctx, b, "master", []string{"step", "t1", "coder/b"}, io.Discard); err == nil || !strings.Contains(err.Error(), "usage: request step") {
		t.Fatalf("no --abandon: %v", err)
	}
	lines, _ := b.AuditLast(ctx, 10)
	for _, want := range []string{"parallel", "step-abandoned"} {
		if !slices.ContainsFunc(lines, func(e bus.AuditEntry) bool { return e.Action == want && e.Actor == "master" }) {
			t.Errorf("no %s audit line: %+v", want, lines)
		}
	}
}

// The remote tip is judged while the branch exists, whatever the local one
// says (foureyes' nit): the remote tip merged, a diverged local tip not, the
// step is merged on the remote tip.
func TestJudgeStepsRemoteTipFirst(t *testing.T) {
	r, _, h1 := newReviewRepo(t)
	origin := reviewRepo{t: t, dir: t.TempDir()}
	origin.git("init", "-q", "--bare")
	r.git("remote", "add", "origin", origin.dir)
	r.git("push", "-q", "origin", "dev", "coder/t1")
	hR := r.commit("remote-fix")
	r.git("push", "-q", "origin", "coder/t1", hR+":refs/heads/dev")
	r.git("reset", "-q", "--hard", h1)
	r.commit("local-only") // local coder/t1 diverges from the remote's
	req := &bus.RequestInfo{OpenSteps: []bus.OpenStep{{Branch: "coder/t1", Commit: h1}}}
	s, err := judgerIn(r, "none").judge(context.Background(), req, "coder/t2", false)
	if err != nil || !slices.Equal(mergedOf(s), []string{"coder/t1"}) || s.Merged[0].Head != hR {
		t.Fatalf("the remote tip: %+v %v", s, err)
	}
}
