package dispatch

import (
	"context"
	"time"
)

// SelfRepairEvery is the self-repair pass's period: R1's grace (60 s) then
// holds at least three reads of a death (self-repair spec 4.1).
const SelfRepairEvery = 15 * time.Second

// SelfRepairConfig is the self-repair watcher's chore (spec
// 2026-11-09-self-repair-watcher-design.md, step 6): a pass every Every,
// only while this dispatcher holds its lease, given the lease token for its
// fenced ledger write. The pass lives with the service (cmd/agentbus):
// relaunch, which it reads through, imports this package.
type SelfRepairConfig struct {
	Every time.Duration // SelfRepairEvery when 0
	Pass  func(ctx context.Context, token string) error
}

func (c SelfRepairConfig) on() bool { return c.Pass != nil }

// selfRepairLoop runs beside the reconciliation: a slow herdr never delays
// a wake.
func (l *loop) selfRepairLoop(ctx context.Context) {
	every := l.cfg.SelfRepair.Every
	if every <= 0 {
		every = SelfRepairEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.selfRepairPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// selfRepairPass calls the pass once if this dispatcher holds the lease; a
// failure is logged once until it changes.
func (l *loop) selfRepairPass(ctx context.Context) {
	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil || !held {
		return
	}
	if err := l.cfg.SelfRepair.Pass(ctx, l.token); err != nil {
		l.logOnce("self-repair", map[string]any{"action": "self-repair-failed", "error": err.Error()})
		return
	}
	l.logOnce("self-repair", map[string]any{"action": "self-repair-ok"})
}
