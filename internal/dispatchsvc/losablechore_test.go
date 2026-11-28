package dispatchsvc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

const (
	losNew    = "1111111111111111111111111111111111111111"
	losPushed = "2222222222222222222222222222222222222222"
	losMerged = "3333333333333333333333333333333333333333"
	losAhead  = "4444444444444444444444444444444444444444"
	losRemote = "5555555555555555555555555555555555555555"
	losBase   = "6666666666666666666666666666666666666666"
	losFar    = "7777777777777777777777777777777777777777"
	losAbsent = "8888888888888888888888888888888888888888" // origin's tip of coder/far, never fetched here
)

// losChore is mqChore with the losable readers faked: coder holds coder/new
// (only here), coder/merged (in the base), coder/ahead (origin's tip is
// past it) and coder/far (origin's tip is not here: not judged on it, so
// unpushed); coder2 holds coder2/p, pushed as is; docs holds nothing; one
// stash names coder.
func losChore(t *testing.T, origin string) (*mergeQueueChore, *bus.Bus) {
	t.Helper()
	c, b := mqChore(t, origin)
	ctx := context.Background()
	for task, claim := range map[string][2]string{"t-new": {"coder", "coder/new"}, "t-merged": {"coder", "coder/merged"}, "t-ahead": {"coder", "coder/ahead"}, "t-far": {"coder", "coder/far"}, "t-p": {"coder2", "coder2/p"}} {
		if err := b.BoardClaim(ctx, task, claim[0], claim[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Status(ctx, "docs", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	c.localBranches = func(context.Context, string, []string) ([]mergequeue.Tip, error) {
		return []mergequeue.Tip{{Branch: "coder/new", Head: losNew}, {Branch: "coder/merged", Head: losMerged}, {Branch: "coder/ahead", Head: losAhead}, {Branch: "coder/far", Head: losFar}, {Branch: "coder2/p", Head: losPushed}}, nil
	}
	c.remoteTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		return []mergequeue.Tip{{Branch: "coder2/p", Head: losPushed}, {Branch: "coder/ahead", Head: losRemote}, {Branch: "coder/far", Head: losAbsent}}, losBase, nil
	}
	c.stashes = func(context.Context, string, []string) ([]string, error) { return []string{"On coder/new: wip"}, nil }
	c.hasCommit = func(_ context.Context, _ string, _ []string, h string) (bool, error) {
		return h == losBase || h == losRemote, nil
	}
	c.isAncestor = func(_ context.Context, _ string, _ []string, h, on string) (bool, error) {
		if on != losBase && on != losRemote { // git merge-base on a commit not here fails
			return false, errors.New("fatal: not a valid commit name " + on)
		}
		return h == losMerged && on == losBase || h == losAhead && on == losRemote, nil
	}
	return c, b
}

// The pass writes each role's losable work: an unpushed branch and a stash
// owe, a pushed, merged or overtaken branch does not, a role holding nothing
// has an empty entry (judged: it owes nothing here).
func TestLosablePass(t *testing.T) {
	c, b := losChore(t, "git@github.com:o/r.git")
	ctx := context.Background()
	if err := c.losablePass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, err := b.LosableRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l := rec["coder"]; !slices.Equal(l.Branches, []string{"coder/far", "coder/new"}) || !slices.Equal(l.Stashes, []string{"On coder/new: wip"}) {
		t.Fatalf("coder: %+v", l)
	}
	for _, role := range []string{"coder2", "docs"} {
		if l, ok := rec[role]; !ok || len(l.Branches)+len(l.Stashes) != 0 {
			t.Fatalf("%s: %+v %v", role, l, ok)
		}
	}
	if o := b.ReadOwes(ctx, map[string]bus.AgentSnapshot{"coder": {}})["coder"]; !slices.Equal(o.Unpushed, []string{"coder/far", "coder/new"}) || !o.Stash || len(o.Unknown) != 0 {
		t.Fatalf("coder owes: %+v", o)
	}
}

// The local forge pushes nothing: no branch is unpushed, the stash still owes.
func TestLosablePassLocalForge(t *testing.T) {
	c, b := losChore(t, "")
	ctx := context.Background()
	c.remoteTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
		t.Fatal("ls-remote in local mode")
		return nil, "", nil
	}
	if err := c.losablePass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if l := mustLosable(t, b)["coder"]; len(l.Branches) != 0 || len(l.Stashes) != 1 {
		t.Fatalf("coder: %+v", l)
	}
}

// A read that fails writes nothing: the last finding stays to age out, never
// replaced by "owes nothing".
func TestLosablePassFailedReadWritesNothing(t *testing.T) {
	ctx := context.Background()
	for name, breakIt := range map[string]func(*mergeQueueChore){
		"stash": func(c *mergeQueueChore) {
			c.stashes = func(context.Context, string, []string) ([]string, error) { return nil, errors.New("x") }
		},
		"locals": func(c *mergeQueueChore) {
			c.localBranches = func(context.Context, string, []string) ([]mergequeue.Tip, error) { return nil, errors.New("x") }
		},
		"remote": func(c *mergeQueueChore) {
			c.remoteTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
				return nil, "", errors.New("x")
			}
		},
		"ancestor": func(c *mergeQueueChore) {
			c.isAncestor = func(context.Context, string, []string, string, string) (bool, error) { return false, errors.New("x") }
		},
		"commit": func(c *mergeQueueChore) {
			c.hasCommit = func(context.Context, string, []string, string) (bool, error) { return false, errors.New("x") }
		},
		"repo": func(c *mergeQueueChore) {
			c.repo = func(context.Context, string, []string) error { return errors.New("x") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, b := losChore(t, "git@github.com:o/r.git")
			if err := b.FencedPutLosable(ctx, "mq", map[string]bus.Losable{"coder": {Branches: []string{"older"}}}); err != nil {
				t.Fatal(err)
			}
			breakIt(c)
			if err := c.losablePass(ctx, "mq"); err != nil {
				t.Fatal(err)
			}
			if l := mustLosable(t, b)["coder"]; !slices.Equal(l.Branches, []string{"older"}) {
				t.Fatalf("replaced after a failed read: %+v", l)
			}
		})
	}
}

// A lost lease is returned, and writes nothing.
func TestLosablePassLeaseLost(t *testing.T) {
	c, b := losChore(t, "git@github.com:o/r.git")
	if err := c.losablePass(context.Background(), "not-the-token"); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("%v", err)
	}
	if rec := mustLosable(t, b); len(rec) != 0 {
		t.Fatalf("%+v", rec)
	}
}

// The chore's pass runs it after the queue, and the service's chore has the
// real readers.
func TestMergeQueuePassWritesLosable(t *testing.T) {
	c, b := losChore(t, "git@github.com:o/r.git")
	if err := c.pass(context.Background(), "mq"); err != nil {
		t.Fatal(err)
	}
	if l := mustLosable(t, b)["coder"]; !slices.Equal(l.Branches, []string{"coder/far", "coder/new"}) {
		t.Fatalf("coder: %+v", l)
	}
	s := serviceMergeQueueChore(b, func(string) string { return "" })
	if s == nil || s.stashes == nil || s.hasCommit == nil || s.isAncestor == nil {
		t.Fatal("the service's chore lacks the losable readers")
	}
}

func mustLosable(t *testing.T, b *bus.Bus) map[string]bus.Losable {
	t.Helper()
	rec, err := b.LosableRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
