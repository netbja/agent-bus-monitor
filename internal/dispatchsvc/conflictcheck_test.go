package dispatchsvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// conflict-owner §2 in the merge queue's pass (build PR A): the approved
// heads, and the PRs the forge flags, are judged by git; the record holds
// what git confirmed. Nothing is sent yet.

const mqBaseTip = "1111111111111111111111111111111111111111"

// judgeAs fakes the check: each key's outcome, the calls recorded.
type judgeCall struct {
	check   mergequeue.ConflictCheck
	baseTip string
	cands   []mergequeue.Candidate
}

func judgeAs(calls *[]judgeCall, out map[string]mergequeue.Judged, why string) func(context.Context, mergequeue.ConflictCheck, string, []mergequeue.Candidate) (map[string]mergequeue.Judged, string) {
	return func(_ context.Context, c mergequeue.ConflictCheck, tip string, cs []mergequeue.Candidate) (map[string]mergequeue.Judged, string) {
		*calls = append(*calls, judgeCall{c, tip, cs})
		return out, why
	}
}

func TestMergeQueueChoreConflictCheck(t *testing.T) {
	ctx := context.Background()
	scratch := t.TempDir()
	pass := func(c *mergeQueueChore, b *bus.Bus) bus.MergeQueueRecord {
		t.Helper()
		if err := c.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		rec, _ := b.MergeQueue(ctx)
		return rec
	}

	// gh: the approved item conflicts by git: out of the items, recorded
	// with branch, head, base tip and files; the check got the candidate,
	// origin's base tip, and fetch on.
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	c.scratch = scratch
	var calls []judgeCall
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"bus/x.go"}}}, "")
	rec := pass(c, b)
	want := bus.MergeConflict{Number: 309, Base: "main", Branch: "feat/request-priority", Head: mqHead, BaseTip: mqBaseTip, Files: []string{"bus/x.go"}}
	if len(rec.Items) != 0 || len(rec.Conflicting) != 1 || !reflect.DeepEqual(rec.Conflicting[0], want) || rec.ConflictCheck != "" {
		t.Fatalf("an item git finds conflicting: %+v", rec)
	}
	if len(calls) != 1 || calls[0].baseTip != mqBaseTip || !calls[0].check.Fetch || calls[0].check.Scratch != scratch || calls[0].check.Dir != c.dir ||
		!reflect.DeepEqual(calls[0].cands, []mergequeue.Candidate{{Key: "#309", Branch: "feat/request-priority", Head: mqHead}}) {
		t.Fatalf("the check's call: %+v", calls)
	}

	// The forge flags it, git finds it clean: an item (forges' flags lag).
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: mqHead, Base: "main", Conflicting: true}}, nil
	}
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {}}, "")
	if rec = pass(c, b); len(rec.Items) != 1 || rec.Items[0].Number != 309 || len(rec.Conflicting) != 0 {
		t.Fatalf("flagged, clean by git: %+v", rec)
	}
	// Flagged and confirmed: the files added.
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"f"}}}, "")
	if rec = pass(c, b); len(rec.Conflicting) != 1 || rec.Conflicting[0].Base != "main" || !reflect.DeepEqual(rec.Conflicting[0].Files, []string{"f"}) {
		t.Fatalf("flagged, confirmed: %+v", rec)
	}
	// Flagged, not judged (absent): the forge's word, the reason recorded.
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Why: "the head is absent here, even after a fetch"}}, "")
	rec = pass(c, b)
	if len(rec.Items) != 0 || len(rec.Conflicting) != 1 || rec.Conflicting[0].Files != nil || !strings.Contains(rec.ConflictCheck, "#309: the head is absent") {
		t.Fatalf("flagged, not judged: %+v", rec)
	}
	// The whole check could not run: the forge's view, the reason recorded.
	c.conflicts = judgeAs(&calls, nil, "git too old for the conflict check")
	if rec = pass(c, b); len(rec.Conflicting) != 1 || rec.ConflictCheck != "git too old for the conflict check" || rec.Reason != "" {
		t.Fatalf("no check: %+v", rec)
	}

	// An approved item git could not judge stays an item, never "clean" or
	// "conflicting" without git's word.
	c, b = mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	c.scratch = scratch
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Why: "not checked this pass (time): next pass"}}, "")
	if rec = pass(c, b); len(rec.Items) != 1 || len(rec.Conflicting) != 0 || !strings.Contains(rec.ConflictCheck, "next pass") || rec.Reason != "" {
		t.Fatalf("an item not reached: %+v", rec)
	}

	// Local mode: keyed by branch, nothing fetched.
	c, b = mqChore(t, "/srv/git/r.git")
	c.scratch = scratch
	calls = nil
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"feat/request-priority": {Conflict: true, Files: []string{"f"}}}, "")
	rec = pass(c, b)
	if len(calls) != 1 || calls[0].check.Fetch || calls[0].cands[0].Key != "feat/request-priority" || len(rec.Conflicting) != 1 || rec.Conflicting[0].Number != 0 || rec.Conflicting[0].Branch != "feat/request-priority" {
		t.Fatalf("local: %+v %+v", calls, rec)
	}
}

