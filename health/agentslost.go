package health

import (
	"fmt"
	"sort"
	"strings"
)

// agents-lost (cleaning-up-agents §10, spec
// docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md): every
// lost agent with its reason, those lost without a holder event too (a
// client crash, a pane closed by hand). The presence is bus.Presence's,
// judged by the caller; parked and stopped are never a fault.

// PresenceAgent is one agent's presence and, when lost, why.
type PresenceAgent struct {
	Name, Presence, Reason string
}

// AgentsLostInput is the check's evidence; ReadErr names a failed read.
type AgentsLostInput struct {
	Agents  []PresenceAgent
	ReadErr string
}

// EvaluateAgentsLost is OK with none lost, Warn listing them by name, and
// Unknown on a failed read.
func EvaluateAgentsLost(in AgentsLostInput) Outcome {
	if in.ReadErr != "" {
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	}
	var lost []string
	for _, a := range in.Agents {
		if a.Presence == "lost" {
			lost = append(lost, fmt.Sprintf("%s (%s)", a.Name, a.Reason))
		}
	}
	if len(lost) == 0 {
		return Outcome{Status: OK}
	}
	sort.Strings(lost)
	return Outcome{Status: Warn, Detail: truncateDetail(fmt.Sprintf("%d lost: %s", len(lost), strings.Join(lost, "; ")))}
}
