package dispatch

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// The context pass (context-alert, ca1) publishes each agent's context fill
// to {p}:context: the reading agentbus statusline recorded for the agent's
// own session. Only the session proved with the agent's process is read
// (IdentSession, set with a pid proof by status or bind): a status that only
// names a session never points the dispatcher at another session's file.

// ContextConfig is the context pass's policy; a nil Read turns it off.
type ContextConfig struct {
	Read  func(session string) (quota.ContextReading, bool)
	Every time.Duration // how often the readings are published
	// Alert is the fill, in percent, at which an agent is told to hand off
	// (context-alert ca2; 0 = off). MaxAge is the oldest reading that acts.
	Alert  float64
	MaxAge time.Duration
	// Notify shows a desktop notification (nil = none).
	Notify func(ctx context.Context, title, body string) error
}

// contextPass publishes the fills when due; a lost lease is ErrNotHolder.
func (l *loop) contextPass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	c := l.cfg.Context
	if c.Read == nil || (!l.contextAt.IsZero() && time.Since(l.contextAt) < c.Every) {
		return nil
	}
	now, err := l.b.ServerTime(ctx)
	if err != nil {
		return err
	}
	fills := map[string]bus.ContextFill{}
	alert := c.Alert
	for name, s := range snaps {
		if s.PID == 0 || s.IdentSession == "" {
			continue // unverified, or no session proved with its process
		}
		if r, ok := c.Read(s.IdentSession); ok {
			fills[name] = bus.ContextFill{Pct: r.Pct, ObservedAt: r.ObservedAt, Session: s.IdentSession, At: now.UnixMilli(), Alert: &alert}
		}
	}
	if err := l.b.FencedPutContext(ctx, l.token, fills); err != nil {
		return err
	}
	l.contextAt = time.Now()
	l.ctxFills, l.ctxFresh = fills, true
	return nil
}
