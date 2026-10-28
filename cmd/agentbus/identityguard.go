package main

import (
	"fmt"
	"strings"
)

// identityGuard refuses (A8) a command that writes as the sender when it runs
// inside a herdr pane (HERDR_PANE_ID) with AGENT_BUS_AGENT unset: it would
// post as the default "hermes", the pane's agent losing its name (the lost
// master of 2026-10-27). Read-only commands still run; a command this table
// does not know counts as a write, so a new one fails safe. An explicit
// AGENT_BUS_AGENT, even "hermes", is an identity and never refused.
func identityGuard(getenv func(string) string, cmd string, rest []string) error {
	if getenv("HERDR_PANE_ID") == "" || getenv("AGENT_BUS_AGENT") != "" || readOnlyCommand(cmd, rest) {
		return nil
	}
	return fmt.Errorf("refusing to write as the default identity \"hermes\" from herdr pane %s: AGENT_BUS_AGENT is not set, "+
		"so this pane's agent would post under another name. Fix: relaunch the agent through its launcher "+
		"(agentbus up <project> --resume), or export AGENT_BUS_AGENT=<agent> in this pane", getenv("HERDR_PANE_ID"))
}

// readOnlyCommand reports a command (and subcommand) that writes nothing as
// the sender: a listing, a read, or one that names its subject itself.
func readOnlyCommand(cmd string, rest []string) bool {
	first := ""
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			first = a
			break
		}
	}
	switch cmd {
	case "reports", "thread", "verdicts", "asks", "swaps", "agents", "pane", "usage", "budget", "listen",
		"subscribe", "watch", // the agent is named on the command line
		"bind", "dispatch", "missions", // the launcher's and the services' own identities
		"answer", // a person (--as, AGENT_BUS_HUMAN), never the agent
		"roles":  // only doctor and adopt get here (main exits the others first, no bus); neither names a sender:
		// doctor --publish writes unauthored model verdicts, adopt writes the manifest
		return true
	case "gate":
		return first != "close"
	case "pilot":
		return first == "status"
	case "request", "board":
		return first == ""
	case "plan":
		return first == "" || first == "show"
	case "health":
		return first == "" || first == "list" || first == "detail" || first == "log"
	}
	return false
}
