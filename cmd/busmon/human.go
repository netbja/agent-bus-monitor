package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// defaultHuman is the sender when no name is given: busmon's operator is a
// person piloting the agents, never an agent (not the CLI's default sender).
const defaultHuman = "pilot"

// humanIdentity is the bus name busmon writes as: --as, else AGENT_BUS_HUMAN,
// else "pilot". It must be a valid bus name (lowercase).
func humanIdentity(flag, env string) (string, error) {
	name := flag
	if name == "" {
		name = env
	}
	if name == "" {
		return defaultHuman, nil
	}
	if !bus.ValidName(name) {
		return "", fmt.Errorf("invalid name %q: lowercase letters, digits, _ or -, starting with a letter", name)
	}
	return name, nil
}

// displayName capitalises a bus name for people: "bernard" -> "Bernard".
func displayName(name string) string {
	if name == "" {
		return ""
	}
	if c := name[0]; c >= 'a' && c <= 'z' {
		return string(c-'a'+'A') + name[1:]
	}
	return name
}

// humanLabel is how the header shows the operator: "♙ Bernard [human]", with
// "· pilot" while they hold the pilot lease.
func humanLabel(self, pilot string) string {
	role := "human"
	if pilot != "" && pilot == self {
		role = "human · pilot"
	}
	return GlyphHuman + " " + displayName(self) + " [" + role + "]"
}

// agentNamesOf are the registered agents the panel shows, the person
// excepted: with showAll false the @all targets. @all reaches the agents the
// panel shows: a dormant one (a team that was replaced, say) is not sent
// directives it will never read.
func agentNamesOf(agents map[string]*agentState, pilot, self string, showAll bool, now time.Time) []string {
	visible, _ := visibleAgents(agents, pilot, showAll, now)
	names := make([]string, 0, len(visible))
	for _, n := range visible {
		if n != self && agents[n].registered {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
