package dispatchsvc

import (
	"context"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// board-drift §5.1: what close on merge (§3) must not close, reported, never
// written. An accepted or review request whose branch tip or recorded commit
// the base holds (an accepted multi-step, a review whose --more was wrong, a
// review with no review_base); a review request with no tip and no commit
// cannot be judged. The git test is the merge queue's own: its readers, its
// Delivered, in the current directory.

// DriftMerged is one finding: a request whose Head is delivered into Base,
// or, Unjudged, a review request with no branch tip and no commit.
type DriftMerged struct {
	Task, Owner, State, Branch, Head, Base string
	Unjudged                               bool
}

// BoardDriftMerged is §5.1 for the project of b, in the current directory;
// why says what could not be read ("" when the board was judged).
func BoardDriftMerged(ctx context.Context, b *bus.Bus, getenv func(string) string) ([]DriftMerged, string) {
	c := serviceMergeQueueChore(b, getenv)
	if c == nil {
		return nil, "no working directory"
	}
	return c.driftMerged(ctx)
}

// driftMerged reads the base tip and the branches' tips as the pass does
// (origin's, the base fetched; the local ones for a local forge; a branch not
// among them from the local refs), then judges each open request's heads.
// A head with no commit of its own is never a finding: a head in its
// review_base, or, with no review_base, an accepted request's head that is an
// ancestor of the base (a branch just made cannot be told from a
// fast-forward merge). A head that cannot be read is not judged.
func (c *mergeQueueChore) driftMerged(ctx context.Context) ([]DriftMerged, string) {
	settings := config.Machine(c.getenv)
	base, _, err := forge.ProjectDeliveryBase(c.getenv, c.b.Project())
	if err != nil {
		return nil, "the project's base: " + err.Error()
	}
	env := mergeQueueEnv(c.environ())
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, mergeQueueCall) }
	cctx, cancel := call()
	err = c.repo(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, "no git repository here"
	}
	cctx, cancel = call()
	url, has, err := c.origin(cctx, c.dir, env)
	cancel()
	if err != nil {
		return nil, "origin unreadable"
	}
	f := forge.Resolve(settings.String("delivery.forge"), url, has)
	var tips []mergequeue.Tip
	baseTip := ""
	if f.Kind == forge.Local {
		cctx, cancel = call()
		tips, baseTip, err = c.localTips(cctx, c.dir, env, base)
		cancel()
	} else {
		if !has {
			return nil, "no origin remote"
		}
		cctx, cancel = call()
		tips, baseTip, err = c.remoteTips(cctx, c.dir, env, base)
		cancel()
		if err == nil {
			cctx, cancel = call()
			err = c.fetchBase(cctx, c.dir, env, base)
			cancel()
		}
	}
	if err != nil {
		return nil, "branches unreadable: " + err.Error()
	}
	board, err := c.b.Board(ctx)
	if err != nil {
		return nil, "board unreadable: " + err.Error()
	}
	tipOf := map[string]string{}
	for _, t := range tips {
		tipOf[t.Branch] = t.Head
	}
	var localOf map[string]string
	if c.localBranches != nil {
		cctx, cancel = call()
		ts, lerr := c.localBranches(cctx, c.dir, env)
		cancel()
		if lerr == nil {
			localOf = map[string]string{}
			for _, t := range ts {
				localOf[t.Branch] = t.Head
			}
		}
	}
	tasks := make([]string, 0, len(board))
	for task := range board {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	var out []DriftMerged
	type cand struct {
		i     int // in out
		heads []string
	}
	var cands []cand
	var all []string
	for _, task := range tasks {
		e := board[task]
		r := e.Request
		if r == nil || e.State != "accepted" && e.State != "review" {
			continue
		}
		tip := ""
		if r.Branch != "" {
			if tip = tipOf[r.Branch]; tip == "" && localOf != nil {
				tip = localOf[r.Branch]
			}
		}
		var heads []string
		for _, h := range []string{r.Commit, tip} {
			if h != "" && bus.ValidHead(h) && (len(heads) == 0 || heads[0] != h) {
				heads = append(heads, h)
			}
		}
		if len(heads) == 0 {
			if e.State == "review" {
				out = append(out, DriftMerged{Task: task, Owner: e.Owner, State: e.State, Branch: r.Branch, Unjudged: true})
			}
			continue
		}
		var judged []string
		for _, h := range heads {
			cctx, cancel = call()
			var own bool // h has a commit of its own: not in its review_base; with none, an accepted head not an ancestor of the base
			var jerr error
			switch {
			case r.ReviewBase != "":
				var in map[string]bool
				in, jerr = c.inBase(cctx, c.dir, env, r.ReviewBase, []string{h})
				own = !in[h]
			case e.State == "accepted":
				var anc bool
				anc, jerr = mergequeue.IsAncestor(cctx, c.dir, env, h, baseTip)
				own = !anc
			default:
				own = true
			}
			cancel()
			if jerr == nil && own {
				judged = append(judged, h)
			}
		}
		if len(judged) == 0 {
			continue
		}
		out = append(out, DriftMerged{Task: task, Owner: e.Owner, State: e.State, Branch: r.Branch, Base: base})
		cands = append(cands, cand{len(out) - 1, judged})
		all = append(all, judged...)
	}
	delivered := map[string]bool{}
	if len(all) > 0 {
		cctx, cancel = call()
		delivered, err = c.delivered(cctx, c.dir, env, baseTip, all)
		cancel()
		if err != nil {
			return nil, "the git test failed: " + err.Error()
		}
	}
	drop := map[int]bool{}
	for _, cd := range cands {
		drop[cd.i] = true
		for _, h := range cd.heads {
			if delivered[h] {
				out[cd.i].Head, drop[cd.i] = h, false
				break
			}
		}
	}
	kept := out[:0]
	for i, d := range out {
		if !drop[i] {
			kept = append(kept, d)
		}
	}
	return kept, ""
}
