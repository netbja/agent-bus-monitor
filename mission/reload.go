package mission

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// reloadCounts are disjoint: changed is in both files with another hash
// (forced integrated ones included), added is new, removed is absent from the
// new file and touched by this reload, unchanged is the rest.
type reloadCounts struct{ Changed, Added, Removed, Unchanged int }

// planReload classifies every mission for revision rev (spec §6.4) and
// returns only the records to write. It refuses — before anything is written
// — every change the rules forbid, listing all of them.
func planReload(old map[string]MissionRecord, f File, rev int, force bool, reason string) (map[string]MissionRecord, reloadCounts, error) {
	out := map[string]MissionRecord{}
	var counts reloadCounts
	var refused []string
	ids := map[string]bool{}
	for id := range old {
		ids[id] = true
	}
	for _, m := range f.Missions {
		ids[m.ID] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)

	for _, id := range sorted {
		r, known := old[id]
		m, inFile := f.Mission(id)
		switch {
		case !known:
			out[id] = MissionRecord{ID: id, Hash: m.Hash(), State: Pending, Reviewer: f.ReviewerOf(m)}
			counts.Added++
			continue
		case r.State == Aborted && inFile:
			// Brought back after a removal: active again. Its attempt count
			// and history stay, so it never reuses an old request name.
			r.State, r.Hash, r.Reviewer, r.Rejections = Pending, m.Hash(), f.ReviewerOf(m), 0
			r.Reason = fmt.Sprintf("reintroduced in rev %d", rev)
			out[id] = r
			counts.Added++
			continue
		case r.State == Aborted || (inFile && r.Hash == m.Hash()):
			counts.Unchanged++
			continue
		}
		label := "changed"
		if !inFile {
			label = "removed"
		}
		switch r.State {
		case Approved, Integrating:
			refused = append(refused, fmt.Sprintf("%s is %s (wait until integrated or blocked, or abort the change)", id, r.State))
			continue
		case Requested, Accepted, Delivered, Checking, InReview, Blocked, Integrated:
			if !force {
				refused = append(refused, fmt.Sprintf("%s is %s and %s (pass --force <reason>)", id, r.State, label))
				continue
			}
		}
		if r.State == Integrated { // its merge is history: it stays integrated
			r.IntegratedUnder = fmt.Sprintf("rev %d %s", rev-1, r.Hash)
			if inFile {
				r.Hash = m.Hash()
			}
			r.Reason = "reload --force: " + reason
		} else {
			if r.Current.Task != "" { // the attempt in flight is superseded
				r.History = append(r.History, Attempt{N: r.Current.N, Task: r.Current.Task, Agent: r.Current.Agent,
					Base: r.Current.Base, Commit: r.Current.Commit, Branch: r.Current.Branch, Outcome: "superseded",
					Reason: fmt.Sprintf("plan reloaded as rev %d", rev)})
				r.Current = Attempt{}
			}
			r.Escalated, r.Subject = true, ""
			if inFile {
				r.State, r.Hash, r.Reviewer, r.Rejections = Pending, m.Hash(), f.ReviewerOf(m), 0
				if force {
					r.Reason = "reload --force: " + reason
				}
			} else {
				r.State, r.Reason = Aborted, fmt.Sprintf("removed in rev %d", rev)
			}
		}
		out[id] = r
		if inFile {
			counts.Changed++
		} else {
			counts.Removed++
		}
	}
	if len(refused) > 0 {
		return nil, reloadCounts{}, errors.New("reload refused: " + strings.Join(refused, "; "))
	}
	return out, counts, nil
}
