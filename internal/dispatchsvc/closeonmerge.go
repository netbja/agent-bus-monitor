package dispatchsvc

import (
	"context"
	"sort"

	"github.com/netbja/agent-bus-monitor/mergequeue"
)

// Close on merge (board-drift §3.1-§3.3): the merge queue's pass judges the
// review requests' heads with the same Delivered test it runs on the
// approved heads, and closes the delivered ones through the fenced
// FencedRequestMerged. Judged only: a request in review, on a branch, with
// a review_base, whose head (the branch's tip this pass read, else the
// commit review recorded) is not already in that review_base.

// mergeClose is one request whose branch head is delivered.
type mergeClose struct{ task, branch, head string }

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
	type cand struct{ task, branch, head, reviewBase string }
	var cands []cand
	for task, e := range board {
		r := e.Request
		if e.State != "review" || r == nil || r.Branch == "" || r.ReviewBase == "" {
			continue // not judged (§3.1); §5 reports one with no base
		}
		head := tipOf[r.Branch]
		if head == "" {
			head = r.Commit // its branch deleted after the merge (§3.2)
		}
		if head == "" {
			continue
		}
		cands = append(cands, cand{task, r.Branch, head, r.ReviewBase})
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
			out.closes = append(out.closes, mergeClose{cd.task, cd.branch, cd.head})
		}
	}
	return out
}
