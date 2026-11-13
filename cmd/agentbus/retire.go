package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// `agentbus retire <role> [--reason <text>] [--now]` (v0.15.1 §11.1): close
// one agent's tab, with the swap's checks and ledger, and launch nothing.
// `agentbus shutdown` is the team-wide stop; this is for one role, such as
// architect once its recommendation is delivered.
//
// `--forget --episode <ms>` (spec 2026-11-03-pop-auto-retire-design.md §4) is
// a pop's end: the whole of PopDone judged before the begin, after it and
// right before the close, then ForgetIfClear unregisters it in one script,
// keeping its consumer group, or keeps it when anything arrived.
//
// pop-closed-by-hand: a plain retire of a pop ends with the same forget
// (a boot role never), and --forget accepts a pop already closed by hand
// (its verified process gone, herdr knowing its pane no more): nothing is
// closed, and ForgetIfClear still checks pid, start, episode and what waits.

const retireUsage = "usage: agentbus retire <role> [--reason <text>] [--now] | retire <role> --forget --episode <ms> [--reason <text>]"

func runRetire(ctx context.Context, b *bus.Bus, d swapDeps, self string, args []string, out, errw io.Writer) int {
	args, now := extractBool(args, "--now")
	args, reason := extractFlag(args, "--reason")
	args, forget := extractBool(args, "--forget")
	args, episodeArg := extractFlag(args, "--episode")
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(errw, retireUsage)
		return 2
	}
	var episode int64
	if forget || episodeArg != "" {
		n, err := strconv.ParseInt(episodeArg, 10, 64)
		if !forget || err != nil || n <= 0 || now {
			fmt.Fprintln(errw, "retire: --forget needs --episode <ms> (the declaration the dispatcher judged) and refuses --now")
			fmt.Fprintln(errw, retireUsage)
			return 2
		}
		episode = n
	}
	role := args[0]
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errw, "retire %s: "+format+"\n", append([]any{role}, a...)...)
		return 1
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return fail("%v", err)
	}
	snap, ok := snaps[role]
	if !ok || snap.Pane == "" || snap.PID == 0 {
		return fail("no verified pane recorded (agentbus agents): nothing to close")
	}
	// A pop is unregistered by its retire too (pop-closed-by-hand), with the
	// retire intent ForgetIfClear checks; a boot role never is.
	pop := snap.Tier == "pop"
	// closed: a pop already closed by hand (its process and pane gone), which
	// only --forget ends: nothing to close, the forget alone (pop-closed-by-hand).
	closed := false
	if start, err := d.ProcStart(snap.PID); err != nil && !errors.Is(err, dispatch.ErrNoProcess) {
		// Unreadable proves nothing either way (reviewer-codex).
		return fail("cannot read its recorded process (pid %d): %v; nothing closed", snap.PID, err)
	} else if err == nil && start == snap.PIDStart {
		// alive: the usual retire
	} else {
		if !forget || !pop {
			hint := ""
			if pop {
				hint = fmt.Sprintf(" (a pop: agentbus retire %s --forget --episode %d unregisters it once nothing waits)", role, snap.TS)
			}
			return fail("its recorded process (pid %d) is not running any more: nothing to close%s", snap.PID, hint)
		}
		closed = true
	}
	if pane := d.Env("HERDR_PANE_ID"); pane != "" && pane == snap.Pane {
		return fail("an agent cannot retire itself: run it from master's pane or a terminal")
	}
	if code := openWork(ctx, b, role, fail); code != 0 {
		return code
	}
	// popDone re-judges the whole of §2 (pop-auto-retire); own is retire's
	// own intent once written, "" before.
	popDone := func(own string) (bool, string) {
		f := dispatch.ReadPopFacts(ctx, b, d.Runtime, role, dispatch.PopReadOptions{
			Now: d.Now(), RetireAfter: d.PopRetireAfter, OwnIntent: own, Episode: episode, ProcStart: d.ProcStart,
			Missions: func(ctx context.Context, agent string) ([]string, error) { return missionsOf(ctx, b, agent) }})
		switch {
		case closed && !f.Closed:
			return false, "its process is gone but herdr still knows its pane: close that pane first"
		case !closed && f.Closed:
			return false, "its process and pane went since retire began"
		}
		return dispatch.PopDone(f)
	}
	if forget {
		if done, why := popDone(""); !done {
			return fail("retire-refused: not done: %s; nothing closed", why)
		}
	}
	if !now && !closed {
		obs, err := b.Observed(ctx)
		if err != nil {
			return fail("%v", err)
		}
		if o, ok := obs[role]; ok && o.Busy && o.Fresh(d.Now().UnixMilli()) {
			return fail("its pane runs a turn right now; wait, or pass --now (the turn is lost)")
		}
		if snap.State == "working" && d.Now().Sub(time.UnixMilli(snap.TS)) < d.Recent {
			return fail("it declared working %s ago; wait, or pass --now (the turn is lost)", d.Now().Sub(time.UnixMilli(snap.TS)).Round(time.Second))
		}
	}
	closeInterrupted(ctx, b, role, self)
	intent := bus.SwapIntent{By: self, OldPID: snap.PID, OldStart: snap.PIDStart}
	if forget || pop {
		intent.Kind, intent.Nonce = bus.SwapKindRetire, bus.NewIntentNonce()
	}
	own := intent.Encode()
	if err := b.BeginSwap(ctx, role, intent,
		bus.SwapEvent{FromProvider: snap.Provider, By: self, Reason: reason, OldPID: snap.PID, OldPane: snap.Pane}); err != nil {
		return fail("%v", err)
	}
	keepIntent := false
	defer func() {
		if !keepIntent {
			_ = b.EndSwap(context.Background(), role)
		}
	}()
	failed := func(step, format string, a ...any) int {
		msg := fmt.Sprintf(format, a...)
		_ = b.LogSwap(context.Background(), bus.SwapEvent{Role: role, Event: bus.SwapFailed, By: self, Error: step + ": " + msg})
		return fail("%s", msg)
	}
	if code := openWork(ctx, b, role, func(f string, a ...any) int { return failed("open work", f, a...) }); code != 0 {
		return code
	}
	if forget {
		if done, why := popDone(own); !done {
			return failed("popdone", "not done any more: %s; nothing was closed", why)
		}
	}
	for deadline := d.Now().Add(10 * time.Second); ; d.Sleep(200 * time.Millisecond) {
		w, ok, err := b.Wake(ctx, role)
		if err != nil {
			return failed("wake record", "cannot read its wake record (%v); nothing was closed", err)
		}
		if !ok || w.State != "prompting" {
			break
		}
		if d.Now().After(deadline) {
			return failed("wake in progress", "a wake is still being typed into its pane; retry in a minute")
		}
	}
	if forget {
		if done, why := popDone(own); !done {
			return failed("popdone", "not done any more: %s; nothing was closed", why)
		}
	}
	if !closed {
		if err := d.ClosePane(ctx, snap.Pane); err != nil {
			return failed("close", "%v; nothing was closed", err)
		}
		for deadline := d.Now().Add(d.PIDGone); ; d.Sleep(200 * time.Millisecond) {
			if dispatch.ProcGone(&snap, d.ProcStart) {
				break
			}
			if d.Now().After(deadline) {
				return failed("old process", "pane %s closed but pid %d is still running after %s", snap.Pane, snap.PID, d.PIDGone)
			}
		}
	}
	ev := bus.SwapEvent{Role: role, FromProvider: snap.Provider, By: self, Reason: reason, OldPID: snap.PID, OldPane: snap.Pane}
	var kept []string
	if forget || pop {
		// A plain retire forgets the episode it read; --forget the one it was told.
		ep := snap.TS
		if forget {
			ep = episode
		}
		res, err := b.ForgetIfClear(ctx, role, own, bus.RetiredAs{PID: snap.PID, PIDStart: snap.PIDStart, Episode: ep})
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
	}
	if err := b.RetireSwap(ctx, ev); err != nil {
		keepIntent = true // the begin reads as interrupted; the next swap or retire records it
		return fail("closed (pane %s), but the ledger was not written: %v; check agentbus swaps", snap.Pane, err)
	}
	if len(kept) > 0 {
		return fail("closed (pane %s, pid %d gone), but its registration is kept: %s; "+
			"the dispatcher escalates what waits and a new spawn of %s reads it from its kept group "+
			"(agentbus agents forget %s --force ends it by hand)", snap.Pane, snap.PID, strings.Join(kept, "; "), role, role)
	}
	if closed {
		fmt.Fprintf(out, "retired %s: already closed (pane %s and pid %d gone), unregistered (its consumer group kept)\n", role, snap.Pane, snap.PID)
		return 0
	}
	if forget || pop {
		fmt.Fprintf(out, "retired %s: pane %s closed, pid %d gone, unregistered (its consumer group kept)\n", role, snap.Pane, snap.PID)
		return 0
	}
	fmt.Fprintf(out, "retired %s: pane %s closed, pid %d gone\n", role, snap.Pane, snap.PID)
	return 0
}

