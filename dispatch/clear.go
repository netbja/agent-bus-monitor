package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Auto-clear (lifecycle spec, part B): after an agent declared its hand-off
// (agentbus handoff), the dispatcher may type the literal /clear into its
// pane, the one exception to "it types only the wake line", under the rule
// below; then the ordinary wake brings the agent back to its hand-off.

// clearClients are the clients whose /clear semantics are known: it ends the
// conversation and keeps the process, argv and environment, starting a new
// session (which the SessionStart hook proves, part D).
var clearClients = map[string]bool{"claude-code": true}

// ClearFacts is everything DecideClear judges, read by the caller.
type ClearFacts struct {
	On         bool              // AGENT_BUS_AUTO_CLEAR
	Record     bus.Handoff       // the agent's declared hand-off
	Snap       bus.AgentSnapshot // its current snapshot
	Paused     bool              // {p}:pause
	Swapping   bool              // a swap or reload intent
	Held       bool              // its provider is held: a cleared agent would wake into a held quota
	OpenAsk    bool
	Gates      []string // open challenges on either side
	Armed      bool
	WakeState  string // its wake record's state
	Marked     bool   // already cleared once for this record's session
	Inspection Inspection
}

// ClearDecision: Clear, or why not; WaitGates when only gates hold it.
type ClearDecision struct {
	Clear     bool
	Reason    string
	WaitGates []string
}

// DecideClear is part B's rule, cheapest first. IdentSession alone never
// authorises a clear: the record must stand for the snapshot's verified pid
// and start, and the pane must show that same process, ready.
func DecideClear(f ClearFacts) ClearDecision {
	no := func(why string) ClearDecision { return ClearDecision{Reason: why} }
	switch {
	case !f.On:
		return no("auto-clear is off")
	case !clearClients[f.Snap.Client]:
		return no("client " + f.Snap.Client + " has no known /clear")
	}
	if void, why := f.Record.VoidFor(f.Snap, true); void {
		return no("hand-off void: " + why)
	}
	switch {
	case f.Paused:
		return no("paused")
	case f.Swapping:
		return no("a swap or reload is in progress")
	case f.Held:
		return no("its provider is held")
	case f.OpenAsk:
		return no("it has an open ask")
	case len(f.Gates) > 0:
		return ClearDecision{Reason: "a review gate is open", WaitGates: f.Gates}
	case f.Snap.State != "idle" || f.Snap.TS <= f.Record.At:
		return no("not idle since its hand-off")
	case f.Armed:
		return no("armed")
	case f.WakeState == "prompting":
		return no("a wake is being typed")
	case f.Marked:
		return no("already cleared for this session")
	}
	in := f.Inspection
	switch {
	case in.Err != nil:
		return no("inspection failed: " + in.Err.Error())
	case !identityOK(&f.Snap, in):
		return no("its verified process is not the pane's")
	case in.Focused:
		return no("the pane is focused")
	case in.State == "blocked":
		return no("the pane is blocked on a prompt")
	case in.Busy || !in.Ready:
		return no("the pane is not ready")
	}
	return ClearDecision{Clear: true}
}

// ClearConfig is the auto-clear's switch and bounds.
type ClearConfig struct {
	On     bool          // AGENT_BUS_AUTO_CLEAR=1
	Proof  time.Duration // how long the new session may take to be proved (30 s)
	Notify func(ctx context.Context, title, body string) error
}

// clearEnd and clearNote are the {p}:quota once-marks of a clear's outcome:
// exactly one terminal message per cleared session (resume, rebuild or
// escalation), and one notice beside a rebuild.
func clearEnd(agent string) string  { return "clear-end:" + agent }
func clearNote(agent string) string { return "clear-note:" + agent }
func clearWait(agent string) string { return "clear-wait:" + agent }

