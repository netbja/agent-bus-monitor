// agentbus — CLI client for the Agent Bus over Redis Streams.
//
// The project is required (--project or AGENT_BUS_PROJECT); every stream is
// namespaced {project}:{kind}. Self identity (for `from`/pilot driver) is
// AGENT_BUS_AGENT (default "hermes"). Trailing words are joined into one message.
//
// Usage:
//   agentbus --project P status    <agent> <working|idle|blocked|done> [msg...]
//   agentbus --project P report    <agent> [--auto] <msg...>
//   agentbus --project P notify    <msg...>
//   agentbus --project P cmd       <target> <command...>
//   agentbus --project P challenge <target> [--ref R] <msg...>   # opens a 4-eyes gate
//   agentbus --project P reply     --ref R <target> <msg...>
//   agentbus --project P verdict   --ref R <target> <approve|reject> [msg...]  # resolves the gate
//   agentbus --project P pilot     <claim|renew|release|status> [--ttl 90s]
//   agentbus --project P gate      <agent>      # lists open challenges; exit 1 if gated
//   agentbus --project P watch     <agent>      # one-shot: prints first addressed cmd, or __HEARTBEAT__
//   agentbus --project P listen    [status report notify cmd]    # debug tail
//   agentbus --host <host> ...
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const (
	pilotTTL  = 90 * time.Second  // default pilot lease TTL (override --ttl)
	heartbeat = 240 * time.Second // watch prints __HEARTBEAT__ and exits after this idle window
)

func die(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	args := os.Args[1:]
	args, host := extractFlag(args, "--host")
	args, project := extractFlag(args, "--project")
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}
	if len(args) < 1 {
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|watch|listen> ...")
	}

	self := envOr("AGENT_BUS_AGENT", "hermes")

	client, err := bus.Connect(host)
	if err != nil {
		die(fmt.Sprintf("Redis connection failed: %v", err))
	}
	b, err := bus.Open(client, project)
	if err != nil {
		die(err.Error())
	}
	ctx := context.Background()
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "status":
		if len(rest) < 2 {
			die("usage: status <agent> <state> [message]")
		}
		if _, err := b.Status(ctx, rest[0], rest[1], strings.Join(rest[2:], " ")); err != nil {
			die(err.Error())
		}

	case "report":
		rest, auto := extractBool(rest, "--auto")
		if len(rest) < 2 {
			die("usage: report <agent> [--auto] <message>")
		}
		kind := bus.ReportNote
		if auto {
			kind = bus.ReportAuto
		}
		if _, err := b.Report(ctx, rest[0], kind, strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "notify":
		if len(rest) < 1 {
			die("usage: notify <message>")
		}
		if _, err := b.Notify(ctx, self, strings.Join(rest, " ")); err != nil {
			die(err.Error())
		}

	case "cmd":
		if len(rest) < 2 {
			die("usage: cmd <target> <command>")
		}
		if _, err := b.Cmd(ctx, self, rest[0], bus.CmdDirective, "", strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "challenge":
		rest, ref := extractFlag(rest, "--ref")
		if len(rest) < 2 {
			die("usage: challenge <target> [--ref R] <message>")
		}
		if ref == "" {
			ref = genRef()
		}
		target, msg := rest[0], strings.Join(rest[1:], " ")
		if _, err := b.Cmd(ctx, self, target, bus.CmdChallenge, ref, msg); err != nil {
			die(err.Error())
		}
		if err := b.OpenChallenge(ctx, target, ref, self+"|"+msg); err != nil {
			die(err.Error())
		}
		fmt.Printf("challenge %s opened on %s\n", ref, target)

	case "reply":
		rest, ref := extractFlag(rest, "--ref")
		if ref == "" || len(rest) < 2 {
			die("usage: reply --ref R <target> <message>")
		}
		if _, err := b.Cmd(ctx, self, rest[0], bus.CmdReply, ref, strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "verdict":
		rest, ref := extractFlag(rest, "--ref")
		if ref == "" || len(rest) < 2 {
			die("usage: verdict --ref R <target> <approve|reject> [message]")
		}
		target, decision := rest[0], rest[1]
		if decision != "approve" && decision != "reject" {
			die("verdict decision must be approve or reject")
		}
		msg := decision
		if len(rest) > 2 {
			msg += ": " + strings.Join(rest[2:], " ")
		}
		// Resolve first: a verdict for a ref that isn't open must fail loudly.
		if err := b.ResolveChallenge(ctx, target, ref); err != nil {
			die(err.Error())
		}
		if _, err := b.Cmd(ctx, self, target, bus.CmdVerdict, ref, msg); err != nil {
			die(err.Error())
		}

	case "pilot":
		if len(rest) < 1 {
			die("usage: pilot <claim|renew|release|status> [--ttl 90s]")
		}
		rest, ttlStr := extractFlag(rest, "--ttl")
		ttl := pilotTTL
		if ttlStr != "" {
			if d, perr := time.ParseDuration(ttlStr); perr == nil {
				ttl = d
			} else {
				die(fmt.Sprintf("bad --ttl %q: %v", ttlStr, perr))
			}
		}
		switch rest[0] {
		case "claim", "renew":
			if err := b.Pilot(ctx, self, ttl); err != nil {
				die(err.Error())
			}
		case "release":
			if err := b.ReleasePilot(ctx); err != nil {
				die(err.Error())
			}
		case "status":
			d, err := b.PilotDriver(ctx)
			if err != nil {
				die(err.Error())
			}
			if d == "" {
				fmt.Println("autonomous")
			} else {
				fmt.Println("piloted by " + d)
			}
		default:
			die("pilot: want claim|renew|release|status")
		}

	case "gate":
		if len(rest) < 1 {
			die("usage: gate <agent>")
		}
		m, err := b.OpenChallenges(ctx, rest[0])
		if err != nil {
			die(err.Error())
		}
		if len(m) == 0 {
			fmt.Printf("%s: ungated\n", rest[0])
			return
		}
		for ref, meta := range m {
			fmt.Printf("%s\t%s\n", ref, meta)
		}
		os.Exit(1) // gated → non-zero so a script/agent can block on it

	case "watch":
		if len(rest) < 1 {
			die("usage: watch <agent>")
		}
		agent := rest[0]
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		wctx, cancel := context.WithTimeout(ctx, heartbeat)
		defer cancel()
		werr := b.WatchCmd(wctx, agent, consumer, func(e bus.Event) bool {
			ref := ""
			if e.Ref != "" {
				ref = " ref=" + e.Ref
			}
			fmt.Printf("[%s %s->%s%s] %s\n", e.Type, e.From, e.Target, ref, e.Message)
			return true
		})
		if werr == nil {
			return // delivered one cmd
		}
		if errors.Is(werr, context.DeadlineExceeded) {
			fmt.Println("__HEARTBEAT__") // idle window elapsed; re-arm the watcher
			return
		}
		die(werr.Error())

	case "listen":
		kinds := rest
		if len(kinds) == 0 {
			kinds = []string{"status", "report", "notify", "cmd"}
		}
		fmt.Fprintf(os.Stderr, "Tailing %v on project %q (Ctrl+C to stop)...\n", kinds, project)
		err := b.Tail(ctx, "$", kinds, func(e bus.Event) {
			who := e.Agent
			if who == "" {
				who = e.From
			}
			fmt.Printf("[%s %s] %s\n", e.Kind, who, e.Message)
		})
		if err != nil {
			die(err.Error())
		}

	default:
		die(fmt.Sprintf("unknown command %q", cmd))
	}
}
