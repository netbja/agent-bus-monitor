package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// senderIdentity is who a command writes as (P2-11): the agent
// (AGENT_BUS_AGENT), else the person's recorded identity (identity.as:
// AGENT_BUS_HUMAN, else config.toml), else "anonymous", which identityGuard
// refuses for a write. person is true for the person's identity. It never
// marks an entry human=yes: busmon alone writes those.
func senderIdentity(getenv func(string) string) (name string, person bool) {
	if a := agentIdentity(getenv); a != "" {
		return a, false
	}
	g := config.Machine(getenv).Get("identity.as")
	// "anonymous" is nobody here too, set as the person's name or not
	// (reviewer-codex on 76625f41).
	if s, ok := g.Value.(string); ok && !g.Invalid && bus.ValidName(s) && s != "anonymous" {
		return s, true
	}
	return "anonymous", false
}

// agentIdentity is AGENT_BUS_AGENT, "" when unset or "anonymous": that
// value is the same nobody as unset (the cron trigger exported it, and its
// directive arrived from anonymous, foureyes).
func agentIdentity(getenv func(string) string) string {
	if a := getenv("AGENT_BUS_AGENT"); a != "anonymous" {
		return a
	}
	return ""
}

// refuseAnonymousTarget refuses "anonymous" as an addressee (report --to,
// reply, cmd): it names nobody, and an answer sent there reaches no one.
func refuseAnonymousTarget(target string) error {
	if target != "anonymous" {
		return nil
	}
	return fmt.Errorf("refusing to address \"anonymous\": it is nobody, and nothing sent there is read. Address the agent or the person by name; "+
		"a person who sent as anonymous passes --as <name> where the command takes it, or sets %s=<name> (once: agentbus identity set <name>)",
		config.EnvName("identity.as"))
}

// identityGuard refuses (A8) a command that writes as the sender when it runs
// inside an agent's terminal (AGENTBUS_TERM_ID) with AGENT_BUS_AGENT unset: it
// would post as the default "anonymous", the terminal's agent losing its name (the lost
// master of 2026-10-27). Read-only commands still run; a command this table
// does not know counts as a write, so a new one fails safe. An explicit
// AGENT_BUS_AGENT is an identity and never refused, but "anonymous", which is
// the same nobody as unset (P2-11).
//
// Outside an agent's terminal (P2-11), a write with no agent and no person
// identity is refused too: it would arrive "from anonymous", and an answer
// to it would go nowhere. Inside a terminal the person's identity never
// stands in for the agent's (an inherited AGENT_BUS_HUMAN).
func identityGuard(getenv func(string) string, cmd string, rest []string) error {
	if agentIdentity(getenv) != "" || readOnlyCommand(cmd, rest) {
		return nil
	}
	if getenv("AGENTBUS_TERM_ID") == "" {
		if _, person := senderIdentity(getenv); person {
			return nil
		}
		why := "no identity"
		if g := config.Machine(getenv).Get("identity.as"); g.Source != "default" {
			why = "identity.as is set but invalid (" + g.Source + ")"
		}
		return fmt.Errorf("refusing to write as \"anonymous\": %s, so this would arrive from nobody and its answer go nowhere. "+
			"Fix: set %s=<your name> (once for this machine: agentbus identity set <name>), or AGENT_BUS_AGENT=<agent> for an agent",
			why, config.EnvName("identity.as"))
	}
	return fmt.Errorf("refusing to write as the default identity \"anonymous\" from terminal %s: AGENT_BUS_AGENT is not set (or is anonymous), "+
		"so this terminal's agent would post under another name. Fix: relaunch the agent through its launcher "+
		"(agentbus up <project> --resume), or export AGENT_BUS_AGENT=<agent> in this terminal", getenv("AGENTBUS_TERM_ID"))
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
	case "reports", "thread", "history", "verdicts", "asks", "swaps", "agents", "pane", "usage", "budget", "listen",
		"subscribe", "watch", // the agent is named on the command line
		"bind", "dispatch", "missions", // the launcher's and the services' own identities
		"answer", // a person (--as, AGENT_BUS_HUMAN), never the agent
		"roles":  // only doctor and adopt get here (main exits the others first, no bus); neither names a sender:
		// doctor --publish writes unauthored model verdicts, adopt writes the manifest
		return true
	case "gate":
		return first != "close"
	case "merge-queue": // drop and undrop write, as their caller (master-idle)
		return first != "drop" && first != "undrop"
	case "checkback": // set and --clear write as the caller; the bare list reads (master-idle)
		return len(rest) == 0
	case "run": // reset is the person's write (run-budget §5); the bare command reads
		return len(rest) == 0
	case "reload": // a person, by an explicit --as (lifecycle part C), never the pane's agent
		return slices.Contains(rest, "--as")
	case "pilot":
		return first == "status"
	case "request":
		// next only reads; priority is a person's, by an explicit --as (request-priority §4)
		return first == "" || first == "next" || first == "priority" && slices.Contains(rest, "--as")
	case "idea":
		// list only reads; set is the person's and refuses any pane itself, with its own words
		return first == "list" || first == "set"
	case "board", "handoff":
		return first == ""
	case "plan": // priority is a person's, by an explicit --as (as request priority)
		return first == "" || first == "show" || first == "priority" && slices.Contains(rest, "--as")
	case "health":
		// warns only reads; accept is the person's, by an explicit --as (as reload)
		return first == "" || first == "list" || first == "detail" || first == "log" || first == "warns" ||
			first == "accept" && slices.Contains(rest, "--as")
	}
	return false
}

// personGuard refuses (human-not-an-agent) ask and status naming the person
// from another agent's terminal. Both write the named agent's entry in
// {p}:agents with the caller's terminal: "agentbus ask <person>" in master's
// pane made the person an agent (busmon listed it, the dispatcher woke and
// escalated it). The name is the asking or declaring agent itself; the
// person is the one the questions go to, and answers in busmon. An agent of
// the person's name, and the person's own shell, are not refused; neither is
// withdrawing an ask, so a stray one can still be closed.
func personGuard(getenv func(string) string, cmd string, rest []string) error {
	if cmd != "ask" && cmd != "status" {
		return nil
	}
	self := agentIdentity(getenv)
	person := settingsIdentity(getenv)
	if self == "" || person == "" || self == person {
		return nil
	}
	args := rest
	if cmd == "ask" {
		var withdraw string
		for _, f := range []string{"--file", "--replace", "--question", "--note", "--task"} {
			args, _ = extractFlag(args, f)
		}
		if args, withdraw = extractFlag(args, "--withdraw"); withdraw != "" {
			return nil
		}
	}
	if len(args) == 0 || args[0] != person {
		return nil
	}
	return fmt.Errorf("refusing %s %s from %s's terminal: %q is the person (identity.as), not an agent, and this would list them as one. "+
		"%s names the agent itself: agentbus ask %s puts your questions to the person, who answers in busmon",
		cmd, person, self, person, cmd, self)
}
