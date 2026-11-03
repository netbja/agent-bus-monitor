package dispatch

import (
	"context"
	"time"
)

// NotifyConfig is the notifications chore (notifications spec, "Where it
// runs"): a pass every 30 s, only while this dispatcher holds its lease; the
// pass gets the lease token for its fenced writes to {p}:notified.
type NotifyConfig struct {
	Every time.Duration // 30 s when 0
	Pass  func(ctx context.Context, token string) error
}

func (c NotifyConfig) on() bool { return c.Pass != nil }

// notifyLoop runs beside the reconciliation: a send's HTTP never delays a wake.
func (l *loop) notifyLoop(ctx context.Context) {
	every := l.cfg.Notify.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l.notifyPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// notifyPass calls the pass once if this dispatcher holds the lease; a
// failure is logged once until it changes.
func (l *loop) notifyPass(ctx context.Context) {
	held, err := l.b.HoldsDispatch(ctx, l.token)
	if err != nil || !held {
		return
	}
	if err := l.cfg.Notify.Pass(ctx, l.token); err != nil {
		l.logOnce("notify", map[string]any{"action": "notify-failed", "error": err.Error()})
		return
	}
	l.logOnce("notify", map[string]any{"action": "notify-ok"})
}
