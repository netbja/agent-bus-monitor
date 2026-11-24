package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The context alert (context-alert ca2): an agent whose context fill reaches
// ContextConfig.Alert is told to hand off before its client compacts. Once
// per episode: the episode is the session proved with the agent's process,
// and a fresh reading below the threshold ends it (its marks are deleted), so
// the next rise alerts again. The directive to the agent, the wake=no notice
// to the escalation target and the desktop notification follow the fills of
// the last publication; a paused project is not judged (reconcile stops
// before), and an unknown or aged fill never acts, nor ends an episode.

// The episode's marks are bus.ContextMark and its kin (bus/context.go).
var (
	contextMark         = bus.ContextMark
	contextNoticeMark   = bus.ContextNoticeMark
	contextEscalateMark = bus.ContextEscalateMark
	contextPersonMark   = bus.ContextPersonMark
	contextEpisodeMarks = bus.ContextEpisodeMarks
)

// contextAlerts judges the last publication once; a lost lease is ErrNotHolder.
func (l *loop) contextAlerts(ctx context.Context) error {
	c := l.cfg.Context
	if !l.ctxFresh {
		return nil
	}
	l.ctxFresh = false
	if c.Alert <= 0 {
		return nil
	}
	names := make([]string, 0, len(l.ctxFills))
	for a := range l.ctxFills {
		names = append(names, a)
	}
	sort.Strings(names)
	var marks map[string]string
	loadMarks := func() error {
		if marks != nil {
			return nil
		}
		m, err := l.b.QuotaMarks(ctx)
		marks = m
		return err
	}
	esc := l.newEscalation(c)
	for _, a := range names {
		f := l.ctxFills[a]
		if f.Session == "" || (c.MaxAge > 0 && f.At-f.ObservedAt > c.MaxAge.Milliseconds()) {
			continue // unknown or aged: it neither alerts nor ends an episode
		}
		if f.Pct < c.Alert {
			if err := loadMarks(); err != nil {
				return err
			}
			if err := l.compactedNotice(ctx, a, f, marks, esc); err != nil {
				if errors.Is(err, bus.ErrNotHolder) {
					return err
				}
				// The episode stays: a later pass retries the notice
				// (reviewer-codex); it ends once sent or proved needless.
				l.logError(a, "context compacted", err)
				continue
			}
			var ended []string
			for _, k := range contextEpisodeMarks(a) {
				if _, ok := marks[k]; ok {
					ended = append(ended, k)
				}
			}
			if len(ended) > 0 {
				if err := l.b.FencedQuotaDel(ctx, l.token, ended...); err != nil {
					return err
				}
			}
			continue
		}
		if err := l.alertContext(ctx, a, f); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "context alert", err)
			continue
		}
		marks = nil // the alert may have written its marks: read them again
		if err := loadMarks(); err != nil {
			return err
		}
		if err := esc.judge(ctx, a, f, marks); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "context escalation", err)
		}
		if err := esc.compact(ctx, a, f, marks); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "auto-compact", err)
		}
	}
	return nil
}

// alertContext sends agent a's episode its directive, notice and desktop
// notification; each entry is written with its mark in one fenced script.
func (l *loop) alertContext(ctx context.Context, a string, f bus.ContextFill) error {
	to := l.cfg.EscalateTo
	directive := fmt.Sprintf("context %.0f%%: at your next stopping point commit WIP, publish your hand-off with agentbus report %s --to %s, then declare it: agentbus handoff %s --report <its id>", f.Pct, a, to, a)
	// The alert keeps its time (§3.1): the deadline counts from it.
	id, err := l.b.FencedCmdOnceAt(ctx, l.token, contextMark(a), f.Session, dispatchFrom, a, bus.CmdDirective, directive, false)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil // the agent is being replaced: its successor starts a new session
	}
	if err != nil {
		return err
	}
	if id != "" {
		l.log(map[string]any{"agent": a, "action": "context-alert", "pct": f.Pct})
		if n := l.cfg.Context.Notify; n != nil {
			if err := n(ctx, "agentbus "+l.b.Project(), fmt.Sprintf("%s context %.0f%%: told to hand off", a, f.Pct)); err != nil {
				l.logError(a, "desktop notify", err)
			}
		}
	}
	if a == to || !bus.ValidName(to) {
		return nil // the target is the agent itself: the directive says it all
	}
	note := fmt.Sprintf("context: %s at %.0f%%; told to commit WIP, report its hand-off to you and declare it (agentbus handoff).", a, f.Pct)
	_, err = l.b.FencedCmdOnce(ctx, l.token, contextNoticeMark(a), f.Session, dispatchFrom, to, bus.CmdDirective, note, true)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil
	}
	return err
}

// escalation is one pass's step 2 (context-escalation §3.2): its settings,
// and the hand-offs and snapshots read once, when first needed.
type escalation struct {
	l        *loop
	urgent   float64
	deadline time.Duration
	read     bool
	handoffs map[string]bus.Handoff
	snaps    map[string]bus.AgentSnapshot
}

