package main

import (
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// settingRefusal is busmon's own check of a value before config.Set, for
// what the key's rule in config cannot see. attach.prefix (P1-8): a prefix
// from the setting is not --prefix, so agentbus attach refuses it for a
// client unless that client's profile records it free (attachGate). The
// setting is refused unless it is free for every client the project's roles
// run on, their own profiles and fallbacks (a swapped agent is attached too),
// naming each other client and the keys free for it (foureyes). "" accepts.
func settingRefusal(getenv func(string) string, key string, v any) string {
	if key != "attach.prefix" {
		return ""
	}
	prefix, _ := v.(string)
	clients, why := projectClients(getenv)
	if why != "" {
		return why
	}
	profiles := agentstate.Profiles()
	var not []string
	for _, name := range clients {
		p := profiles[name]
		if p.KeyStatus(prefix) == "free" {
			continue
		}
		var free []string
		for k, s := range p.Keys {
			if s == "free" {
				free = append(free, k)
			}
		}
		sort.Strings(free)
		if len(free) == 0 {
			not = append(not, name+" (no key verified free)")
		} else {
			not = append(not, name+" (free for it: "+strings.Join(free, ", ")+")")
		}
	}
	if len(not) == 0 {
		return ""
	}
	return prefix + " is not verified free for " + strings.Join(not, ", ") + ": agentbus attach would refuse it there"
}

// projectClients are the client profiles (agentstate's names, a client with
// none by its rolecfg name) the project's roles run on, own and fallback
// profiles, sorted; why says the roles cannot be read.
func projectClients(getenv func(string) string) ([]string, string) {
	const unreadable = "roles.toml cannot be read: which clients attach serves is unknown"
	path, err := rolecfg.ManifestPath(getenv)
	if err != nil {
		return nil, unreadable
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		return nil, unreadable + " (" + err.Error() + ")"
	}
	seen := map[string]bool{}
	var bad error
	add := func(p rolecfg.Profile, err error) {
		if err != nil {
			bad = err
			return
		}
		name := holder.ClientProfile(p.Client)
		if name == "" {
			name = p.Client
		}
		seen[name] = true
	}
	for _, role := range m.RoleNames() {
		add(m.Resolve(role))
		for _, fb := range m.Fallbacks(role) {
			add(m.ResolveWith(role, fb))
		}
	}
	if bad != nil { // defensive (Load validates every profile): a role whose client is unknown is never judged as none
		return nil, "roles.toml: " + bad.Error()
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, ""
}
