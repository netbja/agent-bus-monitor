package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// popEnd is the dispatcher's own end of a done pop (pop-retire-auto, decision
// 2026-11-13-dispatcher-ends-done-pop): the steps of `retire <role> --forget
// --episode <ms>` (runRetire), each write fenced by the dispatch lease token
// and made as "dispatch". It re-judges the whole of PopDone before the begin,
// after it and right before the close; holds a retire intent with its own
// nonce; closes the pane (unless the pop was closed by hand), waits for the
// process; forgets with ForgetIfClearFenced, which keeps the consumer group.
// Never a boot role, never during a running turn.
//
// A wait changes no pane (nothing to tell: the next pass judges again); a
// failure is anything after a close that did not end clean, which master is
// told about.
func popEnd(ctx context.Context, b *bus.Bus, d swapDeps, token, role string, episode int64) dispatch.PopEnd {
	wait := func(format string, a ...any) dispatch.PopEnd { return dispatch.PopEnd{Wait: fmt.Sprintf(format, a...)} }
	snaps, err := b.Agents(ctx)
	if err != nil {
		return wait("%v", err)
	}
	snap, ok := snaps[role]
	switch {
	case !ok || snap.Pane == "" || snap.PID == 0:
		return wait("no verified pane recorded")
	case snap.Tier != "pop":
		return wait("not a pop: a boot role is never ended by the dispatcher")
	case snap.TS != episode:
		return wait("another episode (declared again since)")
	}
	closed := false
	if start, err := d.ProcStart(snap.PID); err != nil && !errors.Is(err, dispatch.ErrNoProcess) {
		return wait("cannot read its recorded process (pid %d): %v", snap.PID, err) // unreadable proves nothing
	} else if err != nil || start != snap.PIDStart {
		closed = true // closed by hand: the forget alone (pop-closed-by-hand)
	}
	if why := openWorkOf(ctx, b, role); why != "" {
		return wait("%s", why)
	}
	if !closed { // a running turn is never cut (nor a declaration still fresh)
		obs, err := b.Observed(ctx)
		if err != nil {
			return wait("%v", err)
		}
		if o, ok := obs[role]; ok && o.Busy && o.Fresh(d.Now().UnixMilli()) {
			return wait("its pane runs a turn")
		}
	}
	popDone := func(own string) (bool, string) {
		f := dispatch.ReadPopFacts(ctx, b, d.Runtime, role, dispatch.PopReadOptions{
			Now: d.Now(), RetireAfter: d.PopRetireAfter, OwnIntent: own, Episode: episode, ProcStart: d.ProcStart,
			Missions: func(ctx context.Context, agent string) ([]string, error) { return missionsOf(ctx, b, agent) }})
		switch {
		case closed && !f.Closed:
			return false, "its process is gone but herdr still knows its pane"
		case !closed && f.Closed:
			return false, "its process and pane went since the end began"
		}
		return dispatch.PopDone(f)
	}
	if done, why := popDone(""); !done {
		return wait("not done: %s", why)
	}
	nonce := bus.NewIntentNonce()
	own, err := b.BeginAutoRetire(ctx, token, role, nonce, snap.PIDStart,
		bus.SwapEvent{FromProvider: snap.Provider, By: dispatchBy, Reason: "work done", OldPID: snap.PID, OldPane: snap.Pane})
	if err != nil {
		return wait("%v", err) // no lease, another intent, no snapshot: nothing written
	}
	keepIntent := false
	defer func() {
		if !keepIntent {
			_ = b.EndAutoRetire(context.Background(), token, role, nonce)
		}
	}()
	logged := func(step, msg string) {
		_ = b.FencedLogSwap(context.Background(), token, bus.SwapEvent{Role: role, Event: bus.SwapFailed, By: dispatchBy, Kind: bus.SwapKindRetire, Error: step + ": " + msg})
	}
	// Before the close: nothing touched yet, so a change is a wait.
	stop := func(step, format string, a ...any) dispatch.PopEnd {
		msg := fmt.Sprintf(format, a...)
		logged(step, msg)
		return dispatch.PopEnd{Wait: msg}
	}
	if why := openWorkOf(ctx, b, role); why != "" {
		return stop("open work", "%s", why)
	}
	if done, why := popDone(own); !done {
		return stop("popdone", "not done any more: %s; nothing was closed", why)
	}
	if w, ok, err := b.Wake(ctx, role); err != nil {
		return stop("wake record", "cannot read its wake record (%v); nothing was closed", err)
	} else if ok && w.State == "prompting" {
		return stop("wake in progress", "a wake is being typed into its pane; nothing was closed")
	}
	if done, why := popDone(own); !done {
		return stop("popdone", "not done any more: %s; nothing was closed", why)
	}
	if held, err := b.AutoRetireHeld(ctx, token, role, nonce); err != nil || !held {
		return stop("intent", "its retire intent or the dispatch lease is no longer held; nothing was closed")
	}
	// From the close on, a stop is a failure: master is told.
	fail := func(step, format string, a ...any) dispatch.PopEnd {
		msg := fmt.Sprintf(format, a...)
		logged(step, msg)
		return dispatch.PopEnd{Failed: msg}
	}
	if !closed {
		if err := d.ClosePane(ctx, snap.Pane); err != nil {
			return fail("close", "%v; nothing was closed", err)
		}
		for deadline := d.Now().Add(d.PIDGone); !dispatch.ProcGone(&snap, d.ProcStart); d.Sleep(200 * time.Millisecond) {
			if d.Now().After(deadline) {
				return fail("old process", "pane %s closed but pid %d is still running after %s", snap.Pane, snap.PID, d.PIDGone)
			}
		}
	}
	ev := bus.SwapEvent{Role: role, FromProvider: snap.Provider, By: dispatchBy, Reason: "work done", OldPID: snap.PID, OldPane: snap.Pane, Kind: bus.SwapKindRetire}
	var kept []string
	res, err := b.ForgetIfClearFenced(ctx, token, role, own, bus.RetiredAs{PID: snap.PID, PIDStart: snap.PIDStart, Episode: episode})
	switch {
	case err != nil:
		ev.Forgot, ev.Error = "false", "forget: "+err.Error()
		kept = []string{err.Error()}
	case res.Forgot:
		ev.Forgot = "true"
	default:
		ev.Forgot, ev.Error = "false", "kept: "+strings.Join(res.Held, "; ")
		kept = res.Held
	}
	if err := b.FencedRetireSwap(ctx, token, ev); err != nil {
		keepIntent = true // the begin reads as interrupted; the next swap or retire records it
		return dispatch.PopEnd{Failed: fmt.Sprintf("closed (pane %s), but the ledger was not written: %v", snap.Pane, err)}
	}
	if len(kept) > 0 {
		return dispatch.PopEnd{Failed: fmt.Sprintf("closed (pane %s, pid %d gone), but its registration is kept: %s; a new spawn of %s reads it from its kept group",
			snap.Pane, snap.PID, strings.Join(kept, "; "), role)}
	}
	if closed {
		return dispatch.PopEnd{Ended: true, Detail: fmt.Sprintf("already closed (pane %s and pid %d gone), unregistered (its consumer group kept)", snap.Pane, snap.PID)}
	}
	return dispatch.PopEnd{Ended: true, Detail: fmt.Sprintf("pane %s closed, pid %d gone, unregistered (its consumer group kept)", snap.Pane, snap.PID)}
}

// dispatchBy is the name the dispatcher's own retire is made under.
const dispatchBy = "dispatch"

// openWorkOf is openWork's judgement without its exit code: "" when the
// agent takes part in no open 4-eyes challenge or mission attempt.
func openWorkOf(ctx context.Context, b *bus.Bus, agent string) string {
	reviews, err := b.ReviewsOf(ctx, agent)
	if err != nil {
		return err.Error()
	}
	missions, err := missionsOf(ctx, b, agent)
	if err != nil {
		return err.Error()
	}
	open := append(reviews, missions...)
	if len(open) == 0 {
		return ""
	}
	sort.Strings(open)
	return "open work: " + strings.Join(open, ", ")
}