// newEscalation applies the start rule: urgent is used only above the alert.
func (l *loop) newEscalation(c ContextConfig) *escalation {
	urgent := c.Urgent
	if urgent > 0 && urgent <= c.Alert {
		l.logOnce("context-urgent", map[string]any{"action": "context-urgent-off", "urgent": urgent, "alert": c.Alert,
			"reason": "dispatch.context_urgent is not above dispatch.context_alert"})
		urgent = 0
	}
	return &escalation{l: l, urgent: urgent, deadline: c.Deadline}
}

// standing reports whether agent a's hand-off stands (VoidFor false).
func (e *escalation) standing(ctx context.Context, a string) (bool, error) {
	if !e.read {
		h, err := e.l.b.Handoffs(ctx)
		if err != nil {
			return false, err
		}
		s, err := e.l.b.Agents(ctx)
		if err != nil {
			return false, err
		}
		e.handoffs, e.snaps, e.read = h, s, true
	}
	h, ok := e.handoffs[a]
	if !ok {
		return false, nil
	}
	snap, known := e.snaps[a]
	void, _ := h.VoidFor(snap, known)
	return !void, nil
}

// judge escalates agent a once in this episode when it is due: alerted, no
// hand-off standing, and the fill at urgent or the alert past the deadline;
// now is the publication's Redis time.
func (e *escalation) judge(ctx context.Context, a string, f bus.ContextFill, marks map[string]string) error {
	if marks[contextMark(a)] != f.Session || (marks[contextEscalateMark(a)] == f.Session && marks[contextPersonMark(a)] == f.Session) {
		return nil // not alerted in this episode, or escalated already
	}
	alertAt := bus.MarkAt(marks, contextMark(a))
	byFill := e.urgent > 0 && f.Pct >= e.urgent
	byTime := e.deadline > 0 && alertAt > 0 && f.At-alertAt >= e.deadline.Milliseconds()
	if !byFill && !byTime {
		return nil
	}
	if stands, err := e.standing(ctx, a); err != nil || stands {
		return err
	}
	l, c := e.l, e.l.cfg.Context
	since := "earlier"
	minutes := ""
	if alertAt > 0 {
		n := (f.At - alertAt) / 60000
		since = fmt.Sprintf("%d min ago", n)
		minutes = fmt.Sprintf(" after %d min", n)
	}
	to := l.cfg.EscalateTo
	if a != to && bus.ValidName(to) {
		directive := fmt.Sprintf("context: %s at %.0f%%, alerted %s at %.0f%%, no hand-off declared. Its client has no mid-turn channel yet: the person is told. Send it no new work; do not type into its pane. Once it declares its hand-off the dispatcher clears it.",
			a, f.Pct, since, c.Alert)
		_, err := l.b.FencedCmdOnce(ctx, l.token, contextEscalateMark(a), f.Session, dispatchFrom, to, bus.CmdDirective, directive, false)
		if errors.Is(err, bus.ErrSwapInProgress) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	notice := fmt.Sprintf("%s context %.0f%%, no hand-off%s: if you are at the desk, interrupt it (Esc) and tell it to hand off now.", a, f.Pct, minutes)
	sent, err := l.b.FencedNotifyOnceForPerson(ctx, l.token, contextPersonMark(a), f.Session, dispatchFrom, notice)
	if err != nil {
		return err
	}
	if sent {
		l.log(map[string]any{"agent": a, "action": "context-escalate", "pct": f.Pct, "by_fill": byFill, "by_time": byTime})
		if n := c.Notify; n != nil {
			if err := n(ctx, "agentbus "+l.b.Project(), fmt.Sprintf("%s context %.0f%%: no hand-off yet", a, f.Pct)); err != nil {
				l.logError(a, "desktop notify", err)
			}
		}
	}
	return nil
}

// compactedNotice (§3.5): an episode ending in the session it alerted, with
// no hand-off standing, whose transcript holds an automatic compaction after
// the alert, tells the escalation target once (wake=no): the agent lost its
// context without a hand-off. A fill lower in the same session with no such
// boundary (a window change) ends the episode silently, as does an episode
// with no alert time (an older build).
func (l *loop) compactedNotice(ctx context.Context, a string, f bus.ContextFill, marks map[string]string, esc *escalation) error {
	c, to := l.cfg.Context, l.cfg.EscalateTo
	alertAt := bus.MarkAt(marks, contextMark(a))
	if c.Compacted == nil || marks[contextMark(a)] != f.Session || alertAt == 0 || !bus.ValidName(to) {
		return nil
	}
	if stands, err := esc.standing(ctx, a); err != nil || stands {
		return err
	}
	pre, found := c.Compacted(esc.snaps[a].ClaudeConfigDir, f.Session, alertAt)
	if !found {
		return nil
	}
	note := fmt.Sprintf("context: %s was compacted by its client at %d tokens without a hand-off; it keeps working with a summary of its context. Check its current step before sending it more.", a, pre)
	episode := f.Session + "|" + strconv.FormatInt(alertAt, 10)
	_, err := l.b.FencedCmdOnce(ctx, l.token, bus.ContextCompactedMark(a), episode, dispatchFrom, to, bus.CmdDirective, note, true)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil
	}
	return err
}
