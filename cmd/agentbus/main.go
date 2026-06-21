// agentbus — CLI client for the Agent Bus over Redis Streams.
//
// The project is required (--project or AGENT_BUS_PROJECT); every stream is
// namespaced {project}:{kind}. Self identity (for `from`/pilot driver) is
// AGENT_BUS_AGENT (default "hermes"). Trailing words are joined into one message.
//
// Usage:
//
//	agentbus --project P status    <agent> <working|idle|blocked|done> [msg...]
//	agentbus --project P report    <agent> [--auto] <msg...>
//	agentbus --project P notify    <msg...>
//	agentbus --project P cmd       <target> <command...>
//	agentbus --project P challenge <target> [--ref R] <msg...>   # opens a 4-eyes gate
//	agentbus --project P reply     --ref R <target> <msg...>
//	agentbus --project P verdict   --ref R <target> <approve|reject> [msg...]  # resolves the gate
//	agentbus --project P pilot     <claim|renew|release|status> [--ttl 90s]
//	agentbus --project P gate      <agent>      # lists open challenges; exit 1 if gated
//	agentbus --project P agents    [--json]      # current state of all agents (one line each)
//	agentbus --project P subscribe <agent> [idle_secs]  # block for next addressed cmd, print it, exit; re-arm to stay subscribed
//	agentbus --project P watch     <agent>      # alias of subscribe (legacy name)
//	agentbus --project P listen    [status report notify cmd]    # debug tail
//	agentbus --host <host> ...
package main

import (
	"context"
	"encoding/json"
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

// listenKinds bounds the stream kinds the debug `listen` tail accepts.
var listenKinds = map[string]bool{"status": true, "report": true, "notify": true, "cmd": true}

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
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|agents|subscribe|watch|listen> ...")
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
		// Open the gate first — it's the authoritative blocking record. If the
		// cmd publish then fails, the target stays correctly gated rather than
		// seeing a phantom challenge event with no matching gate.
		if err := b.OpenChallenge(ctx, target, ref, self+"|"+msg); err != nil {
			die(err.Error())
		}
		if _, err := b.Cmd(ctx, self, target, bus.CmdChallenge, ref, msg); err != nil {
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
		rest, ttlStr := extractFlag(rest, "--ttl")
		if len(rest) < 1 {
			die("usage: pilot <claim|renew|release|status> [--ttl 90s]")
		}
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

	case "agents":
		_, asJSON := extractBool(rest, "--json")
		m, err := b.Agents(ctx)
		if err != nil {
			die(err.Error())
		}
		if asJSON {
			out, _ := json.MarshalIndent(m, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Print(agentsTable(m, time.Now()))

	case "subscribe", "watch":
		// One subscription tick (or a headless --loop). The wake-on-exit model
		// for terminal sessions: block on the agent's :cmd group, print the next
		// addressed entry plus a __AGENTBUS__ rearm sentinel, and EXIT — that exit
		// re-invokes the session, which re-arms iff the sentinel says rearm=1.
		// --loop is for headless consumers (hermes/shell) only — never a terminal
		// wake path, since a long-lived loop can't wake a session.
		rest, loop := extractBool(rest, "--loop")
		if len(rest) < 1 {
			die("usage: subscribe [--loop] <agent> [idle_seconds]")
		}
		agent := rest[0]
		idle := heartbeat
		if len(rest) > 1 {
			idle = parseIdle(rest[1], heartbeat)
		}
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		os.Exit(runSubscribe(ctx, b, agent, consumer, idle, loop, os.Stdout))

	case "listen":
		kinds := rest
		if len(kinds) == 0 {
			kinds = []string{"status", "report", "notify", "cmd"}
		}
		for _, k := range kinds {
			if !listenKinds[k] {
				die(fmt.Sprintf("listen: unknown kind %q (want status|report|notify|cmd)", k))
			}
		}
		fmt.Fprintf(os.Stderr, "Tailing %v on project %q (Ctrl+C to stop)...\n", kinds, project)
		// "$" = live only. Tail warns "$" can miss entries across multiple
		// streams in the first poll gap; acceptable for a debug listener.
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
