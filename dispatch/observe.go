package dispatch

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ObserveEvery is how often the services' dispatcher looks at every
// registered pane to publish what it sees ({p}:observed, v0.14.3).
const ObserveEvery = 15 * time.Second

// observeBudget bounds the inspections and time reads of one pass (the
// final write adds at most observeWrite): panes not reached in time wait for
// the next pass, so observing never starves the wake reconciliation.
const observeBudget = 5 * time.Second

// observeWrite bounds the publication that ends a pass.
const observeWrite = 2 * time.Second

// observePass inspects each agent with a verified pane, at most once per
// cfg.Observe counted from the end of the previous pass, and replaces
// {p}:observed. It only reads panes: it never types, never changes a declared
// state, and a pane whose identity does not match the snapshot is not
// observed (it may be another process). Each observation carries the Redis
// time taken right after its own inspection.
func (l *loop) observePass(ctx context.Context, snaps map[string]bus.AgentSnapshot) error {
	if l.cfg.Observe <= 0 || time.Since(l.observedAt) < l.cfg.Observe {
		return nil
	}
	defer func() { l.observedAt = time.Now() }()
	pctx, cancel := context.WithTimeout(ctx, observeBudget)
	defer cancel()
	// The previous pass (from Redis, so a restart keeps the episodes) carries
	// `since` forward and stands in for a pane whose inspection fails, until
	// it ages out (v0.15 §13).
	prev, err := l.b.Observed(pctx)
	if err != nil {
		prev = l.obs
	}
	start, err := l.b.ServerTime(pctx)
	if err != nil {
		return err
	}
	obs := map[string]Observation{}
	keep := func(a string) {
		if p, ok := prev[a]; ok && p.Fresh(start.UnixMilli()) {
			obs[a] = p
		}
	}
	for a, s := range snaps {
		if s.Pane == "" || s.PID == 0 {
			continue
		}
		if pctx.Err() != nil {
			keep(a)
			continue
		}
		snap := s
		in := l.rt.Inspect(pctx, snap.Pane)
		if in.Err != nil || in.Gone || !identityOK(&snap, in) {
			if !in.Gone {
				// A failed look, or a pane whose process is not the agent's at
				// this instant, is no new fact: the last reading stands until it
				// ages out. Its pid says whose it was; busmon shows it only for
				// that process. A gone pane is a fact: nothing is kept.
				keep(a)
			}
			continue
		}
		now, err := l.b.ServerTime(pctx)
		if err != nil {
			if pctx.Err() != nil {
				keep(a)
				continue // out of budget: publish what was observed
			}
			return err
		}
		o := Observation{Busy: in.State == "working" || in.Busy, State: in.State, At: now.UnixMilli(),
			Since: now.UnixMilli(), PID: snap.PID}
		if p, ok := prev[a]; ok && p.State == o.State && p.PID == o.PID && p.Since != 0 {
			o.Since = p.Since
		}
		obs[a] = o
	}
	l.obs = obs
	// The write has its own short bound, so a spent budget still publishes
	// the partial snapshot and a slow broker cannot hold the pass.
	wctx, wcancel := context.WithTimeout(ctx, observeWrite)
	defer wcancel()
	return l.b.FencedPutObserved(wctx, l.token, obs)
}

// Observation is bus.Observation, named here for the loop.
type Observation = bus.Observation
