package mission

import (
	"context"
	"fmt"
)

// recoverRecords rebuilds the mission records from the conductor's merges
// (spec §6.5): the most recent merge of each mission decides — integrated
// when it carries the current definition's hash, blocked otherwise; a
// mission never merged is pending. rev is the highest Mission-Rev seen, or 1.
func recoverRecords(f File, merges []MissionMerge) (map[string]MissionRecord, int) {
	last := map[string]MissionMerge{}
	rev := 1
	for _, mg := range merges { // oldest first: the last one wins
		last[mg.ID] = mg
		rev = max(rev, mg.Rev)
	}
	out := map[string]MissionRecord{}
	for _, m := range f.Missions {
		r := MissionRecord{ID: m.ID, Hash: m.Hash(), State: Pending, Reviewer: f.ReviewerOf(m)}
		if mg, ok := last[m.ID]; ok {
			r.Current = Attempt{Commit: mg.Commit}
			if mg.Hash == r.Hash {
				r.State, r.TestedOK, r.Reason = Integrated, true, fmt.Sprintf("recovered from its merge (rev %d)", mg.Rev)
			} else {
				r.State = Blocked
				r.Reason = fmt.Sprintf("merged under another definition (rev %d); decide with skip, retry or reload", mg.Rev)
			}
		}
		out[m.ID] = r
	}
	return out, rev
}

// recover rebuilds a change lost from Redis from its branch's merge trailers,
// in one fenced batch.
func (c *Conductor) recover(ctx context.Context, ref string) (string, error) {
	p, commit, data, f, err := c.readMissions(ctx, ref)
	if err != nil {
		return "", err
	}
	if _, known, err := c.Store.Change(ctx, f.Change); err != nil {
		return "", err
	} else if known {
		return "", fmt.Errorf("%s is already loaded; recover is for a lost store", f.Change)
	}
	if err := c.Git.EnsureBranch(ctx, f.Branch, f.Base); err != nil {
		return "", err
	}
	merges, err := c.Git.MissionMerges(ctx, f.Branch, f.Change)
	if err != nil {
		return "", err
	}
	records, rev := recoverRecords(f, merges)
	list := make([]MissionRecord, 0, len(records))
	var integrated, blocked, pending int
	for _, r := range records {
		list = append(list, r)
		switch r.State {
		case Integrated:
			integrated++
		case Blocked:
			blocked++
		default:
			pending++
		}
	}
	now, err := c.Bus.ServerNowMS(ctx)
	if err != nil {
		return "", err
	}
	rec := ChangeRecord{Change: f.Change, Path: p, Commit: commit, FileHash: FileHash(data), Base: f.Base, Branch: f.Branch,
		Rev: rev, State: "active", LoadedAt: now}
	if err := c.Store.PutBatch(ctx, list, rec); err != nil {
		return "", err
	}
	return fmt.Sprintf("recovered %s rev %d: %d integrated, %d blocked, %d pending", f.Change, rev, integrated, blocked, pending), nil
}
