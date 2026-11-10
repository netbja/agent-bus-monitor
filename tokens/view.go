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
// the reason; 0 only for a proved session (a pid and a valid session id,
// the dispatcher's own rule) with nothing today.
func AgentRows(rows map[string]bus.TokenRow, known bool, reason string, agents map[string]bus.AgentSnapshot, today string) []AgentRow {
	byAgent := map[string]*AgentRow{}
	perProvider := map[string]map[string]int64{} // agent -> provider -> tokens today
	for k, v := range rows {
		parts := strings.SplitN(k, "|", 3)
		if len(parts) != 3 || parts[0] != today {
			continue
		}
		a := byAgent[parts[1]]
		if a == nil {
			in, out := int64(0), int64(0)
			a = &AgentRow{Agent: parts[1], In: &in, Out: &out}
			byAgent[parts[1]] = a
			perProvider[parts[1]] = map[string]int64{}
		}
		*a.In += v.In + v.Write
		*a.Out += v.Out
		perProvider[parts[1]][parts[2]] += v.In + v.Write + v.Out
	}
	for name, a := range byAgent {
		a.Provider = topProvider(perProvider[name], agents[name].Provider)
	}
	for name, s := range agents {
		if byAgent[name] != nil {
			continue
		}
		a := &AgentRow{Agent: name, Provider: s.Provider}
		switch {
		case !known:
			a.Reason = reason
		case (s.Client == "codex" || s.Provider == "openai") && (s.PID == 0 || !ValidSessionID(s.IdentSession)):
			a.Reason = "Codex session not proved" // proved, it reads no adapter below (reviewer-codex)
		case s.Client == "kimi" || s.Provider == "moonshot":
			a.Reason = "Kimi logs give no token counts"
		case !countedClient(s): // only Claude Code's sessions are counted: never a 0 nobody counted
			a.Reason = "no token count for " + or(s.Client, s.Provider) + " (no adapter)"
		case s.PID == 0 || !ValidSessionID(s.IdentSession): // no proof, or an id no transcript path can carry
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

// countedClient: the tokens pass counts this agent's sessions (Claude Code's
// transcripts, tokens.ClaudeScan). An agent that names neither a client nor a
// provider is judged by its proof alone, as before (idle-view-providers).
func countedClient(s bus.AgentSnapshot) bool {
	switch {
	case s.Client != "":
		return s.Client == "claude-code"
	case s.Provider != "":
		return s.Provider == "anthropic"
	}
	return true
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// topProvider is the provider an agent with rows today shows, by a rule that
// never depends on map order: the most tokens today; on a tie, the agent's
// snapshot provider when it is among the tied; else the lexically first.
func topProvider(byProvider map[string]int64, snapshot string) string {
	best, bestN := "", int64(-1)
	for p, n := range byProvider {
		switch {
		case n > bestN:
			best, bestN = p, n
		case n < bestN:
		case p == snapshot:
			best = p
		case best != snapshot && p < best:
			best = p
		}
	}
	return best
}
