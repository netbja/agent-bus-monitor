package main

import (
	"fmt"
	"io"
)

// checkProfiles stops up before any service, workspace, tab or pane when an
// agent tab names a profile roles.toml no longer allows: started, it would
// run unchecked by checkClients and the trust gate. Its own step, not behind
// checkClients' lookups (foureyes). A tab that launches no agent, or a role
// roles.toml does not have, keeps today's skip (agent-launch refuses it).
func checkProfiles(d upDeps, tpl upTemplate, path string, stderr io.Writer) bool {
	for _, t := range tpl.Tabs {
		if _, _, err := tabProfile(t.Command, d.Env); err != nil {
			fmt.Fprintf(stderr, "agentbus up: tab %s: %v: choose another in %s\n", t.Name, err, path)
			return false
		}
	}
	return true
}
