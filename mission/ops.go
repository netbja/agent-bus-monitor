package mission

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// HandleOp applies one operator command (spec §5.3). Only the conductor
// changes mission state; the CLI queues the command and waits for this answer.
func (c *Conductor) HandleOp(ctx context.Context, op bus.MissionOp) (ok bool, msg string) {
	var err error
	switch op.Op {
	case "load":
		switch {
		case len(op.Args) == 1:
			msg, err = c.load(ctx, op.Args[0], false, "")
		case len(op.Args) == 3 && op.Args[1] == "force" && strings.TrimSpace(op.Args[2]) != "":
			msg, err = c.load(ctx, op.Args[0], true, op.Args[2])
		default:
			return false, "usage: load <path>@<commit> [force <reason>]"
		}
	case "retry":
		var name, id string
		if name, id, err = missionRef(op.Args); err == nil {
			err = c.retry(ctx, name, id)
			msg = "retried " + name + "/" + id
		}
	case "abort":
		if len(op.Args) != 2 || strings.TrimSpace(op.Args[1]) == "" {
			return false, "usage: abort <change> <reason>"
		}
		err = c.abort(ctx, op.Args[0], op.Args[1])
		msg = "aborted " + op.Args[0]
	default:
		return false, "unknown operation " + op.Op
	}
	if err != nil {
		return false, err.Error()
	}
	return true, msg
}

func missionRef(args []string) (string, string, error) {
	if len(args) != 1 {
		return "", "", errors.New("usage: <change>/<id>")
	}
	name, id, ok := strings.Cut(args[0], "/")
	if !ok || name == "" || id == "" {
		return "", "", fmt.Errorf("%q is not <change>/<id>", args[0])
	}
	return name, id, nil
}

// load reads missions.json at a commit, validates it, creates the change
// branch and the records (rev 1, every mission pending).
func (c *Conductor) load(ctx context.Context, ref string, force bool, reason string) (string, error) {
	i := strings.LastIndex(ref, "@")
	if i <= 0 || i == len(ref)-1 {
		return "", fmt.Errorf("%q is not <path>@<commit>", ref)
	}
	p, rev := ref[:i], ref[i+1:]
	commit, err := c.Git.ResolveCommit(ctx, rev)
	if err != nil {
		return "", err
	}
	data, err := c.Git.Show(ctx, commit, p)
	if err != nil {
		return "", fmt.Errorf("%s not found at %s", p, short(commit))
	}
	f, err := Parse(data)
	if err != nil {
		return "", err
	}
	tree, err := c.Git.Tree(ctx, commit)
	if err != nil {
		return "", err
	}
	dir := path.Dir(p)
	if err := Validate(f, tree, func(b string) bool {
		_, err := c.Git.Show(ctx, commit, path.Join(dir, b))
		return err == nil
	}); err != nil {
		return "", err
	}
	hash := FileHash(data)
	if old, known, err := c.Store.Change(ctx, f.Change); err != nil {
		return "", err
	} else if known {
		if old.FileHash == hash {
			return fmt.Sprintf("already loaded rev %d of %s", old.Rev, f.Change), nil
		}
		return c.reload(ctx, old, f, p, commit, hash, force, reason)
	}
	if err := c.Git.EnsureBranch(ctx, f.Branch, f.Base); err != nil {
		return "", err
	}
	now, err := c.Bus.ServerNowMS(ctx)
	if err != nil {
		return "", err
	}
	// Missions first: a change record pointing at missing missions would be
	// worse than missions with no change (which no pass ever reads).
	for _, m := range f.Missions {
		if err := c.Store.PutMission(ctx, f.Change, MissionRecord{ID: m.ID, Hash: m.Hash(), State: Pending, Reviewer: f.ReviewerOf(m)}); err != nil {
			return "", err
		}
	}
	rec := ChangeRecord{Change: f.Change, Path: p, Commit: commit, FileHash: hash, Base: f.Base, Branch: f.Branch, Rev: 1, State: "active", LoadedAt: now}
	if err := c.Store.PutChange(ctx, rec); err != nil {
		return "", err
	}
	return fmt.Sprintf("loaded %s rev 1: %d missions", f.Change, len(f.Missions)), nil
}

// abort stops the conductor's work on a change: every mission still running
// is aborted (attempts superseded, review gates closed); the branch and the
// worktrees stay where they are.
func (c *Conductor) abort(ctx context.Context, name, reason string) error {
	ch, err := c.loadChange(ctx, name)
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("unknown change %s", name)
	}
	ch.rec.State = "aborted"
	if err := c.Store.PutChange(ctx, ch.rec); err != nil {
		return err
	}
	var errs []error
	for _, r := range ch.missions {
		if r.State == Integrated || r.State == Aborted {
			continue
		}
		m, _ := ch.file.Mission(r.ID)
		if err := c.transition(ctx, ch, m, &r, EvAbort, "change aborted: "+reason); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.ID, err))
		}
	}
	return errors.Join(errs...)
}

// reload builds revision n+1 of a known change (spec §6.4) and writes it in
// one fenced batch; the effects it owes (closing superseded attempts and
// review gates) are replayed by the next pass.
func (c *Conductor) reload(ctx context.Context, old ChangeRecord, f File, p, commit, hash string, force bool, reason string) (string, error) {
	if old.State != "active" {
		return "", fmt.Errorf("change %s is %s; load it under a new name", f.Change, old.State)
	}
	if f.Branch != old.Branch {
		return "", fmt.Errorf("the change branch cannot move from %s to %s", old.Branch, f.Branch)
	}
	records, err := c.Store.Missions(ctx, f.Change)
	if err != nil {
		return "", err
	}
	rev := old.Rev + 1
	updates, counts, err := planReload(records, f, rev, force, reason)
	if err != nil {
		return "", err
	}
	list := make([]MissionRecord, 0, len(updates))
	for _, r := range updates {
		list = append(list, r)
	}
	rec := old
	rec.Rev, rec.Commit, rec.Path, rec.FileHash = rev, commit, p, hash
	if err := c.Store.PutBatch(ctx, list, rec); err != nil {
		return "", err
	}
	return fmt.Sprintf("reloaded %s rev %d: %d changed, %d added, %d removed, %d unchanged",
		f.Change, rev, counts.Changed, counts.Added, counts.Removed, counts.Unchanged), nil
}
