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
	DefaultWakeWait  = 10 * time.Second
	DefaultProofWait = 3 * time.Minute
	DefaultProofPoll = time.Second
)

// NewDeps is the drain of project on host (its holders, the build current
// being the current one), with the bus b, sockets (every holder socket file
// of the project) and gc (the service gc of the old builds).
func NewDeps(project string, host termhost.Host, b *bus.Bus, current string, sockets func() ([]string, error), gc func() error) Deps {
	profiles := map[string]string{} // each agent's client, from its terminal (the look judges with it)
	rt := &dispatch.HostRuntime{Host: host, Project: project, Client: func(agent string) string { return profiles[agent] }}
	return Deps{
		Project: project,
		Host:    host,
		Bus:     b,
		Old:     func(t termhost.Term) bool { return t.Build != current },
		Sockets: sockets,
		Inspect: func(ctx context.Context, t termhost.Term) dispatch.Inspection {
			profiles[t.Name] = t.Profile
			return rt.Inspect(ctx, string(t.ID))
		},
		GC:        gc,
		Sleep:     time.Sleep,
		Now:       time.Now,
		Window:    dispatch.DefaultCPUWindow + time.Second,
		WakeWait:  DefaultWakeWait,
		ProofWait: DefaultProofWait,
		ProofPoll: DefaultProofPoll,
	}
}
