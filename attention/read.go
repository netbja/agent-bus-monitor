package attention

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// Reader is the broker reads Items needs (status-summary §3.2: "Inputs lists
// every read they need"); *bus.Bus is one.
type Reader interface {
	Asks(ctx context.Context) ([]bus.Ask, error)
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Observed(ctx context.Context) (map[string]bus.Observation, error)
	OpenChallenges(ctx context.Context, agent string) (map[string]string, error)
	Health(ctx context.Context) (map[string]health.Record, error)
	ServerTime(ctx context.Context) (time.Time, error)
	MergeQueue(ctx context.Context) (bus.MergeQueueRecord, error)
	DispatchAlive(ctx context.Context) (bool, error)
	ServiceStates(ctx context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error)
	Recent(ctx context.Context, kinds []string, n int) ([]bus.Event, map[string]string, error)
}

// ReadOpts: the person's identity ("" none: no gate is the person's) and
// dispatch.stuck_after (0 off).
type ReadOpts struct {
	Person     string
	StuckAfter time.Duration
}

// cmdScan is how many cmd entries are read for the replies that answer a
// waiting agent (busmon's openThreadScan).
const cmdScan = 1000

// Read is one read of Inputs from the broker, as busmon's poll reads them,
// and the reads that failed, by name (asks, agents, observed, gates, health,
// clock, merge-queue, dispatch, cmd). A failed read leaves its part empty: the
// caller says "?" for what it fed, never "none".
func Read(ctx context.Context, r Reader, o ReadOpts) (Inputs, map[string]error) {
	failed := map[string]error{}
	fail := func(name string, err error) bool {
		if err != nil {
			failed[name] = err
		}
		return err != nil
	}
	in := Inputs{Person: o.Person, Agents: map[string]Agent{}, MergeRead: true}
	asks, err := r.Asks(ctx)
	if !fail("asks", err) {
		in.Asks = asks
	}
	snaps, err := r.Agents(ctx)
	fail("agents", err)
	observed, obsErr := r.Observed(ctx)
	fail("observed", obsErr)
	cmds, _, cmdErr := r.Recent(ctx, []string{"cmd"}, cmdScan)
	fail("cmd", cmdErr)
	open := map[string]bus.Ask{}
	for _, a := range OpenAsks(asks) {
		open[a.From] = a
	}
	for n, s := range snaps {
		a := Agent{StuckAfter: o.StuckAfter}
		if s.TS != 0 { // launched and bound, no status declared yet: no state
			a.State, a.Message, a.StateAt = s.State, s.Message, time.UnixMilli(s.TS)
		}
		if ask, ok := open[n]; ok {
			a.OpenAsk = ask.Open()
		}
		if obsErr == nil {
			ob, ok := observed[n]
			a.Obs = Observe(ob, ok, s.PID)
		}
		if cmdErr == nil && !a.StateAt.IsZero() {
			if ms := bus.WaitAnswered(n, a.StateAt.UnixMilli(), a.OpenAsk, asks, cmds); ms > 0 {
				a.AnsweredAt = time.UnixMilli(ms)
			}
		}
		if m, err := r.OpenChallenges(ctx, n); !fail("gates", err) {
			a.Gates = m
		}
		in.Agents[n] = a
	}
	if _, isAgent := snaps[o.Person]; o.Person != "" && !isAgent {
		if m, err := r.OpenChallenges(ctx, o.Person); !fail("gates", err) {
			in.PersonGates = m
		}
	}
	if h, err := r.Health(ctx); !fail("health", err) {
		in.Health = h
	}
	if now, err := r.ServerTime(ctx); !fail("clock", err) {
		in.HealthNow = now
	}
	if rec, err := r.MergeQueue(ctx); err != nil {
		failed["merge-queue"] = err
		in.MergeErr = "the merge queue could not be read"
	} else {
		in.Merge = rec
	}
	alive, aliveErr := r.DispatchAlive(ctx)
	_, recs, svcErr := r.ServiceStates(ctx)
	if !fail("dispatch", aliveErr) && !fail("dispatch", svcErr) {
		legacy := "down"
		if alive {
			legacy = "up"
		}
		rec, has := recs["dispatch"]
		in.Dispatch = ServiceFact(rec, has, legacy, time.Now())
	}
	return in, failed
}