// clearPass proves the clears in flight, then clears the agents whose
// declared hand-off passes DecideClear. A lost lease is ErrNotHolder.
func (l *loop) clearPass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	l.cleared = map[string]bool{}
	recs, err := l.b.Handoffs(ctx)
	if err != nil {
		return err
	}
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	now, err := l.b.ServerNowMS(ctx)
	if err != nil {
		return err
	}
	pending := map[string]bool{}
	for field, v := range marks {
		a, ok := strings.CutPrefix(field, "clear:")
		if !ok || !bus.ValidName(a) {
			continue
		}
		c, ok := bus.ParseClearMark(v)
		if !ok {
			continue
		}
		pending[a] = true
		rec, has := recs[a]
		if err := l.proveClear(ctx, a, c, rec, has, snaps[a], marks, now); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "clear proof", err)
		}
	}
	if !l.cfg.Clear.On || len(recs) == 0 {
		return nil
	}
	names := make([]string, 0, len(recs))
	for a := range recs {
		if !pending[a] {
			names = append(names, a)
		}
	}
	sort.Strings(names)
	var asks []bus.Ask
	var armed map[string]string
	for _, a := range names {
		snap, ok := snaps[a]
		if !ok {
			continue
		}
		if asks == nil {
			if asks, err = l.b.Asks(ctx); err != nil {
				return err
			}
			if armed, err = l.b.ArmedAgents(ctx); err != nil {
				return err
			}
		}
		if err := l.tryClear(ctx, a, recs[a], snap, asks, armed); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "clear", err)
		}
	}
	return nil
}

// tryClear judges one record and, when the rule passes, reserves, re-inspects
// and types /clear.
func (l *loop) tryClear(ctx context.Context, a string, rec bus.Handoff, snap bus.AgentSnapshot, asks []bus.Ask, armed map[string]string) error {
	f := ClearFacts{On: true, Record: rec, Snap: snap, Held: l.heldBy(&snap)}
	if void, _ := rec.VoidFor(snap, true); void || !clearClients[snap.Client] {
		return nil // judged by DecideClear's cheap rules without reading the pane
	}
	var err error
	if f.Swapping, err = l.b.Swapping(ctx, a); err != nil {
		return err
	}
	for _, k := range asks {
		f.OpenAsk = f.OpenAsk || k.From == a && k.Open() > 0
	}
	if f.Gates, err = l.b.ReviewsOf(ctx, a); err != nil {
		return err
	}
	_, f.Armed = armed[a]
	if w, ok, err := l.b.Wake(ctx, a); err != nil {
		return err
	} else if ok {
		f.WakeState = w.State
	}
	f.Inspection = l.rt.Inspect(ctx, snap.Pane)
	d := DecideClear(f)
	if len(d.WaitGates) > 0 {
		return l.clearWaits(ctx, a, rec, d.WaitGates)
	}
	if !d.Clear {
		return nil
	}
	at, ok, err := l.b.FencedReserveClear(ctx, l.token, a, rec)
	if err != nil || !ok {
		return err
	}
	// Re-inspect right before typing: ready, unfocused, the same process.
	f.Inspection = l.rt.Inspect(ctx, snap.Pane)
	if d := DecideClear(f); !d.Clear {
		l.log(map[string]any{"agent": a, "action": "clear-aborted", "reason": d.Reason})
		return l.b.FencedQuotaDel(ctx, l.token, bus.ClearMark(a)) // nothing typed: the next pass judges again
	}
	l.cleared[a] = true
	if err := l.rt.Clear(ctx, snap.Pane); err != nil {
		return err // typed or not, unknown: the proof settles it, or escalates once
	}
	l.log(map[string]any{"agent": a, "action": "clear", "session": rec.Session, "reserved_at": at})
	return nil
}

// clearWaits tells the escalation target, once per record, which gates hold
// a declared hand-off, so it can close or answer them.
func (l *loop) clearWaits(ctx context.Context, a string, rec bus.Handoff, gates []string) error {
	to := l.cfg.EscalateTo
	if to == a || !bus.ValidName(to) {
		return nil
	}
	note := fmt.Sprintf("auto-clear: %s declared a hand-off (report %s) but waits on open review gates: %s. Close them (agentbus gate close) or answer them.",
		a, rec.Report, strings.Join(gates, "; "))
	_, err := l.b.FencedCmdOnce(ctx, l.token, clearWait(a), rec.Session, dispatchFrom, to, bus.CmdDirective, note, true)
	return err
}

