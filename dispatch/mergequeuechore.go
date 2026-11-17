package dispatch

import (
	"context"
	"time"
)

// MergeQueueConfig is the merge queue's chore (merge-queue spec §3c): a pass
// every 5 minutes, only while this dispatcher holds its lease; the pass gets
// the lease token for its fenced write to {p}:mergequeue. A delivery marks the
// queue due (Due, merge-popup-setting: the person's choice A), and the chore
// runs a pass at its next check, every Poll, so a PR is a row in For you
// within seconds of its delivery.
type MergeQueueConfig struct {
	Every time.Duration // 5 min when 0
	Pass  func(ctx context.Context, token string) error
	// Due reads the last delivery's mark (0: none); a mark that differs from
	// the one the last pass saw runs a pass. nil: the 5-minute pass only.
	Due  func(ctx context.Context) (int64, error)
	Poll time.Duration // how often Due is read; 5 s when 0
}

func (c MergeQueueConfig) on() bool { return c.Pass != nil }

// mergeQueueLoop runs beside the reconciliation: a forge's slowness never
// delays a wake.
func (l *loop) mergeQueueLoop(ctx context.Context) {
	every, poll := l.cfg.MergeQueue.Every, l.cfg.MergeQueue.Poll
	if every <= 0 {
		every = 5 * time.Minute
	}
	if poll <= 0 {
		poll = 5 * time.Second
	}
	due := func() int64 {
		if l.cfg.MergeQueue.Due == nil {
			return 0
		}
		v, err := l.cfg.MergeQueue.Due(ctx)
		if err != nil {
			return 0
		}
		return v
	}
	seen := due() // the mark this pass answers
	l.mergeQueuePass(ctx)
	last := time.Now()
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		mark := due()
		if time.Since(last) < every && (mark == 0 || mark == seen) {
			continue
		}
		seen = mark
		l.mergeQueuePass(ctx)
		last = time.Now()
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
