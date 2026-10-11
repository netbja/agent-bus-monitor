package mission

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Conductor reconciles every active change: it requests ready missions,
// checks deliveries, opens and reads reviews, and (Task 8) integrates. It
// holds no state of its own: everything is reread from Redis and Git on each
// pass, so a restart loses nothing.
type Conductor struct {
	Bus         *bus.Bus
	Store       Store
	Git         Git
	ProjectRoot string // StateHome/<p>; a change's files live in ProjectRoot/<c>
	EscalateTo  string // default "master"
	Self        string // the conductor's bus identity, "conductor"
	TestLimit   time.Duration
	Log         func(format string, args ...any) // optional
}

func (c *Conductor) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// change is one active change as a pass sees it.
type change struct {
	rec      ChangeRecord
	file     File
	missions map[string]MissionRecord
	board    map[string]bus.BoardEntry

	integrated bool // a pass integrates at most one mission per change
}

// Pass runs one reconciliation over every active change. An error on one
// mission does not stop the others; all errors are returned joined.
func (c *Conductor) Pass(ctx context.Context) error {
	names, err := c.Store.Changes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		if err := c.passChange(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Conductor) loadChange(ctx context.Context, name string) (*change, error) {
	rec, ok, err := c.Store.Change(ctx, name)
	if err != nil || !ok {
		return nil, err
	}
	data, err := c.Git.Show(ctx, rec.Commit, rec.Path)
	if err != nil {
		return nil, err
	}
	if FileHash(data) != rec.FileHash {
		return nil, fmt.Errorf("missions.json at %s does not match the loaded hash", rec.Commit)
	}
	f, err := Parse(data)
	if err != nil {
		return nil, err
	}
	ms, err := c.Store.Missions(ctx, name)
	if err != nil {
		return nil, err
	}
	board, err := c.Bus.Board(ctx)
	if err != nil {
		return nil, err
	}
	return &change{rec: rec, file: f, missions: ms, board: board}, nil
}

func (c *Conductor) passChange(ctx context.Context, name string) error {
	ch, err := c.loadChange(ctx, name)
	if err != nil || ch == nil || ch.rec.State != "active" {
		return err
	}
	ids := make([]string, 0, len(ch.missions))
	for id := range ch.missions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, r := range ch.missions {
		if r.State == Integrating { // left by a failed integration: repair now
			ch.integrated = true
			if err := c.repairIntegration(ctx, ch); err != nil {
				errs = append(errs, fmt.Errorf("repair integration: %w", err))
			}
			break
		}
	}
	for _, id := range ids {
		m, ok := ch.file.Mission(id)
		r := ch.missions[id]
		if !ok { // removed from the plan: replay what it still owes, never advance
			if err := c.effects(ctx, ch, Mission{ID: id}, &r); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			}
			if err := c.notifyLateSuperseded(ctx, ch, &r); err != nil {
				errs = append(errs, err)
			}
			ch.missions[id] = r
			continue
		}
		if err := c.advance(ctx, ch, m, &r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
		ch.missions[id] = r // dependents later in this pass see the new state
		if err := c.notifyLateSuperseded(ctx, ch, &r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// advance moves one mission as far as the facts allow in this pass.
func (c *Conductor) advance(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	if err := c.effects(ctx, ch, m, r); err != nil { // replay what a failure or a crash left owed
		return err
	}
	if r.State == Pending && c.depsIntegrated(ch, m) {
		if err := c.transition(ctx, ch, m, r, EvDepsIntegrated, ""); err != nil {
			return err
		}
	}
	if r.State == Ready {
		if r.Current.Task != "" && r.Current.N == r.Attempt {
			// A crash after persisting the attempt: it may already be published.
			r.State = Requested
			if err := c.put(ctx, ch, r); err != nil {
				return err
			}
		} else if err := c.newAttempt(ctx, ch, m, r, m.Agent, ""); err != nil {
			return err
		}
	}
	if r.State == Requested || r.State == Accepted {
		if err := c.ensurePublished(ctx, ch, m, r); err != nil {
			return err
		}
		e := ch.board[r.Current.Task]
		switch e.State {
		case "accepted":
			if r.State == Requested {
				if err := c.transition(ctx, ch, m, r, EvAccepted, ""); err != nil {
					return err
				}
			}
		case "blocked":
			reason := "blocked by " + r.Current.Agent
			if e.Request != nil && e.Request.BlockedReason != "" {
				reason += ": " + e.Request.BlockedReason
			}
			return c.transition(ctx, ch, m, r, EvAgentBlocked, reason)
		case "done":
			if r.State == Requested { // accept and done between two passes
				if err := c.transition(ctx, ch, m, r, EvAccepted, ""); err != nil {
					return err
				}
			}
			if e.Request != nil {
				r.Current.Commit, r.Current.Branch = e.Request.Commit, e.Request.Branch
			}
			if err := c.transition(ctx, ch, m, r, EvDelivered, ""); err != nil {
				return err
			}
		}
	}
	if r.State == Delivered {
		if err := c.check(ctx, ch, m, r); err != nil {
			return err
		}
	}
	if r.State == InReview {
		if err := c.readReview(ctx, ch, m, r); err != nil {
			return err
		}
	}
	if r.State == Approved && !ch.integrated { // one integration per pass
		ch.integrated = true
		return c.integrate(ctx, ch, m, r)
	}
	return nil
}

func (c *Conductor) depsIntegrated(ch *change, m Mission) bool {
	for _, d := range m.DependsOn {
		if ch.missions[d].State != Integrated {
			return false
		}
	}
	return true
}

func (c *Conductor) put(ctx context.Context, ch *change, r *MissionRecord) error {
	return c.Store.PutMission(ctx, ch.rec.Change, *r)
}

// transition applies Next and persists the new state together with the
// effects it owes (a review to open or close, an escalation, attempts to
// close), then performs them through effects. An effect that fails is not
// lost: the flags stay set and every pass replays what is still owed.
func (c *Conductor) transition(ctx context.Context, ch *change, m Mission, r *MissionRecord, ev Event, reason string) error {
	if ev == EvCheckFailed || ev == EvReviewRejected {
		r.Rejections++
	}
	next, acts, err := Next(r.State, ev, Ctx{AttemptsLeft: ch.file.MaxAttempts - r.Rejections})
	if err != nil {
		return err
	}
	prev := r.Current
	r.State = next
	if reason != "" {
		r.Reason = reason
	}
	sends := hasAction(acts, ActSendRequest)
	for _, a := range acts {
		switch a {
		case ActSupersede:
			outcome := "superseded"
			switch ev {
			case EvCheckFailed:
				outcome = "check_failed"
			case EvReviewRejected:
				outcome = "rejected"
			}
			by := ""
			if sends {
				by = TaskName(ch.rec.Change, m.ID, r.Attempt+1)
			}
			r.History = append(r.History, Attempt{N: prev.N, Task: prev.Task, Agent: prev.Agent, Base: prev.Base,
				Commit: prev.Commit, Branch: prev.Branch, Outcome: outcome, Reason: reason, By: by})
		case ActOpenReview:
			r.Subject = "mission:" + ch.rec.Change + "/" + m.ID + "@" + short(r.Current.Commit)
			r.ReviewRef, r.ReviewOpen = r.Current.Task, false
		case ActEscalate:
			r.Escalated = false
		}
		// ActResolveReview needs nothing here: leaving in_review with
		// ReviewOpen set is what effects closes.
	}
	if sends {
		agent := m.Agent
		if prev.Agent != "" {
			agent = prev.Agent
		}
		if err := c.newAttempt(ctx, ch, m, r, agent, reason); err != nil { // persists, publishes
			return err
		}
	} else if err := c.put(ctx, ch, r); err != nil {
		return err
	}
	return c.effects(ctx, ch, m, r)
}

// effects performs, idempotently, every effect the record still owes, and
// records each one as done right after it succeeds.
func (c *Conductor) effects(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	for i := range r.History {
		h := &r.History[i]
		if h.Closed || h.Task == "" {
			continue
		}
		if err := c.closeAttempt(ctx, ch, *h, h.By, h.Reason); err != nil {
			return fmt.Errorf("close %s: %w", h.Task, err)
		}
		h.Closed = true
		if err := c.put(ctx, ch, r); err != nil {
			return err
		}
	}
	if r.ReviewOpen && (r.State != InReview || r.ReviewRef != r.Current.Task) {
		if err := c.Bus.FencedResolveChallenge(ctx, c.Store.Token, r.Reviewer, r.ReviewRef); err != nil {
			return fmt.Errorf("close review: %w", err)
		}
		r.ReviewOpen = false
		if err := c.put(ctx, ch, r); err != nil {
			return err
		}
	}
	if r.State == InReview && !r.ReviewOpen && r.ReviewRef != "" {
		if err := c.openReview(ctx, ch, m, r); err != nil {
			return fmt.Errorf("open review: %w", err)
		}
		r.ReviewOpen = true
		if err := c.put(ctx, ch, r); err != nil {
			return err
		}
	}
	if r.State == Blocked && !r.Escalated {
		if _, err := c.Bus.FencedCmdRef(ctx, c.Store.Token, c.Self, c.EscalateTo, bus.CmdEscalation, "",
			fmt.Sprintf("%s/%s blocked: %s. Next: agentbus mission retry %s/%s | agentbus mission abort %s",
				ch.rec.Change, r.ID, r.Reason, ch.rec.Change, r.ID, ch.rec.Change), false); err != nil {
			return fmt.Errorf("escalate: %w", err)
		}
		r.Escalated = true
		if err := c.put(ctx, ch, r); err != nil {
			return err
		}
	}
	return nil
}

func hasAction(acts []Action, a Action) bool {
	for _, x := range acts {
		if x == a {
			return true
		}
	}
	return false
}

// newAttempt opens attempt n+1 for agent, persists it (state requested), then
// publishes its request. Publishing is idempotent: ensurePublished republishes
// a persisted attempt whose request a crash lost.
func (c *Conductor) newAttempt(ctx context.Context, ch *change, m Mission, r *MissionRecord, agent, previous string) error {
	base, err := c.Git.ResolveCommit(ctx, ch.rec.Branch)
	if err != nil {
		return err
	}
	r.Attempt++
	// A name already on the board belongs to an attempt this record no longer
	// knows (a recover after losing Redis): never reuse it as ours.
	for _, taken := ch.board[TaskName(ch.rec.Change, m.ID, r.Attempt)]; taken; _, taken = ch.board[TaskName(ch.rec.Change, m.ID, r.Attempt)] {
		if r.Attempt >= 99 {
			return fmt.Errorf("%s/%s: no free request name left (99 attempts)", ch.rec.Change, m.ID)
		}
		r.Attempt++
	}
	r.Current = Attempt{N: r.Attempt, Task: TaskName(ch.rec.Change, m.ID, r.Attempt), Agent: agent, Base: base}
	r.Subject, r.LogPath, r.CheckTail = "", "", "" // ReviewRef stays until its gate is closed
	r.State = Requested
	if previous != "" {
		r.Reason = previous
	}
	if err := c.put(ctx, ch, r); err != nil {
		return err
	}
	return c.ensurePublished(ctx, ch, m, r)
}

// ensurePublished publishes the current attempt's request unless the board
// already holds it.
func (c *Conductor) ensurePublished(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	if _, ok := ch.board[r.Current.Task]; ok {
		return nil
	}
	brief, anchor, _ := strings.Cut(m.Brief, "#")
	dir := filepath.ToSlash(filepath.Dir(ch.rec.Path))
	body := fmt.Sprintf("mission %s/%s rev %d: %s | base=%s | brief=%s/%s@%s#%s | deliver: agentbus request done %s --commit <sha> --branch <b> \"<summary>\"",
		ch.rec.Change, m.ID, ch.rec.Rev, m.Title, r.Current.Base, dir, brief, ch.rec.Commit, anchor, r.Current.Task)
	if r.Current.N > 1 && r.Reason != "" {
		body += " | previous: " + strings.ReplaceAll(r.Reason, "\n", " ")
	}
	_, err := c.Bus.FencedRequest(ctx, c.Store.Token, r.Current.Task, c.Self, r.Current.Agent, "", body)
	if err != nil && strings.Contains(err.Error(), "board task already exists: "+r.Current.Task) {
		err = nil // published by a pass that crashed before recording it
	}
	if err == nil {
		ch.board[r.Current.Task] = bus.BoardEntry{Owner: r.Current.Agent, State: "requested"}
	}
	return err
}

// closeAttempt supersedes an old attempt: its board entry stops counting as
// waiting, and its agent is told on the old thread, without being woken.
func (c *Conductor) closeAttempt(ctx context.Context, ch *change, old Attempt, newTask, reason string) error {
	if err := c.Bus.FencedBoardState(ctx, c.Store.Token, old.Task, "superseded"); err != nil {
		if strings.Contains(err.Error(), "unknown board task") {
			return nil // never published
		}
		return err
	}
	if e, ok := ch.board[old.Task]; ok { // this pass must not read the old state
		e.State = "superseded"
		ch.board[old.Task] = e
	}
	return c.replySuperseded(ctx, old, newTask, reason)
}

func (c *Conductor) replySuperseded(ctx context.Context, old Attempt, newTask, reason string) error {
	board, err := c.Bus.Board(ctx)
	if err != nil {
		return err
	}
	thread := ""
	if e, ok := board[old.Task]; ok && e.Request != nil {
		thread = e.Request.Thread
	}
	msg := "superseded by " + newTask + ": stop, do not deliver"
	if newTask == "" {
		msg = "cancelled: stop, do not deliver"
	}
	if reason != "" {
		msg = reason + " — " + msg
	}
	_, err = c.Bus.FencedCmdRef(ctx, c.Store.Token, c.Self, old.Agent, bus.CmdReply, thread, msg, true)
	return err
}

// notifyLateSuperseded answers, once per board state, an agent that accepted
// or completed a task after it was superseded.
func (c *Conductor) notifyLateSuperseded(ctx context.Context, ch *change, r *MissionRecord) error {
	changed := false
	for i, a := range r.History {
		if !strings.HasPrefix(a.Outcome, "superseded") {
			continue
		}
		st := ch.board[a.Task].State
		if st != "accepted" && st != "done" && st != "blocked" {
			continue
		}
		if a.Outcome == "superseded_notified_"+st {
			continue
		}
		if err := c.replySuperseded(ctx, a, r.Current.Task, ""); err != nil {
			return err
		}
		r.History[i].Outcome = "superseded_notified_" + st
		changed = true
	}
	if changed {
		return c.put(ctx, ch, r)
	}
	return nil
}

// check runs the mechanical checks on a delivery (spec §5.2).
func (c *Conductor) check(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	if err := c.transition(ctx, ch, m, r, EvCheckStarted, ""); err != nil {
		return err
	}
	reason, err := c.runChecks(ctx, ch, m, r)
	if err != nil {
		// The conductor's own failure, not the delivery's: back to delivered so
		// the next pass checks again; no rejection is counted.
		r.State = Delivered
		if perr := c.put(ctx, ch, r); perr != nil {
			return errors.Join(err, perr)
		}
		return err
	}
	if reason != "" {
		return c.transition(ctx, ch, m, r, EvCheckFailed, reason)
	}
	return c.transition(ctx, ch, m, r, EvCheckPassed, "")
}

// runChecks returns the reason a delivery fails, or "" when it passes. err is
// for the conductor's own failures, never the delivery's.
func (c *Conductor) runChecks(ctx context.Context, ch *change, m Mission, r *MissionRecord) (string, error) {
	if r.Current.Commit == "" {
		return "no commit delivered (use agentbus request done <task> --commit <sha>)", nil
	}
	commit, err := c.Git.ResolveCommit(ctx, r.Current.Commit)
	if err != nil {
		return fmt.Sprintf("commit %s not found in %s", r.Current.Commit, c.Git.Repo), nil
	}
	r.Current.Commit = commit
	if ok, err := c.Git.IsAncestor(ctx, r.Current.Base, commit); err != nil {
		return "", err
	} else if !ok {
		return fmt.Sprintf("commit %s does not descend from base %s", short(commit), short(r.Current.Base)), nil
	}
	names, err := c.Git.DiffNames(ctx, r.Current.Base, commit)
	if err != nil {
		return "", err
	}
	var outside []string
	for _, n := range names {
		if !matchAny(m.AllowedPaths, n) {
			outside = append(outside, n)
		}
	}
	if len(outside) > 0 {
		return "files outside allowed_paths: " + strings.Join(outside, ", "), nil
	}
	dir := filepath.Join(c.ProjectRoot, ch.rec.Change, "check-"+r.Current.Task)
	r.LogPath = filepath.Join(c.ProjectRoot, ch.rec.Change, "logs", r.Current.Task+".log")
	_ = c.Git.RemoveWorktree(ctx, dir) // a leftover from a crashed check
	if err := c.Git.AddWorktree(ctx, dir, commit, true); err != nil {
		return "", err
	}
	defer c.Git.RemoveWorktree(context.WithoutCancel(ctx), dir)
	ok, tail, err := RunTest(ctx, dir, m.Test, r.LogPath, c.TestLimit)
	if err != nil {
		return "", err
	}
	r.CheckTail = tail
	if !ok {
		return "tests failed (" + r.LogPath + "):\n" + tail, nil
	}
	return "", nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// openReview opens the 4-eyes challenge on the assigned reviewer unless its
// gate already holds ReviewRef (a crash between the two steps).
func (c *Conductor) openReview(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	open, err := c.Bus.OpenChallenges(ctx, r.Reviewer)
	if err != nil {
		return err
	}
	if _, ok := open[r.ReviewRef]; ok {
		return nil
	}
	msg := fmt.Sprintf("review %s (%s by %s, log %s). Answer: agentbus verdict --subject %s --ref %s %s <approve|reject> <message>",
		r.Subject, m.Title, r.Current.Agent, r.LogPath, r.Subject, r.ReviewRef, r.Current.Agent)
	return c.Bus.FencedChallenge(ctx, c.Store.Token, r.Reviewer, r.ReviewRef, c.Self, msg)
}

// readReview rolls up the verdicts of the assigned reviewer on this exact
// subject; any other verdict is ignored.
func (c *Conductor) readReview(ctx context.Context, ch *change, m Mission, r *MissionRecord) error {
	vs, err := c.Bus.Verdicts(ctx, r.Subject)
	if err != nil {
		return err
	}
	var mine []bus.Verdict
	for _, v := range vs {
		if v.Author == r.Current.Agent && v.Reviewer == r.Reviewer {
			mine = append(mine, v)
		} else {
			c.logf("%s/%s: ignored verdict by %s on %s", ch.rec.Change, m.ID, v.Reviewer, r.Subject)
		}
	}
	switch state, idx := bus.RollUp(mine); state {
	case "APPROVED":
		return c.transition(ctx, ch, m, r, EvApproved, "")
	case "REJECTED":
		return c.transition(ctx, ch, m, r, EvReviewRejected, "review rejected by "+r.Reviewer+": "+mine[idx].Message)
	}
	return nil
}

// retry is the operator's retry of a blocked mission: a new round of
// max_attempts, a new attempt for the same agent.
func (c *Conductor) retry(ctx context.Context, changeName, id string) error {
	ch, err := c.loadChange(ctx, changeName)
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("unknown change %s", changeName)
	}
	m, ok := ch.file.Mission(id)
	r, known := ch.missions[id]
	if !ok || !known {
		return fmt.Errorf("unknown mission %s/%s", changeName, id)
	}
	if r.State != Blocked {
		return fmt.Errorf("%s/%s is %s, not blocked", changeName, id, r.State)
	}
	r.Rejections = 0
	return c.transition(ctx, ch, m, &r, EvRetry, "retried by the operator")
}
