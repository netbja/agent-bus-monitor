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
//	agentbus --project P verdict   (--pr N | --subject S) [--ref R] <author> <approve|reject> [msg...]  # records to the ledger; resolves a matching gate as a bonus
//	agentbus --project P verdicts  [--pr N | --subject S]   # roll-up 4-eyes state of a subject (exit 0=approved/2=pending/3=rejected), or recent across all
//	agentbus --project P pilot     <claim|renew|release|status> [--ttl 90s]
//	agentbus --project P gate      <agent>      # lists open challenges; exit 1 if gated
//	agentbus --project P agents    [--json]      # current state of all agents (one line each)
//	agentbus --project P pane      <agent>       # print the agent's herdr pane (HERDR_PANE_ID); non-zero if none
//	agentbus --project P usage     [<agent> <json>]   # write a budget snapshot, or print all (status-line tee)
//	agentbus --project P subscribe [--since <cursor>] <agent> [idle_secs]  # JSON per fire; persist id, pass back as --since
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
	heartbeat = 240 * time.Second // subscribe emits a heartbeat JSON object (rearm:true) and exits after this idle window
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
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|verdicts|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
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
		// HERDR_PANE_ID (set inside a herdr pane) registers the agent's pane in
		// the {project}:agents hash; empty outside herdr.
		pane := os.Getenv("HERDR_PANE_ID")
		if _, err := b.Status(ctx, rest[0], rest[1], strings.Join(rest[2:], " "), pane); err != nil {
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
		rest, pr := extractFlag(rest, "--pr")
		rest, subjectFlag := extractFlag(rest, "--subject")
		subject, serr := resolveSubject(pr, subjectFlag)
		if serr != nil {
			die(serr.Error())
		}
		if len(rest) < 2 {
			die("usage: verdict (--pr N | --subject S) [--ref R] <author> <approve|reject> [message]")
		}
		author, decision := rest[0], rest[1]
		if decision != "approve" && decision != "reject" {
			die("verdict decision must be approve or reject")
		}
		rationale := strings.Join(rest[2:], " ")
		// 1. Durable ledger entry — always, including self-approvals (the
		//    independence rule is enforced at read time, not here).
		if _, err := b.AppendVerdict(ctx, bus.Verdict{
			Subject: subject, Author: author, Reviewer: self,
			Decision: decision, Message: rationale, Ref: ref,
		}); err != nil {
			die(err.Error())
		}
		// 2. Live notification on :cmd so busmon and the author see it (as before).
		cmdMsg := decision
		if rationale != "" {
			cmdMsg += ": " + rationale
		}
		if _, err := b.Cmd(ctx, self, author, bus.CmdVerdict, ref, cmdMsg); err != nil {
			die(err.Error())
		}
		// 3. Bonus gate resolution: best-effort, only when --ref names an open
		//    challenge. A missing/stale ref is a notice, not a fatal error — that
		//    is what lets a cmd-requested review (no challenge) still be recorded.
		if ref != "" {
			if err := b.ResolveChallenge(ctx, author, ref); err != nil {
				fmt.Fprintln(os.Stderr, "notice: "+err.Error())
			}
		}
		fmt.Printf("verdict recorded: %s %s on %s\n", decision, subject, author)

	case "verdicts":
		rest, pr := extractFlag(rest, "--pr")
		rest, subjectFlag := extractFlag(rest, "--subject")
		if strings.TrimSpace(pr) == "" && strings.TrimSpace(subjectFlag) == "" {
			vs, err := b.Verdicts(ctx, "") // overview: all subjects
			if err != nil {
				die(err.Error())
			}
			fmt.Print(verdictsOverview(vs, time.Now()))
			return
		}
		subject, serr := resolveSubject(pr, subjectFlag)
		if serr != nil {
			die(serr.Error())
		}
		vs, err := b.Verdicts(ctx, subject)
		if err != nil {
			die(err.Error())
		}
		out, code := verdictsReport(subject, vs, time.Now())
		fmt.Print(out)
		os.Exit(code)

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

	case "pane":
		if len(rest) < 1 {
			die("usage: pane <agent>")
		}
		m, err := b.Agents(ctx)
		if err != nil {
			die(err.Error())
		}
		p, ok := agentPane(m, rest[0])
		if !ok {
			die(fmt.Sprintf("no herdr pane registered for %q", rest[0]))
		}
		fmt.Println(p)

	case "usage":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) == 0 {
			m, err := b.Usage(ctx)
			if err != nil {
				die(err.Error())
			}
			if asJSON {
				out, _ := json.MarshalIndent(m, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Print(usageTable(m, time.Now()))
			return
		}
		if len(rest) < 2 {
			die("usage: usage <agent> <json>   (or no args to read everyone's budget)")
		}
		var snap bus.UsageSnapshot
		if err := json.Unmarshal([]byte(strings.Join(rest[1:], " ")), &snap); err != nil {
			die("bad usage JSON: " + err.Error())
		}
		snap.TS = time.Now().UnixMilli()
		if err := b.SetUsage(ctx, rest[0], snap); err != nil {
			die(err.Error())
		}

	case "subscribe", "watch":
		// One subscription tick (or a headless --loop). Emits one JSON subEvent
		// per fire: the cmd payload + cursor (id) + rearm flag. --since <cursor>
		// sets the floor; omitted = skip backlog (start at the server's "now").
		rest, loop := extractBool(rest, "--loop")
		rest, since := extractFlag(rest, "--since")
		if len(rest) < 1 {
			die("usage: subscribe [--loop] [--since <cursor>] <agent> [idle_seconds]")
		}
		agent := rest[0]
		idle := heartbeat
		if len(rest) > 1 {
			idle = parseIdle(rest[1], heartbeat)
		}
		floor := since
		switch {
		case floor == "":
			f, ferr := b.ServerFloor(ctx)
			if ferr != nil {
				die("could not resolve server-time floor: " + ferr.Error())
			}
			floor = f
		case floor != "0" && !strings.Contains(floor, "-"):
			floor += "-0" // accept a bare <ms> cursor
		}
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		os.Exit(runSubscribe(ctx, b, agent, consumer, idle, floor, loop, os.Stdout))

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
