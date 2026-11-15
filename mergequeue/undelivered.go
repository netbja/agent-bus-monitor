package mergequeue

import (
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// UndeliveredInput is what UndeliveredCandidates joins (master-idle §3.1).
type UndeliveredInput struct {
	Verdicts []bus.Verdict
	Now      time.Time
	Window   time.Duration   // an approve older than this is out (72 h)
	Remote   []Tip           // origin's branch tips: a head there is pushed, master's part done
	Local    []Tip           // the local branches, to name a head's branch
	Dropped  map[string]bool // heads master withdrew (merge-queue drop)
	Done     map[string]bool // subjects of plan tasks recorded done
}

// UndeliveredCandidates are the approved deciding heads that may still be
// in master's hand, oldest approve first: within Window, no tip on origin,
// not dropped, no subject done. The git test (Delivery) then leaves out
// those the base already holds.
func UndeliveredCandidates(in UndeliveredInput) []bus.MergeUndelivered {
	onOrigin := map[string]bool{}
	for _, tp := range in.Remote {
		onOrigin[tp.Head] = true
	}
	branch := map[string]string{}
	for _, tp := range in.Local {
		if _, ok := branch[tp.Head]; !ok {
			branch[tp.Head] = tp.Branch
		}
	}
	var out []bus.MergeUndelivered
	for head, a := range candidates(in.Verdicts) {
		if onOrigin[head] || in.Dropped[head] || in.Now.Sub(time.UnixMilli(a.at)) > in.Window {
			continue
		}
		done := false
		for _, s := range a.subjects {
			done = done || in.Done[s]
		}
		if done {
			continue
		}
		out = append(out, bus.MergeUndelivered{Subject: a.subjects[0], Head: head, Branch: branch[head], ApprovedAt: a.at})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ApprovedAt != out[j].ApprovedAt {
			return out[i].ApprovedAt < out[j].ApprovedAt
		}
		return out[i].Head < out[j].Head
	})
	return out
}
