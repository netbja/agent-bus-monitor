package usage

import (
	"fmt"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Moved from cmd/agentbus unchanged (dispatchsvc P6): each agent's usage
// from its proved session's transcript.

// CollectAgentUsage turns each agent's proved session into a usage snapshot
// read straight from its transcript. Only the session recorded with the
// agent's identity proof (IdentSession, with a pid) is read, as for
// {p}:context (usage-session): an unverified status that names another
// session moves Session, never the transcript read here. Agents with no proved
// session (any non-Claude-Code peer, an unverified one, or one that has not
// published a status yet) are skipped with a note rather than silently dropped.
func CollectAgentUsage(agents map[string]bus.AgentSnapshot, projectsRoot string) (map[string]bus.UsageSnapshot, []string) {
	out := map[string]bus.UsageSnapshot{}
	var notes []string
	for _, name := range sortedAgents(agents) {
		a := agents[name]
		if a.PID == 0 || !bus.ValidSessionID(a.IdentSession) {
			notes = append(notes, fmt.Sprintf("%s: no session proved with its process (skipped)", name))
			continue
		}
		path, err := FindTranscript(projectsRoot, a.IdentSession)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		t, err := ReadTranscript(path)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: reading transcript: %v", name, err))
			continue
		}
		if t.Empty() {
			notes = append(notes, fmt.Sprintf("%s: transcript has no assistant turn yet", name))
			continue
		}
		out[name] = bus.UsageSnapshot{
			Model:    t.Model,
			Ctx:      humanTokens(t.ContextTokens),
			Provider: AnthropicProvider,
			Source:   "transcript",
			TS:       t.TS,
		}
	}
	return out, notes
}

func sortedAgents(m map[string]bus.AgentSnapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// humanTokens renders a raw context count. Deliberately not a percentage: that
// needs a per-model context-limit table, which goes stale every model release.
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM ctx", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk ctx", n/1_000)
	default:
		return fmt.Sprintf("%d ctx", n)
	}
}
