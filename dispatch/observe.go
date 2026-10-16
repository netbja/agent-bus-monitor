package dispatch

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ObserveEvery is how often the services' dispatcher looks at every
// registered pane to publish what it sees ({p}:observed, v0.14.3).
const ObserveEvery = 15 * time.Second

// observePass inspects each agent with a verified pane, at most once per
// cfg.Observe, and replaces {p}:observed. It only reads panes: it never types,
// never changes a declared state, and a pane whose identity does not match
// the snapshot is not observed (it may be another process).
func (l *loop) observePass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	if l.cfg.Observe <= 0 || time.Since(l.observedAt) < l.cfg.Observe {
		return nil
	}
	l.observedAt = time.Now()
	now, err := l.b.ServerTime(ctx)
	if err != nil {
		return err
	}
	obs := map[string]Observation{}
	for a, s := range snaps {
		if s.Pane == "" || s.PID == 0 {
			continue
		}
		snap := s
		in := l.rt.Inspect(ctx, snap.Pane)
		if in.Err != nil || in.Gone || !identityOK(&snap, in) {
			continue
		}
		obs[a] = Observation{Busy: in.State == "working" || in.Busy, State: in.State, At: now.UnixMilli()}
	}
	return l.b.FencedPutObserved(ctx, l.token, obs)
}

// Observation is bus.Observation, named here for the loop.
type Observation = bus.Observation
