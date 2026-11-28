package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// observer-role (the person, 2026-11-25): a session agentbus does not
// launch (claude-ao, the Orchestrator) declared kind = "observer" in
// roles.toml. Under its own name (AGENT_BUS_AGENT) it reads the bus and
// writes nothing: the CLI allows only the read forms below and refuses
// every other command before the bus. With broker.acl off this refusal and
// the docs are the boundary (a process can still write Redis directly);
// with it on, its Redis user reads only (bus.ACLObserver).

// observerReads are the commands an observer may run, the summary its
// refusal names.
const observerReads = "summary, agents, board, request, reports, thread, history, verdicts, asks, swaps, health, quota, usage, budget, tokens, models, merge-queue, audit, version, limits, help"

// positionals are args that are not flags.
func positionals(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// observerRead: cmd rest is a read form (it writes nothing on the bus, nor
// any file of the project's).
func observerRead(cmd string, rest []string) bool {
	pos := positionals(rest)
	first := ""
	if len(pos) > 0 {
		first = pos[0]
	}
	switch cmd {
	case "summary", "reports", "thread", "history", "verdicts", "asks", "swaps", "quota", "tokens", "audit", "version", "limits", "help":
		return true
	case "agents":
		return first != "forget"
	case "board", "request", "usage", "budget", "merge-queue": // their bare listing; every subcommand writes
		return len(pos) == 0
	case "health":
		return first != "note" && first != "accept"
	case "models":
		return (first == "" || first == "check") && !slices.Contains(rest, "--refresh")
	}
	return false
}

// observerRefusal is why args are refused, "" allowed: to identity when
// isObserver names it, or to any identity under a registered observer
// (ancestor, read only for a command that is not a read: reviewer-codex);
// nil isObserver (no manifest known) limits nobody.
func observerRefusal(args []string, identity string, ancestor func() string, isObserver func(string) bool) string {
	if isObserver == nil || len(args) == 0 || observerRead(args[0], args[1:]) {
		return ""
	}
	who := ""
	switch {
	case identity != "" && isObserver(identity):
		who = identity + " is an observer"
	default:
		if a := ancestor(); a != "" && isObserver(a) {
			who = "this process runs under the observer " + a
		}
	}
	if who == "" {
		return ""
	}
	return fmt.Sprintf("%s (roles.toml kind = \"observer\"): it reads only (%s); %q writes or acts on the bus, refused", who, observerReads, args[0])
}

// launcherObservers are the observers of the manifest the launcher uses
// (ROLES_TOML, else the repository's), nil when it declares none or cannot
// be read: then nobody is limited, as before the observer role existed.
func launcherObservers() func(string) bool {
	path, err := manifestPath(os.Environ())
	if err != nil {
		return nil
	}
	m, err := rolecfg.Load(path)
	if err != nil || len(m.Observers()) == 0 {
		return nil
	}
	return m.IsObserver
}

// observerGuard refuses an observer's write before the bus, and tells Connect
// which identities are observers (their Redis user, while broker.acl is on).
// The ancestry is read only when the manifest declares an observer and the
// command writes; one that cannot be read limits nobody (stated).
func observerGuard(args []string, ancestorOf func(context.Context) (string, error)) (int, bool) {
	isObserver := launcherObservers()
	bus.SetObservers(isObserver)
	ancestor := func() string {
		a, _ := ancestorOf(context.Background())
		return a
	}
	if why := observerRefusal(args, os.Getenv("AGENT_BUS_AGENT"), ancestor, isObserver); why != "" {
		fmt.Fprintln(os.Stderr, "error: "+why)
		return 2, true
	}
	return 0, false
}
