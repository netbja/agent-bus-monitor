package dispatchsvc

import (
	"context"
	"errors"
	"sort"

	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// Close on merge (board-drift §3.1-§3.3): the merge queue's pass judges the
// review requests' heads with the same Delivered test it runs on the
// approved heads, and closes the delivered ones through the fenced
// FencedRequestMerged. Judged only: a request in review, on a branch, with
// a review_base, whose head (the commit review recorded, when the branch's
// tip is that commit or gone; the tip when no commit was recorded) is not
// already in that review_base.

// mergeClose is one request whose branch head is delivered, and the review
// read (the write applies to that review only).
type mergeClose struct {
	task, branch, head string
	reviewN            int
}

// errNoLocalReader: no reader of the local refs, so they are unread.
var errNoLocalReader = errors.New("no reader of the local refs")

// mergeClosePass is what the pass closes, and into which base.
type mergeClosePass struct {
	base   string
	closes []mergeClose
}

// closeOnMerge finds the review requests whose head the base tip baseTip
// holds; a read that fails closes nothing (the next pass judges again).
func (c *mergeQueueChore) closeOnMerge(ctx context.Context, env []string, tips []mergequeue.Tip, baseTip, base string) mergeClosePass {
	out := mergeClosePass{base: base}
	if baseTip == "" {
		return out
	}
	board, err := c.b.Board(ctx)
	if err != nil {
		return out
	}
	tipOf := map[string]string{}
	for _, t := range tips {
		tipOf[t.Branch] = t.Head
	}
	var localOf map[string]string // the local refs, read once and only when a branch is not in tips (§3.2)
	localRead := false
	local := func(branch string) (string, bool) {
		if !localRead {
			localRead = true
			var ts []mergequeue.Tip
			err := errNoLocalReader
			if c.localBranches != nil {
				cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
				ts, err = c.localBranches(cctx, c.dir, env)
				cancel()
			}
			if err == nil {
				localOf = map[string]string{}
				for _, t := range ts {
					localOf[t.Branch] = t.Head
				}
			}
		}
		if localOf == nil {
			return "", false // unread: not judged this pass
		}
		return localOf[branch], true
	}
	type cand struct {
		task, branch, head, reviewBase string
		reviewN                        int
	}
	var cands []cand
	for task, e := range board {
		r := e.Request
		if e.State != "review" || r == nil || r.Branch == "" || r.ReviewBase == "" {
			continue // not judged (§3.1); §5 reports one with no base
		}
		tip := tipOf[r.Branch] // the remote tip (the local one in local mode), else the local ref
		if tip == "" {
			h, ok := local(r.Branch)
			if !ok {
				continue
			}
			tip = h
		}
		// The head judged is the one reviewed (foureyes): the recorded commit.
		// A tip that differs from it is not judged this pass: origin's old
		// tip after a squash merge (the branch reused for the next step), or
		// work pushed after the review with no new review; §5 reports it.
		// With no tip (the branch deleted after its merge), the commit; with
		// no commit recorded, the tip.
		head := r.Commit
		switch {
		case head == "":
			head = tip
		case tip != "" && tip != head:
			continue
		}
		if head == "" {
			continue
		}
		cands = append(cands, cand{task, r.Branch, head, r.ReviewBase, r.ReviewN})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].task < cands[j].task })
	var judged []cand
	for _, cd := range cands { // a head already in its review_base is never judged (§3.2)
		cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
		in, err := c.inBase(cctx, c.dir, env, cd.reviewBase, []string{cd.head})
		cancel()
		if err != nil || in[cd.head] {
			continue
		}
		judged = append(judged, cd)
	}
	if len(judged) == 0 {
		return out
	}
	heads := make([]string, len(judged))
	for i, cd := range judged {
		heads[i] = cd.head
	}
	cctx, cancel := context.WithTimeout(ctx, mergeQueueCall)
	delivered, _ := c.delivered(cctx, c.dir, env, baseTip, heads) // a failed read: nil, nothing delivered, nothing closed
	cancel()
	for _, cd := range judged {
		if delivered[cd.head] {
			out.closes = append(out.closes, mergeClose{cd.task, cd.branch, cd.head, cd.reviewN})
		}
	}
	return out
}
