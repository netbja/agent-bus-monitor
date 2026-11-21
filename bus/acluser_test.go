package bus

import "testing"

// Step 4b (spec "Each process picks its user by its entry point"): the user
// a process logs in as once broker.acl is on, from its entry point, its
// AGENT_BUS_AGENT and the registered agent among its ancestors ("" none).
func TestACLUserFor(t *testing.T) {
	for _, c := range []struct {
		entry              ACLEntry
		identity, ancestor string
		want               string
		refused            bool
	}{
		{ACLEntryService, "", "", ACLDispatcher, false},
		{ACLEntryService, "coder", "coder", ACLDispatcher, false}, // the services' env carries no identity; the entry decides
		{ACLEntryAdmin, "", "", ACLAdmin, false},
		{ACLEntryAdmin, "coder", "", "", true},  // an agent's environment
		{ACLEntryAdmin, "", "coder", "", true},  // an agent's process tree
		{ACLEntryAdmin, "master", "", "", true}, // master is an agent too
		{ACLEntryCLI, "", "", ACLBusmon, false}, // the person's CLI
		{ACLEntryCLI, "coder", "coder", ACLAgent, false},
		{ACLEntryCLI, "", "coder", ACLAgent, false}, // an agent that unset its identity
		{ACLEntryCLI, "master", "", ACLMaster, false},
		{ACLEntryCLI, "master", "master", ACLMaster, false},
		{ACLEntryCLI, "master", "coder", ACLAgent, false}, // an agent cannot claim master
		{ACLEntryCLI, "", "master", ACLMaster, false},     // master's own tree
		{ACLEntryBusmon, "", "", ACLBusmon, false},
		{ACLEntryBusmon, "coder", "", ACLAgent, false},
		{ACLEntryBusmon, "", "coder", ACLAgent, false},
		{ACLEntryBusmon, "master", "", ACLMaster, false},
		{ACLEntryBusmon, "master", "coder", ACLAgent, false},
	} {
		got, err := ACLUserFor(c.entry, c.identity, c.ancestor)
		if (err != nil) != c.refused || got != c.want {
			t.Errorf("%v identity %q ancestor %q: %q %v, want %q refused %v", c.entry, c.identity, c.ancestor, got, err, c.want, c.refused)
		}
	}
}
