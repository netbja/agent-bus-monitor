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
//	agentbus --project P reports   [<id>]         # list recent reports; <id> prints one report's full retained text
//	agentbus --project P notify    <msg...>
//	agentbus --project P cmd       [--ref T] [--no-wake] <target> <command...>   # prints the entry id (= thread root); --no-wake: never wakes the target
//	agentbus --project P thread    <thread-id>   # show the :cmd thread (entries where ref or id == thread-id), chronological
//	agentbus --project P challenge <target> [--ref R] <msg...>   # opens a 4-eyes gate
//	agentbus --project P reply     --ref R <target> <msg...>
//	agentbus --project P verdict   (--pr N | --subject S) [--ref R] <author> <approve|reject> [msg...]  # records to the ledger; resolves a matching gate as a bonus
//	agentbus --project P verdicts  [--pr N | --subject S]   # roll-up 4-eyes state of a subject (exit 0=approved/2=pending/3=rejected), or recent across all
//	agentbus --project P pilot     <claim|renew|release|status> [--ttl 90s]
//	agentbus --project P gate      <agent>      # lists open challenges; exit 1 if gated
//	agentbus --project P agents    [--json]      # current state of all agents (one line each)
//	agentbus --project P pane      <agent>       # print the agent's herdr pane (HERDR_PANE_ID); non-zero if none
//	agentbus --project P bind      <agent> [--pane P] [--pid N]   # bind a verified pane identity (lists candidates without --pid, exit 2)
//	agentbus --project P usage     [<agent> <json>]   # write a budget snapshot, or print all (status-line tee)
//	agentbus --project P subscribe [--since <cursor>] <agent> [idle_secs]  # JSON per fire; persist id, pass back as --since
//	agentbus --project P subscribe --drain <agent>  # JSONL, one line per waiting entry (PEL then unread); exit 0 output, 64 empty, 75 error
//	agentbus --project P watch     <agent>      # alias of subscribe (legacy name)
//	agentbus --project P budget    [--json]     # account budget window, one row per provider
//	agentbus --project P budget    <provider> <json>             # publish a provider's window
//	agentbus --project P board     [--json]     # shared task board: task → owner/state/branch
//	agentbus --project P board     claim <task> [--branch b]     # fails if a peer owns it
//	agentbus --project P board     state <task> <state> | done <task> | drop <task>
//	agentbus --project P shutdown  [--force]    # broadcast a shutdown directive (wake=no, expires in 10m) to every peer; refused while tasks aren't done or peers are busy
//	agentbus --project P refresh   [--quiet]    # read local sources -> publish budget + per-agent usage
//	agentbus --project P listen    [status report notify cmd]    # debug tail
//	agentbus --project P dispatch  [--horizon 24h] [--escalate-after 10m] [--escalate-to master]  # wake unarmed agents through herdr (JSON log on stderr)
//	agentbus --project P dispatch  --status     # print "dispatch up" (exit 0) or "dispatch down" (exit 1)
//	agentbus --project P request [--json] | send <task> <target> [--ref T] [--ttl 5m] <body>
//	agentbus --project P request accept <task> | block <task> <reason> | done <task> <reply>
//	agentbus roles  <validate|exists|tier|field|launch|install|template|check-toml> ...  # role manifest (scripts), no project/broker
//	agentbus limits                # writer size/retention limits, no broker needed
//	agentbus version               # print the bus protocol version (no project/broker needed)
//	agentbus --host <host> ...
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/usage"
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
	// mem works on files in the working tree — no project, no broker — so it
	// runs before the project check, like limits and version.
	if len(args) >= 1 && args[0] == "mem" {
		wd, _ := os.Getwd()
		os.Exit(runMem(args[1:], os.Getenv, wd, time.Now(), os.Stdout, os.Stderr))
	}
	// roles reads the role manifest (the scripts' launcher): no project, no
	// broker. launch replaces this process with the client.
	if len(args) >= 1 && args[0] == "roles" {
		os.Exit(runRoles(args[1:], os.Environ(), os.Stdout, os.Stderr))
	}
	// version reads a compile-time constant — no project, no broker. Handle it
	// before the project check and before bus.Connect.
	if len(args) >= 1 && args[0] == "limits" {
		out, _ := json.MarshalIndent(bus.Limits(), "", "  ")
		fmt.Println(string(out))
		return
	}
	if len(args) >= 1 && args[0] == "version" {
		fmt.Printf("agentbus protocol v%d\nagentbus version %s\n", bus.ProtocolVersion, buildinfo.Version)
		return
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}
	if len(args) < 1 {
		die("usage: agentbus --project <p> <status|report|reports|notify|cmd|thread|challenge|reply|verdict|verdicts|version|pilot|gate|agents|pane|bind|usage|budget|board|shutdown|refresh|request|dispatch|missions|mission|limits|mem|roles|subscribe|watch|listen> ...")
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
	case "missions":
		sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		wd, _ := os.Getwd()
		code := runMissions(sctx, b, rest, wd, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case "bind":
		h := dispatch.Herdr{Bin: os.Getenv("HERDR_BIN")}
		os.Exit(runBind(ctx, bindDeps{Bind: b.Bind, Procs: h.ForegroundProcs, Start: dispatch.ProcStart, Env: os.Getenv}, rest, os.Stdout, os.Stderr))
	case "mission":
		os.Exit(runMission(ctx, b, self, rest, os.Stdout, os.Stderr))
	case "dispatch":
		sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		code := runDispatch(sctx, b, rest, dispatch.Herdr{Bin: os.Getenv("HERDR_BIN")}, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)

	case "request":
		if err := runRequest(ctx, b, self, rest, os.Stdout); err != nil {
			die(err.Error())
		}
	case "status":
		if len(rest) < 2 {
			die("usage: status <agent> <state> [message]")
		}
		// Ambient identity, read from the environment rather than asked of the
		// agent: HERDR_PANE_ID (empty outside herdr) locates its pane, and
		// CLAUDE_CODE_SESSION_ID (empty for a non-Claude-Code peer) locates its
		// transcript, which is what `agentbus refresh` reads usage from. The
		// agent never types either, so neither can drift or be forgotten.
		who := statusIdent(ctx)
		if _, err := b.Status(ctx, rest[0], rest[1], strings.Join(rest[2:], " "), who); err != nil {
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

	case "reports":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) >= 1 { // detail: one report's full text by id
			e, err := b.ReportByID(ctx, rest[0])
			if err != nil {
				die(err.Error())
			}
			if asJSON {
				out, _ := json.MarshalIndent(e, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Println(reportDetail(e))
			return
		}
		evs, err := b.Reports(ctx, 25)
		if err != nil {
			die(err.Error())
		}
		if asJSON {
			out, _ := json.MarshalIndent(evs, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Print(reportsTable(evs))

	case "notify":
		if len(rest) < 1 {
			die("usage: notify <message>")
		}
		if _, err := b.Notify(ctx, self, strings.Join(rest, " ")); err != nil {
			die(err.Error())
		}

	case "cmd":
		if err := runCmd(ctx, b, self, rest, os.Stdout); err != nil {
			die(err.Error())
		}

	case "thread":
		if len(rest) < 1 {
			die("usage: thread <thread-id>")
		}
		evs, err := b.Thread(ctx, rest[0])
		if err != nil {
			die(err.Error())
		}
		fmt.Print(threadReport(rest[0], evs, time.Now()))

	case "challenge":
		rest, ref := extractFlag(rest, "--ref")
		if len(rest) < 2 {
			die("usage: challenge <target> [--ref R] <message>")
		}
		if ref == "" {
			ref = genRef()
		}
		target, msg := rest[0], strings.Join(rest[1:], " ")
		if err := bus.ValidateText(msg, bus.CmdMaxRunes); err != nil {
			die(err.Error())
		}
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
		cmdMsg := decision
		if rationale != "" {
			cmdMsg += ": " + rationale
		}
		if err := bus.ValidateText(cmdMsg, bus.CmdMaxRunes); err != nil {
			die(err.Error())
		}
		// 1. Durable ledger entry — always, including self-approvals (the
		//    independence rule is enforced at read time, not here).
		if _, err := b.AppendVerdict(ctx, bus.Verdict{
			Subject: subject, Author: author, Reviewer: self,
			Decision: decision, Message: rationale, Ref: ref,
		}); err != nil {
			die(err.Error())
		}
		// 2. Live notification on :cmd so busmon and the author see it (as before).
		if _, err := b.Cmd(ctx, self, author, bus.CmdVerdict, ref, cmdMsg); err != nil {
			die(err.Error())
		}
		// 3. Bonus gate resolution: best-effort, only when --ref names an open
		//    challenge. A missing/stale ref is a notice, not a fatal error — that
		//    is what lets a cmd-requested review (no challenge) still be recorded.
		if ref != "" {
			if msg := verdictGateNotice(ctx, b, author, self, ref); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		fmt.Printf("verdict recorded: %s %s on %s\n", decision, subject, author)

	case "verdicts":
		rest, asJSON := extractBool(rest, "--json")
		if asJSON {
			rest, pr := extractFlag(rest, "--pr")
			_, subject := extractFlag(rest, "--subject")
			if pr != "" || subject != "" {
				var err error
				subject, err = resolveSubject(pr, subject)
				if err != nil {
					die(err.Error())
				}
			}
			vs, err := b.Verdicts(ctx, subject)
			if err != nil {
				die(err.Error())
			}
			out, _ := json.MarshalIndent(vs, "", "  ")
			fmt.Println(string(out))
			return
		}
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

	case "budget":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) == 0 {
			m, err := b.Budgets(ctx)
			if err != nil {
				die(err.Error())
			}
			if asJSON {
				out, _ := json.MarshalIndent(m, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Print(budgetTable(m, time.Now()))
			return
		}
		if len(rest) < 2 {
			die("usage: budget <provider> <json>   (or no args to read every provider's window)")
		}
		var snap bus.BudgetSnapshot
		if err := json.Unmarshal([]byte(strings.Join(rest[1:], " ")), &snap); err != nil {
			die("bad budget JSON: " + err.Error())
		}
		snap.TS = time.Now().UnixMilli()
		if err := b.SetBudget(ctx, rest[0], snap); err != nil {
			die(err.Error())
		}

	case "board":
		rest, asJSON := extractBool(rest, "--json")
		rest, branch := extractFlag(rest, "--branch")
		if len(rest) == 0 { // list the whole board
			m, err := b.Board(ctx)
			if err != nil {
				die(err.Error())
			}
			if asJSON {
				out, _ := json.MarshalIndent(m, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Print(boardTable(m, time.Now()))
			return
		}
		switch rest[0] {
		case "claim":
			if len(rest) < 2 {
				die("usage: board claim <task> [--branch b]")
			}
			if err := b.BoardClaim(ctx, rest[1], self, branch); err != nil {
				die(err.Error())
			}
		case "state":
			if len(rest) < 3 {
				die("usage: board state <task> <state>")
			}
			if err := b.BoardState(ctx, rest[1], rest[2]); err != nil {
				die(err.Error())
			}
		case "done":
			if len(rest) < 2 {
				die("usage: board done <task>")
			}
			if err := b.BoardState(ctx, rest[1], "done"); err != nil {
				die(err.Error())
			}
		case "drop":
			if len(rest) < 2 {
				die("usage: board drop <task>")
			}
			if err := b.BoardDrop(ctx, rest[1]); err != nil {
				die(err.Error())
			}
		default:
			die("board: want claim|state|done|drop (or no subcommand to list)")
		}

	case "shutdown":
		// Global team stop: broadcast a shutdown directive to every known peer.
		// Refused while work is in flight (non-done board tasks, busy peers)
		// unless --force. Closing the panes afterwards is master's job (its
		// skill) — the bus only carries the signal.
		_, force := extractBool(rest, "--force")
		board, err := b.Board(ctx)
		if err != nil {
			die(err.Error())
		}
		agents, err := b.Agents(ctx)
		if err != nil {
			die(err.Error())
		}
		if !force {
			blockers := shutdownBlockers(board, agents, self, time.Now())
			if len(blockers) > 0 {
				for _, bl := range blockers {
					fmt.Fprintln(os.Stderr, "shutdown blocked: "+bl)
				}
				die(fmt.Sprintf("shutdown refused: %d blocker(s) — finish the work or pass --force", len(blockers)))
			}
		}
		msg := "shutdown: all work complete — set status done, post a final report, and do NOT re-arm subscribe; master closes your pane"
		n := broadcastShutdown(ctx, b, self, agents, msg, os.Stderr)
		fmt.Printf("shutdown broadcast to %d agent(s)\n", n)

	case "refresh":
		// Read the local artefacts and publish both halves: the ACCOUNT budget
		// (one row per provider) and each AGENT's model + context fill (from its
		// own transcript). Nothing here asks an agent to cooperate.
		//
		// Never fails on a missing source — this runs from the sentinel's cron
		// wake, where a hard exit means the whole caretaker pass is lost.
		_, quiet := extractBool(rest, "--quiet")
		notes := []string{}

		budgets, bnotes := collectBudgets(usage.CCStatuslinePath())
		notes = append(notes, bnotes...)
		for provider, snap := range budgets {
			if err := b.SetBudget(ctx, provider, snap); err != nil {
				notes = append(notes, fmt.Sprintf("publishing %s budget: %v", provider, err))
			}
		}

		agents, err := b.Agents(ctx)
		if err != nil {
			notes = append(notes, fmt.Sprintf("reading agents: %v", err))
		}
		snaps, unotes := collectAgentUsage(agents, usage.ProjectsRoot())
		notes = append(notes, unotes...)
		for agent, snap := range snaps {
			if err := b.SetUsage(ctx, agent, snap); err != nil {
				notes = append(notes, fmt.Sprintf("publishing %s usage: %v", agent, err))
			}
		}

		if !quiet {
			fmt.Printf("refresh: %d provider budget(s), %d agent snapshot(s)\n", len(budgets), len(snaps))
			for _, n := range notes {
				fmt.Fprintln(os.Stderr, "refresh: "+n)
			}
		}

	case "subscribe", "watch":
		// One subscription tick (or a headless --loop). Emits one JSON subEvent
		// per fire: the cmd payload + cursor (id) + rearm flag. --since <cursor>
		// sets the floor; omitted = skip backlog (start at the server's "now").
		// --drain empties the queue (PEL, then unread; no floor) and exits.
		sa, perr := parseSubscribeArgs(rest)
		if errors.Is(perr, errDrainUsage) {
			fmt.Fprintln(os.Stderr, "error: "+perr.Error())
			fmt.Fprintln(os.Stderr, subscribeUsage)
			os.Exit(2)
		}
		if perr != nil {
			die(perr.Error())
		}
		agent, loop, since := sa.agent, sa.loop, sa.since
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		if sa.drain {
			os.Exit(runDrain(ctx, b, agent, consumer, os.Stdout))
		}
		idle := heartbeat
		if sa.idle != "" {
			idle = parseIdle(sa.idle, heartbeat)
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

// statusIdent is the identity `status` publishes, all of it ambient. Inside
// herdr (HERDR_ENV=1 and HERDR_PANE_ID set) it also records the agent's
// process: the pane's foreground pid that is an ancestor of this process, its
// /proc start time, and the herdr terminal id — what the dispatcher checks
// before typing into the pane. Any failure leaves those empty (the agent is
// then unverified and never prompted); status itself still succeeds.
// HERDR_BIN overrides the herdr binary.
func statusIdent(ctx context.Context) bus.AgentIdent {
	who := bus.AgentIdent{
		Pane:    os.Getenv("HERDR_PANE_ID"),
		Session: os.Getenv("CLAUDE_CODE_SESSION_ID"),
	}
	if os.Getenv("HERDR_ENV") != "1" || who.Pane == "" {
		return who
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	pids, terminal, err := dispatch.Herdr{Bin: os.Getenv("HERDR_BIN")}.ForegroundPIDs(ctx, who.Pane)
	if err != nil {
		return who
	}
	pid, ok := dispatch.AncestorAmong(pids, os.Getpid())
	if !ok {
		return who
	}
	start, err := dispatch.ProcStart(pid)
	if err != nil {
		return who
	}
	who.PID, who.PIDStart, who.Terminal = pid, start, terminal
	return who
}
