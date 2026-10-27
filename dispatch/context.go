package dispatch

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// The context pass (context-alert, ca1) publishes each agent's context fill
// to {p}:context: the reading agentbus statusline recorded for the agent's
// own session. Only a verified snapshot's session is read (a pid proved by
// status or bind): a snapshot anyone could write never points the
// dispatcher at another session's file.

// ContextConfig is the context pass's policy; a nil Read turns it off.
type ContextConfig struct {
	Read  func(session string) (quota.ContextReading, bool)
	Every time.Duration // how often the readings are published
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
	for name, s := range snaps {
		if s.PID == 0 || s.Session == "" {
			continue // unverified, or no session to read
		}
		if r, ok := c.Read(s.Session); ok {
			fills[name] = bus.ContextFill{Pct: r.Pct, ObservedAt: r.ObservedAt, Session: s.Session, At: now.UnixMilli()}
		}
	}
	if err := l.b.FencedPutContext(ctx, l.token, fills); err != nil {
		return err
	}
	l.contextAt = time.Now()
	return nil
}
