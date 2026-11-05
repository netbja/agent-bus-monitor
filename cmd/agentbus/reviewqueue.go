package main

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

// review-queue's reader (spec docs/superpowers/specs/2026-11-04-review-queue-design.md
// §3): the team as team-stall reads it, every open gate with its challenge's
// delivery, the verdict ledger read forward, the retained challenges, and the
// plan's done tasks. The judgement is health.EvaluateReviewQueue.

func (s *stallReader) reviewQueue(ctx context.Context) (health.Outcome, error) {
	now := s.now()
	set := readStallSettings(s.getenv)
	in := health.ReviewQueueInput{Now: now, After: set.stall}
	defer s.keep(func() { s.lastReview = &in }) // every return: never a stale wake
	if in.After <= 0 {
		return health.EvaluateReviewQueue(in), nil
	}
	fail := func(phrase string) (health.Outcome, error) {
		in.ReadErr = phrase
		return health.EvaluateReviewQueue(in), nil
	}
	tr, phrase := s.readTeam(ctx, now, set)
	if phrase != "" {
		return fail(phrase)
	}
	in.Agents, in.NoIdentities = tr.agents, tr.noIdentities
	if phrase, err := s.readOpenWork(ctx, tr.team, &in.Open); err != nil {
		return fail(phrase)
	}
	if phrase := s.answerWaitsLive(ctx, &tr); phrase != "" { // stale-waiting
		return fail(phrase)
	}
	in.Agents = tr.agents
	cmds, _, err := s.b.Recent(ctx, []string{"cmd"}, stallCmdScan)
	if err != nil {
		return fail("cmd history unavailable")
	}
	in.Challenges = map[string][]string{}
	challengeID := map[string]string{} // target|ref -> its newest challenge entry
	for _, e := range cmds {
		if e.Type != bus.CmdChallenge || e.Ref == "" {
			continue
		}
		if !slices.Contains(in.Challenges[e.Ref], e.Target) {
			in.Challenges[e.Ref] = append(in.Challenges[e.Ref], e.Target)
		}
		challengeID[e.Target+"|"+e.Ref] = e.ID
	}
	for name := range tr.snaps {
		gates, err := s.b.OpenChallenges(ctx, name)
		if err != nil {
			return fail("gates unavailable")
		}
		if len(gates) == 0 {
			continue
		}
		o, err := s.b.Obligation(ctx, name, bus.DefaultHorizon)
		if err != nil {
			return fail("delivery cursors unavailable")
		}
		for ref, meta := range gates {
			g := health.ReviewGate{Reviewer: name, Ref: ref, Subject: bus.ParseGateMeta(meta).Subject}
			if id, ok := challengeID[name+"|"+ref]; ok {
				g.At = time.UnixMilli(streamIDMillis(id))
				g.Delivered = !health.StreamIDLess(o.LastDelivered, id) && !slices.Contains(o.PendingIDs, id)
			} else {
				// Trimmed: nothing proves it was read. A group skips an
				// entry trimmed before it read it, so a cursor past it
				// proves nothing either: not judged (reviewer-codex).
				g.DeliveryUnknown = true
			}
			in.Gates = append(in.Gates, g)
		}
	}
	ledger, err := s.ledger(ctx)
	if err != nil {
		return fail("verdicts unavailable")
	}
	for _, v := range ledger {
		in.Verdicts = append(in.Verdicts, health.ReviewVerdict{Subject: v.Subject, Author: v.Author, Reviewer: v.Reviewer,
			Decision: v.Decision, Head: v.Head, Ref: v.Ref, At: time.UnixMilli(v.TS)})
	}
	act, ok, err := plan.Load(ctx, s.b)
	if err != nil {
		return fail("plan unavailable")
	}
	in.DoneSubjects = map[string]bool{}
	if ok {
		verdicts, err := s.readVerdicts(ctx)
		if err != nil {
			return fail("verdicts unavailable")
		}
		pin, errs := plan.ReadWithVerdicts(ctx, s.b, act.File, verdicts, true)
		if len(errs) > 0 {
			return fail("plan inputs unavailable")
		}
		for _, r := range plan.Derive(act.File, pin) {
			if r.State != plan.Done {
				continue
			}
			if sub := r.Task.ReviewSubject(); sub != "" {
				in.DoneSubjects[sub] = true
			}
			if r.PR > 0 {
				in.DoneSubjects["pr:"+strconv.Itoa(r.PR)] = true
			}
		}
	}
	return health.EvaluateReviewQueue(in), nil
}

// ledger is the verdict ledger in order, read forward (readVerdicts).
func (s *stallReader) ledger(ctx context.Context) ([]bus.Verdict, error) {
	if _, err := s.readVerdicts(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.verdicts), nil
}
