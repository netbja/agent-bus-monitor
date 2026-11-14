package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A pop's end (spec docs/superpowers/specs/2026-11-03-pop-auto-retire-design.md):
// the dispatcher judges every registered pop with PopDone and, once one is
// done, either ends it itself (End set: pop-retire-auto, decision
// 2026-11-13-dispatcher-ends-done-pop) and tells the escalation target
// (master) with one notice that wakes nobody, or sends master one waking
// directive per episode to run `agentbus retire <role> --forget --episode
// <ms>`. Either way the act re-judges the whole of §2.

// PopRetireConfig is the pass's switch and delay.
type PopRetireConfig struct {
	On    bool          // dispatch.pop_retire and a non-zero delay
	After time.Duration // dispatch.pop_retire_after (condition 9)
	// Missions lists the mission attempts in progress on an agent; nil reads
	// as unknown, so no pop is ever judged done without it.
	Missions func(ctx context.Context, agent string) ([]string, error)
	// ProcStart reads a pid's start time; nil never judges a pop closed
	// (pop-closed-by-hand).
	ProcStart func(pid int) (uint64, error)
	// End, when set, ends a done pop as `retire --forget` does, every write
	// fenced by token (the service's popEnd); nil: master is told to run it.
	End func(ctx context.Context, token, role string, episode int64) PopEnd
}

// PopEnd is what End did: Ended; Wait, nothing done and nobody told (a turn
// running, a wake being typed: the next pass judges again); or Failed, once
// it began, which wakes master with the reason.
type PopEnd struct {
	Ended  bool
	Detail string // Ended: what was closed and forgotten
	Wait   string
	Failed string
}

// PopEndedText is the notice master gets (wake=no) once the dispatcher ended
// a pop itself.
func PopEndedText(role string, episode int64, detail string) string {
	return fmt.Sprintf("%s was ended by the dispatcher (episode %d): it had finished, no task, gate, ask or message left; %s.", role, episode, detail)
}

// PopEndFailedText is the directive master gets when the dispatcher could
// not end a done pop: why, and the command that ends it by hand.
func PopEndFailedText(role string, episode int64, why string) string {
	return fmt.Sprintf("The dispatcher could not end %s, which has finished: %s. End it by hand: agentbus retire %s --forget --episode %d --reason \"work done\"",
		role, why, role, episode)
}

// PopRetireText is the directive master gets for a done pop.
func PopRetireText(role string, episode int64, idle time.Duration) string {
	return fmt.Sprintf("%s has finished: no task, gate, ask or message left, idle %s. End it: agentbus retire %s --forget --episode %d --reason \"work done\"",
		role, idle.Round(time.Minute), role, episode)
}

// PopClosedText is the directive master gets for a pop closed by hand
// (pop-closed-by-hand): its process and pane are gone, nothing waits for it.
// A hand-off it declared is named first, to be read before the end
// (reviewer-codex): no /clear will ever take it.
func PopClosedText(role string, episode int64, handoffReport string) string {
	first := ""
	if handoffReport != "" {
		first = fmt.Sprintf(" It declared a hand-off: read it first, agentbus reports %s.", handoffReport)
	}
	return fmt.Sprintf("%s was closed (its process and pane are gone) and nothing is left for it: no task, gate, ask or message.%s Unregister it: agentbus retire %s --forget --episode %d --reason \"closed\"",
		role, first, role, episode)
}

