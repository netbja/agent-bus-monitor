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
// done, sends the escalation target (master) one waking directive per
// episode to run `agentbus retire <role> --forget --episode <ms>`. It closes
// and types nothing new: retire re-judges the whole of §2 at the act.

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
}

// PopRetireText is the directive master gets for a done pop.
func PopRetireText(role string, episode int64, idle time.Duration) string {
	return fmt.Sprintf("%s has finished: no task, gate, ask or message left, idle %s. End it: agentbus retire %s --forget --episode %d --reason \"work done\"",
		role, idle.Round(time.Minute), role, episode)
}

// PopClosedText is the directive master gets for a pop closed by hand
// (pop-closed-by-hand): its process and pane are gone, nothing waits for it.
func PopClosedText(role string, episode int64) string {
	return fmt.Sprintf("%s was closed (its process and pane are gone) and nothing is left for it: no task, gate, ask or message. Unregister it: agentbus retire %s --forget --episode %d --reason \"closed\"",
		role, role, episode)
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
	if cannot := l.recipientCannotAct(a); cannot != "" {
		l.logOnce("pop-kept:"+a, map[string]any{"agent": a, "action": "pop-kept", "reason": "done, but " + cannot})
		return nil
	}
	episode := PopEpisode(f.Snap)
	text := PopRetireText(a, episode, now.Sub(time.UnixMilli(episode)))
	if f.Closed {
		text = PopClosedText(a, episode)
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
