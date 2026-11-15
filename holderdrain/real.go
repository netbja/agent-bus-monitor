package holderdrain

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// Defaults of a production drain.
const (
	DefaultProofWait = 3 * time.Minute
	DefaultProofPoll = time.Second
)

// NewDeps is the drain of project on host (its holders, the current build
// being current), the bus b, and gc (the service gc of the old builds).
func NewDeps(project string, host termhost.Host, b *bus.Bus, current string, gc func() error) Deps {
	profiles := map[string]string{} // each agent's client, from its terminal (the look judges with it)
	rt := &dispatch.HostRuntime{Host: host, Project: project, Client: func(agent string) string { return profiles[agent] }}
	return Deps{
		Project: project,
		Host:    host,
		Old:     func(t termhost.Term) bool { return t.Build != current },
		Inspect: func(ctx context.Context, t termhost.Term) dispatch.Inspection {
			profiles[t.Name] = t.Profile
			return rt.Inspect(ctx, string(t.ID))
		},
		Declared: func(ctx context.Context) (map[string]string, error) {
			snaps, err := b.Agents(ctx)
			if err != nil {
				return nil, err
			}
			out := map[string]string{}
			for n, s := range snaps {
				out[n] = s.State
			}
			return out, nil
		},
		Resume:    b.ResumeOf,
		Proved:    ProvedIn(host, b.Agents),
		Event:     func(ctx context.Context, e bus.TermEvent) error { _, err := b.AppendEvent(ctx, e); return err },
		GC:        gc,
		Sleep:     time.Sleep,
		Now:       time.Now,
		Window:    dispatch.DefaultCPUWindow + time.Second,
		ProofWait: DefaultProofWait,
		ProofPoll: DefaultProofPoll,
	}
}

// ProvedIn is the relaunch-close proof on a holder: the agent's snapshot
// names a verified process (pid and its start time) that runs in the new
// terminal: its program or one of its descendants, as the holder reports
// them. The old terminal's process can never be in the new one's tree.
func ProvedIn(host termhost.Host, agents func(context.Context) (map[string]bus.AgentSnapshot, error)) func(context.Context, string, termhost.Term) (bool, error) {
	return func(ctx context.Context, agent string, t termhost.Term) (bool, error) {
		snaps, err := agents(ctx)
		if err != nil {
			return false, err
		}
		s, ok := snaps[agent]
		if !ok || s.PID <= 0 || s.PIDStart == 0 {
			return false, nil
		}
		if s.PID == t.PID && s.PIDStart == t.PIDStart {
			return true, nil
		}
		procs, err := host.Processes(ctx, t.ID)
		if err != nil {
			return false, err
		}
		for _, p := range procs {
			if p.PID == s.PID && p.Start == s.PIDStart {
				return true, nil
			}
		}
		return false, nil
	}
}
