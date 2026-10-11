package mission

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/netbja/agent-bus-monitor/bus"
)

// checkComplete signals the escalation target once per revision when every
// mission of the plan is integrated (skipped included). The change suite runs
// once more on the branch head first: the last mission may have been skipped,
// or the revision may have changed the suite. A red suite is escalated once
// per head. The change stays active: reload and abort rely on it.
func (c *Conductor) checkComplete(ctx context.Context, ch *change) error {
	if ch.rec.CompletedRev == ch.rec.Rev || len(ch.file.Missions) == 0 {
		return nil
	}
	integrated, skipped := 0, 0
	for _, m := range ch.file.Missions {
		r, ok := ch.missions[m.ID]
		if !ok || r.State != Integrated {
			return nil
		}
		if r.Skipped {
			skipped++
		} else {
			integrated++
		}
	}
	dir, err := c.ensureIntegrationWorktree(ctx, ch)
	if err != nil {
		return err
	}
	if dirty, err := c.Git.Dirty(ctx, dir); err != nil {
		return err
	} else if dirty {
		// Complete waits for a clean tree; repairIntegration reports it once
		// (DirtyEscalated) and clears the flag when it is clean again.
		return c.repairIntegration(ctx, ch)
	}
	if ch.rec.DirtyEscalated {
		if err := c.repairIntegration(ctx, ch); err != nil { // clean again: clear the flag
			return err
		}
	}
	head, err := c.Git.Head(ctx, dir)
	if err != nil {
		return err
	}
	if ch.rec.CompleteFailedHead == head {
		return nil
	}
	ok := true
	if ch.file.Test != "" {
		log := filepath.Join(c.ProjectRoot, ch.rec.Change, "logs", fmt.Sprintf("complete-rev%d.log", ch.rec.Rev))
		if ok, _, err = RunTest(ctx, dir, ch.file.Test, log, c.TestLimit); err != nil {
			return err
		}
	}
	rec := ch.rec
	if !ok {
		rec.CompleteFailedHead = head
		msg := fmt.Sprintf("change %s rev %d cannot complete: the change suite is red on %s@%s (log %s)",
			rec.Change, rec.Rev, rec.Branch, short(head), filepath.Join(c.ProjectRoot, rec.Change, "logs"))
		if err := c.Store.PutChangeWithCmd(ctx, rec, c.Self, c.EscalateTo, bus.CmdEscalation, msg); err != nil {
			return err
		}
		ch.rec = rec
		return nil
	}
	rec.CompletedRev, rec.CompletedHead, rec.CompleteFailedHead = rec.Rev, head, ""
	msg := fmt.Sprintf("change %s rev %d complete on %s@%s: %d integrated, %d skipped; open the PR to %s",
		rec.Change, rec.Rev, rec.Branch, short(head), integrated, skipped, rec.Base)
	if err := c.Store.PutChangeWithCmd(ctx, rec, c.Self, c.EscalateTo, bus.CmdComplete, msg); err != nil {
		return err
	}
	ch.rec = rec
	return nil
}