// proveClear settles one clear in flight: proved (same process, a new proved
// session, nothing written since the reservation) sends the resume directive;
// a turn that ran around the clear sends the rebuild directive; no proof in
// time escalates once. Each outcome is one message per session.
func (l *loop) proveClear(ctx context.Context, a string, c bus.ClearReservation, rec bus.Handoff, has bool,
	snap bus.AgentSnapshot, marks map[string]string, now int64) error {
	sess, at, report := c.Session, c.At, c.Report
	sameRecord := has && rec.Session == sess
	if marks[clearEnd(a)] == sess { // settled: forget the clear once its record is gone
		if !sameRecord {
			return l.b.FencedQuotaDel(ctx, l.token, bus.ClearMark(a))
		}
		return nil
	}
	if sameRecord {
		proved := snap.PID == rec.PID && snap.PIDStart == rec.PIDStart && snap.IdentSession != "" && snap.IdentSession != sess
		// A status after the reservation's boundary means a turn ran: told
		// apart by stream id, not by millisecond (reviewer-codex).
		wrote := false
		if proved {
			var err error
			if wrote, err = l.b.StatusAfter(ctx, a, c.StatusID); err != nil {
				return err
			}
		}
		switch {
		case proved && !wrote:
			text := fmt.Sprintf("context cleared: re-run your boot steps (load your role's skills, read the memory index), then read your hand-off: agentbus reports %s", report)
			if _, err := l.b.FencedCmdOnce(ctx, l.token, clearEnd(a), sess, dispatchFrom, a, bus.CmdDirective, text, false); err != nil {
				return err
			}
			l.log(map[string]any{"agent": a, "action": "clear-proved"})
			if err := l.b.FencedDeleteHandoff(ctx, l.token, a, rec); err != nil {
				return err
			}
			return l.b.FencedQuotaDel(ctx, l.token, bus.ClearMark(a))
		case proved: // a status after the reservation: a turn ran
			return l.clearRebuild(ctx, a, sess, report)
		case now-at > l.clearProof().Milliseconds():
			return l.clearEscalate(ctx, a, sess)
		}
		return nil
	}
	// The record is gone or replaced: the agent wrote after the reservation.
	return l.clearRebuild(ctx, a, sess, report)
}

func (l *loop) clearProof() time.Duration {
	if l.cfg.Clear.Proof > 0 {
		return l.cfg.Clear.Proof
	}
	return 30 * time.Second
}

func (l *loop) clearRebuild(ctx context.Context, a, sess, report string) error {
	text := fmt.Sprintf("your context was cleared after a turn your hand-off does not cover: read agentbus reports %s, check your last status and reports, then publish a fresh hand-off if work is in flight", report)
	if _, err := l.b.FencedCmdOnce(ctx, l.token, clearEnd(a), sess, dispatchFrom, a, bus.CmdDirective, text, false); err != nil {
		return err
	}
	if to := l.cfg.EscalateTo; to != a && bus.ValidName(to) {
		note := fmt.Sprintf("auto-clear: %s ran a turn its hand-off does not cover around the clear; told to rebuild from its reports.", a)
		if _, err := l.b.FencedCmdOnce(ctx, l.token, clearNote(a), sess, dispatchFrom, to, bus.CmdDirective, note, true); err != nil {
			return err
		}
	}
	l.log(map[string]any{"agent": a, "action": "clear-rebuild"})
	return l.b.FencedQuotaDel(ctx, l.token, bus.ClearMark(a))
}

// clearEscalate: no proof in time. The mark stays (no retry for that
// session); one notice to the escalation target, and one to the desktop.
func (l *loop) clearEscalate(ctx context.Context, a, sess string) error {
	to := l.cfg.EscalateTo
	if to == a || !bus.ValidName(to) {
		to = a // nobody else to tell: the agent gets it when it drains
	}
	note := fmt.Sprintf("auto-clear of %s not confirmed in %s; look at its pane.", a, l.clearProof())
	sent, err := l.b.FencedCmdOnce(ctx, l.token, clearEnd(a), sess, dispatchFrom, to, bus.CmdDirective, note, true)
	if err != nil || !sent {
		return err
	}
	l.log(map[string]any{"agent": a, "action": "clear-escalated"})
	if n := l.cfg.Clear.Notify; n != nil {
		if err := n(ctx, "agentbus "+l.b.Project(), note); err != nil {
			l.logError(a, "desktop notify", err)
		}
	}
	return nil
}
