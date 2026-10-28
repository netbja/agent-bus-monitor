package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"

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

// contextMark and contextNoticeMark are the {p}:quota fields of an agent's
// episode; their value is the session.
func contextMark(agent string) string       { return "context:" + agent }
func contextNoticeMark(agent string) string { return "context-notice:" + agent }

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
	for _, a := range names {
		f := l.ctxFills[a]
		if f.Session == "" || (c.MaxAge > 0 && f.At-f.ObservedAt > c.MaxAge.Milliseconds()) {
			continue // unknown or aged: it neither alerts nor ends an episode
		}
		if f.Pct < c.Alert {
			if marks == nil {
				m, err := l.b.QuotaMarks(ctx)
				if err != nil {
					return err
				}
				marks = m
			}
			var ended []string
			for _, k := range []string{contextMark(a), contextNoticeMark(a)} {
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
		}
	}
	return nil
}

// alertContext sends agent a's episode its directive, notice and desktop
// notification; each entry is written with its mark in one fenced script.
func (l *loop) alertContext(ctx context.Context, a string, f bus.ContextFill) error {
	to := l.cfg.EscalateTo
	directive := fmt.Sprintf("context %.0f%%: at your next stopping point commit WIP, publish your hand-off with agentbus report %s --to %s, then declare it: agentbus handoff %s --report <its id>", f.Pct, a, to, a)
	sent, err := l.b.FencedCmdOnce(ctx, l.token, contextMark(a), f.Session, dispatchFrom, a, bus.CmdDirective, directive, false)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil // the agent is being replaced: its successor starts a new session
	}
	if err != nil {
		return err
	}
	if sent {
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
