package main

import (
	"context"
	"errors"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
)

// idle-master's reader (spec docs/superpowers/specs/2026-11-15-master-idle-design.md
// §2-§4): master as the stall guards read it (an answered waiting idle since
// the answer), its obligation in the dispatcher's sense, the pause, its
// provider's hold, and what is in its hand: the merge queue's undelivered
// heads, the board's requests addressed to it, its check-back. The
// judgement is health.EvaluateIdleMaster; the hook, idleMasterAct.

// mergeQueueStale: a merge queue pass older than three passes is no source.
const mergeQueueStale = 3 * mergeQueueEvery

func (s *stallReader) idleMaster(ctx context.Context) (health.Outcome, error) {
	set := readStallSettings(s.getenv)
	r := config.Machine(s.getenv)
	master := s.escalateTo()
	// Every time judged here is Redis time (status and answer ids, the
	// check-back, the merge queue's pass, the wake's id): so is now
	// (reviewer-codex), never the dispatcher host's clock.
	clock := s.redisNow
	if clock == nil {
		clock = s.b.ServerTime
	}
	now, err := clock(ctx)
	in := health.IdleMasterInput{Now: now, After: r.Duration("dispatch.master_idle_after"), Master: master, CheckbackMax: r.Int("dispatch.checkback_max")}
	defer s.keep(func() { s.lastMaster = &in }) // every return: never a stale wake
	fail := func(phrase string) (health.Outcome, error) {
		in.ReadErr = phrase
		return health.EvaluateIdleMaster(in), nil
	}
	if err != nil {
		return fail("broker time unavailable")
	}
	// The check-back first: its postponements tell the person on any pass,
	// the check off included (§4).
	cbs, err := s.b.Checkbacks(ctx)
	if err != nil {
		return fail("check-backs unavailable")
	}
	if cb, ok := cbs[master]; ok {
		in.Checkback = &health.MasterCheckback{What: cb.What, DueMs: cb.DueMs, SetID: cb.SetID, FirstSetID: cb.FirstSetID, Count: cb.Count}
	}
	if in.After <= 0 {
		return health.EvaluateIdleMaster(in), nil
	}
	tr, phrase := s.readTeam(ctx, now, set)
	if phrase != "" {
		return fail(phrase)
	}
	if phrase := s.answerWaitsLive(ctx, &tr); phrase != "" { // stale-waiting
		return fail(phrase)
	}
	snap, registered := tr.snaps[master]
	if !registered {
		return health.EvaluateIdleMaster(in), nil
	}
	for i := range tr.agents {
		if tr.agents[i].Name == master {
			in.Agent = &tr.agents[i]
		}
	}
	if in.Agent.State == "waiting" {
		asks, err := s.b.Asks(ctx)
		if err != nil {
			return fail("asks unavailable")
		}
		in.OpenAsks = bus.OpenQuestionsOf(master, asks)
	}
	o, err := s.b.Obligation(ctx, master, bus.DefaultHorizon)
	if err != nil {
		return fail("delivery cursors unavailable")
	}
	in.Obligation = o.Wakes()
	pause, err := s.b.Pause(ctx)
	if err != nil {
		return fail("pause unavailable")
	}
	in.Paused, in.Held = pause != nil, tr.held[snap.Provider]
	obs, err := s.b.Observed(ctx)
	if err != nil {
		return fail("observations unavailable")
	}
	ob, seen := obs[master]
	in.CannotAct = dispatch.CannotAct(master, in.Held, seen && ob.State == "blocked")
	marks, err := s.b.QuotaMarks(ctx)
	if err != nil {
		return fail("quota marks unavailable")
	}
	in.WokeEpisode, in.WokeAt = marks[health.StallMasterMark], bus.MarkAt(marks, health.StallMasterMark)
	board, err := s.b.Board(ctx)
	if err != nil {
		return fail("board unavailable")
	}
	for task, e := range board {
		// A request's owner is its target (bus/requests.go). Only a request
		// counts (§3): a task master claims (BoardClaim, working) is a plan
		// task, team-stall's (foureyes).
		if e.Request != nil && e.Owner == master && (e.State == "requested" || e.State == "accepted") {
			in.Requests = append(in.Requests, task)
		}
	}
	rec, err := s.b.MergeQueue(ctx)
	switch {
	case err != nil:
		in.QueueUnknown = "merge queue unavailable"
	case rec.ItemsAt == 0:
		in.QueueUnknown = "merge queue not read yet"
	case now.Sub(time.UnixMilli(rec.ItemsAt)) > mergeQueueStale:
		in.QueueUnknown = "merge queue not read since " + time.UnixMilli(rec.ItemsAt).Local().Format("15:04")
	default:
		for _, u := range rec.Undelivered {
			in.Heads = append(in.Heads, health.MasterHead{Subject: u.Subject, Head: u.Head})
		}
	}
	return health.EvaluateIdleMaster(in), nil
}

// idleMasterAct is the stall hook after a stored idle-master record: the
// postponed check-backs' notice, then the wake to master (its time kept
// with its mark, bus.FencedCmdOnceAt) or the person's notice, each once per
// episode. A master being swapped is skipped; the next pass sends it.
func (s *stallReader) idleMasterAct(ctx context.Context, token string) error {
	s.mu.Lock()
	var in health.IdleMasterInput
	ok := s.lastMaster != nil
	if ok {
		in = *s.lastMaster
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if n := health.CheckbackPostponed(in); n != nil {
		if _, err := s.b.FencedNotifyOnceForPerson(ctx, token, n.Mark, n.Episode, "dispatch", n.Text); err != nil {
			return err
		}
	}
	act := health.IdleMasterActs(in)
	if w := act.Wake; w != nil {
		if _, err := s.b.FencedCmdOnceAt(ctx, token, w.Mark, w.Episode, "dispatch", w.Target, bus.CmdDirective, w.Text, false); err != nil && !errors.Is(err, bus.ErrSwapInProgress) {
			return err
		}
	}
	if p := act.Person; p != nil {
		if _, err := s.b.FencedNotifyOnceForPerson(ctx, token, p.Mark, p.Episode, "dispatch", p.Text); err != nil {
			return err
		}
	}
	return nil
}
