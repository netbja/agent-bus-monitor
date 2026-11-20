package dispatchsvc

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// wireRuntime gives the dispatcher's runtime its bus-backed background facts:
// the agents' hook records (background-work-visible step 1). Config calls it;
// a test pins it.
func wireRuntime(h *dispatch.HostRuntime, b *bus.Bus) {
	h.Hooks = bgHooks(b)
}

// bgHooks is HostRuntime.Hooks from the agents' hook records
// (background-work-visible step 1): what an agent's Stop or SubagentStop last
// listed, for its process pid only (a record of another process is not
// read). Sub-agents and the other tasks are counted apart; a scheduled
// wakeup is no running work. No record (never reported, dropped at
// SessionStart, past its hour): not read, so a client whose hooks are
// authoritative reads unknown, never none.
func bgHooks(b *bus.Bus) func(agent string, pid int) (agentstate.HookFacts, bool) {
	return func(agent string, pid int) (agentstate.HookFacts, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r, ok, err := b.BgTasks(ctx, agent)
		if err != nil || !ok || pid <= 0 || r.PID != pid {
			return agentstate.HookFacts{}, false
		}
		var h agentstate.HookFacts
		for _, t := range r.Tasks {
			if t.Type == "subagent" {
				h.Subagents++
			} else {
				h.Tasks++
			}
		}
		return h, true
	}
}
