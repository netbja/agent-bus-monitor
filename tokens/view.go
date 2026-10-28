package tokens

import (
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The readers' rules, shared by agentbus tokens and busmon's U page, so the
// two never disagree: a project's days from {p}:tokens, and its agents today
// with a reason for every unknown.

// ProjectDays sums {p}:tokens rows ("day|agent|provider") per day.
func ProjectDays(rows map[string]bus.TokenRow) map[string]bus.TokenRow {
	out := map[string]bus.TokenRow{}
	for k, v := range rows {
		day, _, _ := strings.Cut(k, "|")
		s := out[day]
		s.In, s.Write, s.Read, s.Out, s.N = s.In+v.In, s.Write+v.Write, s.Read+v.Read, s.Out+v.Out, s.N+v.N
		out[day] = s
	}
	return out
}

// LastDays are the n local days ending at now, oldest first.
func LastDays(now time.Time, loc *time.Location, n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[n-1-i] = now.In(loc).AddDate(0, 0, -i).Format("2006-01-02")
	}
	return out
}

// AgentRow is one agent's tokens today; In and Out nil = unknown, with Reason.
type AgentRow struct {
	Agent    string `json:"agent"`
	Provider string `json:"provider,omitempty"`
	In       *int64 `json:"in"`  // input + cache writes
	Out      *int64 `json:"out"` // output
	Reason   string `json:"reason,omitempty"`
}

// AgentRows are every registered agent and every agent with tokens today,
// the most tokens first. known is whether {p}:tokens exists (reason says why
// not). An agent whose client gives no counted session reads unknown with
// the reason; 0 only for a proved session with nothing today.
func AgentRows(rows map[string]bus.TokenRow, known bool, reason string, agents map[string]bus.AgentSnapshot, today string) []AgentRow {
	byAgent := map[string]*AgentRow{}
	for k, v := range rows {
		parts := strings.SplitN(k, "|", 3)
		if len(parts) != 3 || parts[0] != today {
			continue
		}
		a := byAgent[parts[1]]
		if a == nil {
			in, out := int64(0), int64(0)
			a = &AgentRow{Agent: parts[1], Provider: parts[2], In: &in, Out: &out}
			byAgent[parts[1]] = a
		}
		*a.In += v.In + v.Write
		*a.Out += v.Out
	}
	for name, s := range agents {
		if byAgent[name] != nil {
			continue
		}
		a := &AgentRow{Agent: name, Provider: s.Provider}
		switch {
		case !known:
			a.Reason = reason
		case s.Client == "codex" || s.Provider == "openai":
			a.Reason = "Codex session not proved"
		case s.Client == "kimi" || s.Provider == "moonshot":
			a.Reason = "Kimi logs give no token counts"
		case s.PID == 0 || s.IdentSession == "":
			a.Reason = "no session proved with its process"
		default:
			in, out := int64(0), int64(0)
			a.In, a.Out = &in, &out
		}
		byAgent[name] = a
	}
	out := make([]AgentRow, 0, len(byAgent))
	for _, a := range byAgent {
		out = append(out, *a)
	}
	total := func(a AgentRow) int64 {
		if a.In == nil {
			return -1
		}
		return *a.In + *a.Out
	}
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := total(out[i]), total(out[j]); ti != tj {
			return ti > tj
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}