// popRetirePass runs after the pause check (a paused project ends nobody).
func (l *loop) popRetirePass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	c := l.cfg.PopRetire
	if !c.On {
		return nil
	}
	nowMS, err := l.b.ServerNowMS(ctx)
	if err != nil {
		return err
	}
	now := time.UnixMilli(nowMS)
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	// A mark whose agent is gone (retired and forgotten) is dropped.
	for field := range marks {
		if a, ok := strings.CutPrefix(field, "popdone:"); ok {
			if _, registered := snaps[a]; !registered {
				if err := l.b.FencedQuotaDel(ctx, l.token, field); err != nil {
					return err
				}
			}
		}
	}
	names := make([]string, 0, len(snaps))
	for a, s := range snaps {
		// The cheap part of §2 first, from the snapshot alone: a pop, declared
		// idle or done long enough (or whose process is gone: closed by hand,
		// pop-closed-by-hand), not already told for this episode.
		if s.Tier != "pop" || now.Sub(time.UnixMilli(s.TS)) < c.After ||
			marks[bus.PopDoneMark(a)] == strconv.FormatInt(PopEpisode(&s), 10) {
			continue
		}
		if (s.Auto != "" || (s.State != "idle" && s.State != "done")) && !ProcGone(&s, c.ProcStart) {
			continue
		}
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		if err := l.popRetire(ctx, a, now); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "pop retire", err)
		}
	}
	return nil
}

func (l *loop) popRetire(ctx context.Context, a string, now time.Time) error {
	c := l.cfg.PopRetire
	f := ReadPopFacts(ctx, l.b, l.rt, a, PopReadOptions{Now: now, RetireAfter: c.After, Missions: c.Missions, ProcStart: c.ProcStart})
	if f.Snap == nil {
		return nil
	}
	if !f.Held {
		f.Held = l.heldBy(f.Snap)
	}
	done, why := PopDone(f)
	if !done {
		l.logOnce("pop-kept:"+a, map[string]any{"agent": a, "action": "pop-kept", "reason": why})
		return nil
	}
	episode := PopEpisode(f.Snap)
	if c.End != nil { // the dispatcher ends it itself (pop-retire-auto)
		return l.popEnd(ctx, a, episode)
	}
	if cannot := l.recipientCannotAct(a); cannot != "" {
		l.logOnce("pop-kept:"+a, map[string]any{"agent": a, "action": "pop-kept", "reason": "done, but " + cannot})
		return nil
	}
	text := PopRetireText(a, episode, now.Sub(time.UnixMilli(episode)))
	if f.Closed {
		text = PopClosedText(a, episode, f.HandoffReport)
	}
	sent, err := l.b.FencedCmdOnce(ctx, l.token, bus.PopDoneMark(a), strconv.FormatInt(episode, 10), dispatchFrom,
		l.cfg.EscalateTo, bus.CmdDirective, text, false)
	if err != nil {
		return err
	}
	if sent {
		l.log(map[string]any{"agent": a, "action": "pop-done", "to": l.cfg.EscalateTo, "episode": episode})
	}
	return nil
}

// popEnd ends a done pop through End: a notice (wake=no) once ended, the
// waking directive with the reason once failed, nothing on a wait. The
// episode's mark keeps either from being sent twice, and a failed episode
// is not tried again (master ends it by hand).
func (l *loop) popEnd(ctx context.Context, a string, episode int64) error {
	r := l.cfg.PopRetire.End(ctx, l.token, a, episode)
	var text string
	noWake := false
	switch {
	case r.Wait != "":
		l.logOnce("pop-kept:"+a, map[string]any{"agent": a, "action": "pop-wait", "reason": r.Wait})
		return nil
	case r.Ended:
		text, noWake = PopEndedText(a, episode, r.Detail), true
	default:
		text = PopEndFailedText(a, episode, r.Failed)
	}
	sent, err := l.b.FencedCmdOnce(ctx, l.token, bus.PopDoneMark(a), strconv.FormatInt(episode, 10), dispatchFrom,
		l.cfg.EscalateTo, bus.CmdDirective, text, noWake)
	if err != nil {
		return err
	}
	if sent {
		action := "pop-ended"
		if !r.Ended {
			action = "pop-end-failed"
		}
		l.log(map[string]any{"agent": a, "action": action, "to": l.cfg.EscalateTo, "episode": episode, "reason": r.Failed})
	}
	return nil
}
