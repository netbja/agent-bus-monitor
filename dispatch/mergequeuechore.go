package dispatch

import (
	"context"
	"time"
)

// MergeQueueConfig is the merge queue's chore (merge-queue spec §3c): a pass
// every 5 minutes, only while this dispatcher holds its lease; the pass gets
// the lease token for its fenced write to {p}:mergequeue.
type MergeQueueConfig struct {
	Every time.Duration // 5 min when 0
	Pass  func(ctx context.Context, token string) error
}

func (c MergeQueueConfig) on() bool { return c.Pass != nil }

// mergeQueueLoop runs beside the reconciliation: a forge's slowness never
// delays a wake.
func (l *loop) mergeQueueLoop(ctx context.Context) {
	every := l.cfg.MergeQueue.Every
	if every <= 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.mergeQueuePass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// mergeQueuePass calls the pass once if this dispatcher holds the lease; a
// failure is logged once until it changes.
func (l *loop) mergeQueuePass(ctx context.Context) {
	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil || !held {
		return
	}
	if err := l.cfg.MergeQueue.Pass(ctx, l.token); err != nil {
		l.logOnce("mergequeue", map[string]any{"action": "mergequeue-failed", "error": err.Error()})
		return
	}
	l.logOnce("mergequeue", map[string]any{"action": "mergequeue-ok"})
}
