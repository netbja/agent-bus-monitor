package dispatch

import (
	"context"
	"errors"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
)

// handoffPass deletes the hand-off records that no longer stand (lifecycle
// spec, part A: the agent declared working since, or its process or proved
// session changed). A void record is never acted on; deleting it keeps the
// next declaration's flag honest. A lost lease is ErrNotHolder.
func (l *loop) handoffPass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	recs, err := l.b.Handoffs(ctx)
	if err != nil || len(recs) == 0 {
		return err
	}
	marks, err := l.b.QuotaMarks(ctx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(recs))
	for a := range recs {
		// A record under a clear in flight is the proof's to settle: the new
		// session it waits for would read here as "another session".
		if c, ok := bus.ParseClearMark(marks[bus.ClearMark(a)]); ok && c.Session == recs[a].Session {
			continue
		}
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		s, ok := snaps[a]
		void, why := recs[a].VoidFor(s, ok)
		if !void {
			continue
		}
		if err := l.b.FencedDeleteHandoff(ctx, l.token, a, recs[a]); err != nil {
			if errors.Is(err, bus.ErrNotHolder) {
				return err
			}
			l.logError(a, "handoff", err)
			continue
		}
		l.log(map[string]any{"agent": a, "action": "handoff-void", "reason": why})
	}
	return nil
}
