package dispatchsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// conflict-owner build B in the merge queue's pass (§4): a conflict git
// confirms goes back to its owner, written after the check and before the
// record; a PR closed or clean forgets its field; a pass that failed, or a
// live derive, touches nothing.

func handoffs(t *testing.T, b *bus.Bus) []string {
	t.Helper()
	es, _, err := b.Recent(context.Background(), []string{"cmd"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		if e.From == "dispatch" && strings.Contains(e.Message, "conflicts with") {
			out = append(out, e.Target+": "+e.Message)
		}
	}
	return out
}

func TestMergeQueueChoreConflictHandoff(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	c.scratch = t.TempDir()
	env := map[string]string{}
	c.getenv = func(k string) string { return env[k] }
	var calls []judgeCall
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"bus/x.go"}}}, "")

	// A live derive (merge-queue --live) writes nothing.
	if rec := c.derive(ctx); len(rec.Conflicting) != 1 {
		t.Fatalf("live: %+v", rec)
	}
	if r, _ := b.ConflictRecord(ctx); len(r) != 0 || len(handoffs(t, b)) != 0 {
		t.Fatalf("a live derive wrote %+v %v", r, handoffs(t, b))
	}

	// The pass: feat/ names no agent, so master is the owner (no copy).
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	hs := handoffs(t, b)
	if len(hs) != 1 || !strings.HasPrefix(hs[0], "master: PR #309 (feat/request-priority)") || !strings.Contains(hs[0], "bus/x.go") || !strings.Contains(hs[0], "Hand-off 1 of 3") {
		t.Fatalf("hand-offs %v", hs)
	}
	if r, _ := b.ConflictRecord(ctx); r["pr:309"].Count != 1 {
		t.Fatalf("record %+v", r)
	}
	if rec, _ := b.MergeQueue(ctx); rec.ConflictCheck != "" || rec.Reason != "" {
		t.Fatalf("a clean hand-off names nothing: %+v", rec)
	}

	// The owner's fix in review: a new head, not approved, so not a
	// candidate; the PR is still open: its field is kept, nothing sent.
	gh0 := c.github
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: strings.Repeat("e", 40)}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); r["pr:309"].Count != 1 || len(handoffs(t, b)) != 1 {
		t.Fatalf("a fix in review: %+v %v", r, handoffs(t, b))
	}
	c.github = gh0

	// A pass that fails (the forge unreadable): nothing forgotten.
	gh := c.github
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return nil, errors.New("gh: 502")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); len(r) != 1 {
		t.Fatalf("a failed pass forgot: %+v", r)
	}
	c.github = gh

	// The approved head git now finds clean: resolved, the field gone.
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {}}, "")
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); len(r) != 0 {
		t.Fatalf("a clean head kept its field: %+v", r)
	}

	// conflict.max_handoffs 0: straight to master's escalation.
	env["AGENT_BUS_CONFLICT_MAX_HANDOFFS"] = "0"
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"f"}}}, "")
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	es, _, _ := b.Recent(ctx, []string{"cmd"}, 100)
	last := es[len(es)-1]
	if last.Target != "master" || !strings.Contains(last.Message, "still conflicts with main after 0 hand-off(s)") {
		t.Fatalf("cap 0: %+v", last)
	}

	// The PR closed: its field goes.
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) { return nil, nil }
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); len(r) != 0 {
		t.Fatalf("a closed PR kept its field: %+v", r)
	}
}

// The git fallback: the branch's field; a branch gone forgets it.
func TestMergeQueueChoreConflictHandoffLocal(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "/srv/git/r.git")
	c.scratch = t.TempDir()
	var calls []judgeCall
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"feat/request-priority": {Conflict: true, Files: []string{"f"}}}, "")
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	field := mergequeue.ConflictField(0, "feat/request-priority")
	if r, _ := b.ConflictRecord(ctx); r[field].Count != 1 {
		t.Fatalf("record %+v", r)
	}
	if hs := handoffs(t, b); len(hs) != 1 || !strings.HasPrefix(hs[0], "master: Branch feat/request-priority at") {
		t.Fatalf("hand-offs %v", hs)
	}
	// A fix in review: the branch's new tip is not approved: kept.
	c.localTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return []mergequeue.Tip{{Branch: "feat/request-priority", Head: strings.Repeat("e", 40)}}, mqBaseTip, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); r[field].Count != 1 {
		t.Fatalf("a fix in review: %+v", r)
	}
	c.localTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return nil, mqBaseTip, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.ConflictRecord(ctx); len(r) != 0 {
		t.Fatalf("a branch gone kept its field: %+v", r)
	}
}

// The hand-off's failure is the record's, beside the check's; the queue's
// own outcome is untouched.
func TestMergeQueueChoreConflictHandoffFailure(t *testing.T) {
	ctx := context.Background()
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	c.scratch = t.TempDir()
	var calls []judgeCall
	c.conflicts = judgeAs(&calls, map[string]mergequeue.Judged{"#309": {Conflict: true, Files: []string{"f"}}}, "")
	r, err := bus.Connect("") // the test broker mqChore opened
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, bus.ConflictsKey(b.Project()), "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" || len(rec.Conflicting) != 1 || !strings.Contains(rec.ConflictCheck, "conflicts: the record is unreadable") {
		t.Fatalf("record %+v", rec)
	}
}
