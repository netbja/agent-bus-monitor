package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// `agentbus retire <role> [--reason <text>] [--now]` (v0.15.1 §11.1): close
// one agent's tab, with the swap's checks and ledger, and launch nothing.
// `agentbus shutdown` is the team-wide stop; this is for one role, such as
// architect once its recommendation is delivered.

const retireUsage = "usage: agentbus retire <role> [--reason <text>] [--now]"

func runRetire(ctx context.Context, b *bus.Bus, d swapDeps, self string, args []string, out, errw io.Writer) int {
	args, now := extractBool(args, "--now")
	args, reason := extractFlag(args, "--reason")
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(errw, retireUsage)
		return 2
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
	if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
		return fail("its recorded process (pid %d) is not running any more: nothing to close", snap.PID)
	}
	if pane := d.Env("HERDR_PANE_ID"); pane != "" && pane == snap.Pane {
		return fail("an agent cannot retire itself: run it from master's pane or a terminal")
	}
	if code := openWork(ctx, b, role, fail); code != 0 {
		return code
	}
	if !now {
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
	if bad, ev, err := b.Interrupted(ctx, role); err == nil && bad {
		_ = b.LogSwap(ctx, bus.SwapEvent{Role: role, Event: bus.SwapFailed, ToProfile: ev.ToProfile, By: self,
			Error: "interrupted: the swap begun at " + ev.ID + " never finished"})
	}
	if err := b.BeginSwap(ctx, role, bus.SwapIntent{By: self, OldPID: snap.PID, OldStart: snap.PIDStart},
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
	if err := d.ClosePane(ctx, snap.Pane); err != nil {
		return failed("close", "%v; nothing was closed", err)
	}
	for deadline := d.Now().Add(d.PIDGone); ; d.Sleep(200 * time.Millisecond) {
		if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
			break
		}
		if d.Now().After(deadline) {
			return failed("old process", "pane %s closed but pid %d is still running after %s", snap.Pane, snap.PID, d.PIDGone)
		}
	}
	if err := b.RetireSwap(ctx, bus.SwapEvent{Role: role, FromProvider: snap.Provider, By: self, Reason: reason,
		OldPID: snap.PID, OldPane: snap.Pane}); err != nil {
		keepIntent = true // the begin reads as interrupted; the next swap or retire records it
		return fail("closed (pane %s), but the ledger was not written: %v; check agentbus swaps", snap.Pane, err)
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
		lastSeen[e.Role] = true
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