// reviewer-codex: a PR onto another base than the queue's is never judged
// against the queue's base tip: it stays as the forge said (an approved one
// an item, a flagged one conflicting with its own base), the reason named.
func TestMergeQueueChoreConflictOtherBase(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	c.scratch = t.TempDir()
	var calls []judgeCall
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"f"}}}, "")
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: mqHead, Base: "release"}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, _ := b.MergeQueue(ctx)
	if len(calls) != 0 || len(rec.Items) != 1 || len(rec.Conflicting) != 0 || !strings.Contains(rec.ConflictCheck, "#309 onto release: not checked against main") {
		t.Fatalf("an approved PR onto release: calls %+v, %+v", calls, rec)
	}
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Branch: "feat/request-priority", Head: mqHead, Base: "release", Conflicting: true}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, _ = b.MergeQueue(ctx)
	if len(calls) != 0 || len(rec.Items) != 0 || len(rec.Conflicting) != 1 || rec.Conflicting[0].Base != "release" || rec.Conflicting[0].BaseTip != "" ||
		!strings.Contains(rec.ConflictCheck, "onto release") {
		t.Fatalf("a flagged PR onto release: calls %+v, %+v", calls, rec)
	}
	// Onto the queue's base, named so: judged as before.
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Branch: "feat/request-priority", Head: mqHead, Base: "main"}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); len(calls) != 1 || len(rec.Conflicting) != 1 || rec.Conflicting[0].BaseTip != mqBaseTip {
		t.Fatalf("onto main: %+v %+v", calls, rec)
	}
}

// The service's chore runs the real check in the project's state directory.
func TestServiceMergeQueueChoreChecksConflicts(t *testing.T) {
	b, _ := bustest.Open(t)
	state := t.TempDir()
	c := serviceMergeQueueChore(b, func(k string) string { return map[string]string{"XDG_STATE_HOME": state, "HOME": t.TempDir()}[k] })
	if c == nil || c.conflicts == nil || c.scratch != filepath.Join(state, "agentbus", b.Project()) {
		t.Fatalf("chore %+v", c)
	}
}

// End to end in local mode, with real git: an approved branch that conflicts
// with main is recorded with its file, a clean one stays an item.
func TestMergeQueueChoreConflictRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "LC_ALL=C"}
	g := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644) }
	g("init", "-q", "-b", "main")
	write("f", "1\n")
	g("add", "f")
	g("commit", "-q", "-m", "one")
	g("switch", "-q", "-c", "feat/clash")
	write("f", "clash\n")
	g("commit", "-q", "-am", "clash")
	clash := g("rev-parse", "HEAD")
	g("switch", "-q", "-c", "feat/clean", "main")
	write("c", "c\n")
	g("add", "c")
	g("commit", "-q", "-m", "clean")
	clean := g("rev-parse", "HEAD")
	g("switch", "-q", "main")
	write("f", "main moved\n")
	g("commit", "-q", "-am", "main moves")

	c, b := mqChore(t, "")
	ctx := context.Background()
	for subject, head := range map[string]string{"clash": clash, "clean": clean} {
		if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: subject, Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: head, Message: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	c.dir, c.scratch = dir, t.TempDir()
	c.environ = func() []string { return env }
	c.localTips, c.inBase, c.conflicts = mergequeue.LocalTips, mergequeue.InBase, mergequeue.CheckConflicts
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, _ := b.MergeQueue(ctx)
	if rec.Reason != "" || rec.ConflictCheck != "" || len(rec.Conflicting) != 1 || rec.Conflicting[0].Branch != "feat/clash" || !reflect.DeepEqual(rec.Conflicting[0].Files, []string{"f"}) ||
		len(rec.Items) != 1 || rec.Items[0].Branch != "feat/clean" {
		t.Fatalf("%+v", rec)
	}
	if es, _ := os.ReadDir(c.scratch); len(es) != 0 {
		t.Fatalf("scratch left: %v", es)
	}
}
