package dispatchsvc

import (
	"context"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// agentsLost is the agents-lost check (cleaning-up-agents §10, step 1c):
// every agent's presence (bus.Presence) from its snapshot, the holder's
// live terminals, what it owes (bus.ReadOwes) and roles.toml's required.
// No holder read judges nothing (every agent runs); a roles file not read
// leaves required unknown, which fails closed (lost, "unknown: roles").
func (s *stallReader) agentsLost(ctx context.Context) (health.Outcome, error) {
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		return health.EvaluateAgentsLost(health.AgentsLostInput{ReadErr: "agents unavailable"}), nil
	}
	var live map[string]bool
	if s.liveTerms != nil {
		live = s.liveTerms(ctx)
	}
	var man *rolecfg.Manifest
	if path, err := rolecfg.ManifestPath(s.getenv); err == nil && path != "" {
		man, _ = rolecfg.Load(path)
	}
	in := health.AgentsLostInput{Agents: presenceAgents(snaps, live, s.b.ReadOwes(ctx, snaps), man, config.SelfRepairOn(config.Machine(s.getenv)))}
	return health.EvaluateAgentsLost(in), nil
}

// presenceAgents judges each agent of snaps, sorted by name.
func presenceAgents(snaps map[string]bus.AgentSnapshot, live map[string]bool, owes map[string]bus.Owes, man *rolecfg.Manifest, selfRepair bool) []health.PresenceAgent {
	out := make([]health.PresenceAgent, 0, len(snaps))
	for n, s := range snaps {
		o, ok := owes[n]
		if !ok {
			o.Unknown = []string{"owes"}
		}
		in := bus.PresenceIn{Pane: s.Pane, TermsRead: live != nil, Live: live[n], SelfRepair: selfRepair, Owes: o}
		if man == nil {
			in.Owes.Unknown = append([]string{"roles"}, o.Unknown...)
		} else {
			in.Required = man.RoleRequired(n)
		}
		p, why := bus.Presence(in)
		out = append(out, health.PresenceAgent{Name: n, Presence: p, Reason: why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
