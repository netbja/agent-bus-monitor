package main

import (
	"fmt"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
)

// defaultHuman is the sender when no name is given: busmon's operator is a
// person piloting the agents, never an agent (the old default was "hermes").
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

// agentNames lists the project's known agents (sorted), the operator
// excluded: the targets of @all.
func (u *ui) agentNames() []string {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	names := make([]string, 0, len(u.st.agents))
	for n := range u.st.agents {
		if n != u.self {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
