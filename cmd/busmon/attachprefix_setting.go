package main

import (
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/agentstate"
)

// attachProfiles are the clients' profiles the attach.prefix gate reads
// (agentstate's, embedded); tests replace them.
var attachProfiles = agentstate.Profiles

// settingRefusal is busmon's own check of a value before config.Set, for
// what the key's rule in config cannot see: attach.prefix is refused when a
// client's profile records it as bound (P1-8), the gate agentbus attach
// applies to that client's terminals, so the setting never names a key
// attach would refuse there. "" accepts.
func settingRefusal(key string, v any) string {
	if key != "attach.prefix" {
		return ""
	}
	prefix, _ := v.(string)
	var bound []string
	for name, p := range attachProfiles() {
		if p.KeyStatus(prefix) == "bound" {
			bound = append(bound, name)
		}
	}
	if len(bound) == 0 {
		return ""
	}
	sort.Strings(bound)
	return prefix + " is bound by " + strings.Join(bound, ", ") + ": agentbus attach refuses it there"
}