// runSwaps lists the swap ledger, newest first, and the roles on a fallback.
func runSwaps(ctx context.Context, b *bus.Bus, args []string, out, errw io.Writer) int {
	args, role := extractFlag(args, "--role")
	args, asJSON := extractBool(args, "--json")
	if len(args) != 0 || (role != "" && !bus.ValidName(role)) {
		fmt.Fprintln(errw, "usage: agentbus swaps [--role <role>] [--json]")
		return 2
	}
	log, err := b.SwapLog(ctx, role, 50)
	if err != nil {
		fmt.Fprintf(errw, "swaps: %v\n", err)
		return 1
	}
	fbs, err := b.Fallbacks(ctx)
	if err != nil {
		fmt.Fprintf(errw, "swaps: %v\n", err)
		return 1
	}
	live := map[string]bool{}
	for _, e := range log {
		if _, seen := live[e.Role]; !seen {
			ok, _ := b.Swapping(ctx, e.Role)
			live[e.Role] = ok
		}
	}
	if asJSON {
		return writeJSON(out, errw, map[string]any{"swaps": log, "fallback": fbs})
	}
	for r, f := range fbs {
		if role == "" || r == role {
			fmt.Fprintf(out, "fallback %-12s on %s (%s) since %s, own %s (%s)\n", r, f.Profile, f.Provider,
				time.UnixMilli(f.Since).Local().Format("Mon 15:04"), f.OwnProfile, f.OwnProvider)
		}
	}
	lastSeen := map[string]bool{}
	for _, e := range log {
		ev := e.Event
		if ev == bus.SwapBegin && !lastSeen[e.Role] && !live[e.Role] {
			ev = "interrupted"
		}
		if e.Event != bus.SwapWouldRelaunch { // a shadow's record, not an end (bus.Interrupted)
			lastSeen[e.Role] = true
		}
		at := ""
		if ms, _, ok := strings.Cut(e.ID, "-"); ok {
			var n int64
			fmt.Sscan(ms, &n)
			at = time.UnixMilli(n).Local().Format("01-02 15:04")
		}
		line := fmt.Sprintf("%s  %-12s %-11s %s -> %s", at, e.Role, ev, dash(e.FromProfile), dash(e.ToProfile))
		if e.By != "" {
			line += "  by " + e.By
		}
		if e.Reason != "" {
			line += "  (" + e.Reason + ")"
		}
		if e.Error != "" {
			line += "  " + e.Error
		}
		if e.Kind != "" {
			line += "  [" + e.Kind + "]"
		}
		switch e.Forgot {
		case "true":
			line += "  forgot"
		case "false":
			line += "  kept"
		}
		fmt.Fprintln(out, line)
	}
	if len(log) == 0 && len(fbs) == 0 {
		fmt.Fprintln(out, "no swap recorded")
	}
	return 0
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeJSON(out, errw io.Writer, v any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(errw, "%v\n", err)
		return 1
	}
	return 0
}
