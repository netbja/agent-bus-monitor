package main

import "github.com/netbja/agent-bus-monitor/bus"

// connectEntryOf is how this agentbus came to the broker, for the user
// Connect logs in as while broker.acl is on (broker-hardening step 4b): the
// services (service run, the holder service) as dispatcher; setup, backup and
// restore as admin, the person's; every other command as the CLI, by
// identity and ancestry. holder drain is the person's or master's command,
// not the service.
func connectEntryOf(args []string) bus.ACLEntry {
	switch {
	case len(args) == 0:
	case args[0] == "setup", args[0] == "backup", args[0] == "restore":
		return bus.ACLEntryAdmin
	case args[0] == "service" && len(args) >= 2 && args[1] == "run":
		return bus.ACLEntryService
	case args[0] == "holder" && !(len(args) >= 2 && args[1] == "drain"):
		return bus.ACLEntryService
	}
	return bus.ACLEntryCLI
}
