package mission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (c *Conductor) integrationDir(change string) string {
	return filepath.Join(c.ProjectRoot, change, "integration")
}

// ensureIntegrationWorktree checks the change branch out in the conductor's
// own worktree, creating it on first use.
func (c *Conductor) ensureIntegrationWorktree(ctx context.Context, ch *change) (string, error) {
	dir := c.integrationDir(ch.rec.Change)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	return dir, c.Git.AddWorktree(ctx, dir, ch.rec.Branch, false)
}

// integrate merges an approved mission into the change branch, then runs the
// change's suite (spec §5.2). Only a green suite recorded for the current
// head makes it integrated; a conflict or a red suite restores the previous
// head and blocks the mission.
func (c *Conductor) integrate(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	dir, err := c.ensureIntegrationWorktree(ctx, ch)
	if err != nil {
		return err
	}
	if r.PreHead, err = c.Git.Head(ctx, dir); err != nil {
		return err
	}
	r.TestedHead, r.TestedOK = "", false
	if err := c.transition(ctx, ch, m, r, EvIntegrateStarted, ""); err != nil {
		return err
	}
	msg := MergeMessage(ch.rec.Change, m.ID, ch.rec.Rev, m.Title, r.Current.Commit, r.Hash)
	if err := c.Git.MergeNoFF(ctx, dir, r.Current.Commit, msg); err != nil {
		if !errors.Is(err, ErrConflict) {
			return err // stays integrating; Startup resumes it
		}
		if err := c.Git.MergeAbort(ctx, dir); err != nil {
			return err
		}
		return c.transition(ctx, ch, m, r, EvIntegrateFailed,
			fmt.Sprintf("merge conflict integrating %s into %s", short(r.Current.Commit), ch.rec.Branch))
	}
	return c.finishIntegration(ctx, ch, m, r, dir)
}

// finishIntegration runs the change suite on the merged head and records the
// result, then integrates or rolls back to PreHead.
func (c *Conductor) finishIntegration(ctx context.Context, ch *change, m Mission, r *MissionRecord, dir string) error {
	head, err := c.Git.Head(ctx, dir)
	if err != nil {
		return err
	}
	ok := true
	if ch.file.Test != "" {
		log := filepath.Join(c.ProjectRoot, ch.rec.Change, "logs", "integrate-"+r.Current.Task+".log")
		var tail string
		if ok, tail, err = RunTest(ctx, dir, ch.file.Test, log, c.TestLimit); err != nil {
			return err
		}
		r.CheckTail = tail
	}
	r.TestedHead, r.TestedOK = head, ok
	if !ok {
		if err := c.Git.ResetHard(ctx, dir, r.PreHead); err != nil {
			return err
		}
		return c.transition(ctx, ch, m, r, EvIntegrateFailed,
			fmt.Sprintf("change suite red after merging %s; %s reset to %s", short(r.Current.Commit), ch.rec.Branch, short(r.PreHead)))
	}
	return c.transition(ctx, ch, m, r, EvIntegrated, "")
}

// Startup repairs what a crash left behind, before the first pass (spec
// §6.3): a merge in progress is aborted; unrecorded changes in the
// integration worktree block the change rather than being deleted; an
// interrupted integration is resumed from its record; an interrupted check
// is redone.
func (c *Conductor) Startup(ctx context.Context) error {
	names, err := c.Store.Changes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		if err := c.startupChange(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Conductor) startupChange(ctx context.Context, name string) error {
	ch, err := c.loadChange(ctx, name)
	if err != nil || ch == nil || ch.rec.State != "active" {
		return err
	}
	for id, r := range ch.missions { // a check interrupted mid-way is redone
		if r.State == Checking {
			r.State = Delivered
			if err := c.put(ctx, ch, &r); err != nil {
				return err
			}
			ch.missions[id] = r
		}
	}
	dir := c.integrationDir(name)
	if _, err := os.Stat(dir); err != nil {
		return nil // never integrated anything: nothing to repair
	}
	if in, err := c.Git.MergeInProgress(dir); err != nil {
		return err
	} else if in {
		if err := c.Git.MergeAbort(ctx, dir); err != nil {
			return err
		}
	}
	if dirty, err := c.Git.Dirty(ctx, dir); err != nil {
		return err
	} else if dirty {
		for _, r := range ch.missions {
			if r.State != Integrating {
				continue
			}
			m, _ := ch.file.Mission(r.ID)
			if err := c.transition(ctx, ch, m, &r, EvIntegrateFailed, "integration worktree has unrecorded changes ("+dir+"); nothing was deleted"); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range ch.missions {
		if r.State != Integrating {
			continue
		}
		m, _ := ch.file.Mission(r.ID)
		if err := c.resumeIntegration(ctx, ch, m, &r, dir); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conductor) resumeIntegration(ctx context.Context, ch *change, m Mission, r *MissionRecord, dir string) error {
	head, err := c.Git.Head(ctx, dir)
	if err != nil {
		return err
	}
	merged, err := c.Git.IsAncestor(ctx, r.Current.Commit, head)
	if err != nil {
		return err
	}
	if !merged {
		// The merge never landed: go back to PreHead if the branch moved
		// past it, and let the next pass integrate again.
		if r.PreHead != "" && head != r.PreHead {
			if back, _ := c.Git.IsAncestor(ctx, r.PreHead, head); back {
				if err := c.Git.ResetHard(ctx, dir, r.PreHead); err != nil {
					return err
				}
			}
		}
		r.State = Approved
		return c.put(ctx, ch, r)
	}
	if r.TestedOK && r.TestedHead == head {
		return c.transition(ctx, ch, m, r, EvIntegrated, "")
	}
	return c.finishIntegration(ctx, ch, m, r, dir)
}
