// agentbus — CLI client for the Agent Bus. Drop-in replacement for agent_bus.py.
//
// Usage:
//   agentbus status <agent> <working|idle|blocked|done> [message...]
//   agentbus cmd <target> <command...>
//   agentbus notify <message...>
//   agentbus listen <channel_pattern>
//   agentbus report <agent> [--auto] <message...>
//   agentbus --host <host> <command> ...
//
// Unlike agent_bus.py, trailing words are joined, so unquoted multi-word
// messages (status claude1 working plan 10 shipped) are kept whole.
// The cmd sender defaults to hermes_vdr; override with AGENT_BUS_AGENT.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

func die(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

func main() {
	args := os.Args[1:]
	host := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--host" && i+1 < len(args) {
			host = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if len(args) < 1 {
		die("usage: agentbus <status|cmd|notify|listen|report> ...  [--host <host>]")
	}

	client, err := bus.Connect(host)
	if err != nil {
		die(fmt.Sprintf("Redis connection failed: %v", err))
	}
	ctx := context.Background()

	switch args[0] {
	case "status":
		if len(args) < 3 {
			die("usage: agentbus status <agent> <state> [message]")
		}
		agent, state := args[1], args[2]
		if !bus.ValidAgents[agent] {
			die(fmt.Sprintf("invalid agent %q", agent))
		}
		if !bus.ValidStates[state] {
			die(fmt.Sprintf("invalid state %q (working|idle|blocked|done)", state))
		}
		if err := bus.Status(ctx, client, agent, state, strings.Join(args[3:], " ")); err != nil {
			die(err.Error())
		}

	case "cmd":
		if len(args) < 3 {
			die("usage: agentbus cmd <target> <command>")
		}
		target := args[1]
		if !bus.ValidAgents[target] {
			die(fmt.Sprintf("invalid target %q", target))
		}
		from := os.Getenv("AGENT_BUS_AGENT")
		if from == "" {
			from = "hermes_vdr"
		}
		if err := bus.Cmd(ctx, client, from, target, strings.Join(args[2:], " ")); err != nil {
			die(err.Error())
		}

	case "notify":
		if len(args) < 2 {
			die("usage: agentbus notify <message>")
		}
		if err := bus.Notify(ctx, client, strings.Join(args[1:], " ")); err != nil {
			die(err.Error())
		}

	case "report":
		if len(args) < 3 {
			die("usage: agentbus report <agent> [--auto] <message>")
		}
		agent := args[1]
		if !bus.ValidAgents[agent] {
			die(fmt.Sprintf("invalid agent %q", agent))
		}
		rest := args[2:]
		kind := bus.ReportNote
		if rest[0] == "--auto" {
			kind = bus.ReportAuto
			rest = rest[1:]
		}
		if len(rest) == 0 {
			die("usage: agentbus report <agent> [--auto] <message>")
		}
		if err := bus.Report(ctx, client, agent, kind, strings.Join(rest, " ")); err != nil {
			die(err.Error())
		}

	case "listen":
		if len(args) < 2 {
			die("usage: agentbus listen <channel_pattern>")
		}
		fmt.Fprintf(os.Stderr, "Listening on %q (Ctrl+C to stop)...\n", args[1])
		bus.Listen(ctx, client, args[1], func(ch, msg string) {
			fmt.Printf("[%s] %s\n", ch, msg)
		})

	default:
		die(fmt.Sprintf("unknown command %q", args[0]))
	}
}
