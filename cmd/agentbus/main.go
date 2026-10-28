// agentbus — CLI client for the Agent Bus over Redis Streams.
//
// The project is required (--project or AGENT_BUS_PROJECT); every stream is
// namespaced {project}:{kind}. Self identity (for `from`/pilot driver) is
// AGENT_BUS_AGENT (default "hermes"). Trailing words are joined into one message.
//
// Every command's usage, flags (long and short) and examples are in one
// table, commands.go: `agentbus help`, `agentbus help <cmd>` or
// `agentbus <cmd> --help` prints them.
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
	"github.com/netbja/agent-bus-monitor/cli"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/models"
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
	// Short flags become long ones (the table in commands.go), so every
	// parser below sees only the long forms it always read.
	args := expandArgs(os.Args[1:])
	args, host, project := globalArgs(args)
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	// help reads the table only: no project, no broker.
	if name, ok := helpRequest(args); ok {
		os.Exit(runHelp(name, os.Stdout, os.Stderr))
	}
	if len(args) == 0 {
		cli.Index(os.Stderr, "agentbus", globalFlags, commands)
		os.Exit(1)
	}
	// mem works on files in the working tree — no project, no broker — so it
	// runs before the project check, like limits and version.
	if len(args) >= 1 && args[0] == "mem" {
		wd, _ := os.Getwd()
		os.Exit(runMem(args[1:], os.Getenv, wd, time.Now(), os.Stdout, os.Stderr))
	}
	// roles reads the role manifest (the scripts' launcher): no project, no
	// broker. launch replaces this process with the client.
	// models is the machine's model table (v0.15.2): no project, no broker.
	if len(args) >= 1 && args[0] == "models" {
		os.Exit(runModels(context.Background(), args[1:], models.RealEnv(), models.CachePath(), models.ConfigPath(), time.Now(), os.Stdout, os.Stderr))
	}
	// doctor and adopt need the project's bus: they go through the switch below.
	if len(args) >= 1 && args[0] == "roles" && !(len(args) >= 2 && (args[1] == "doctor" || args[1] == "adopt")) {
		os.Exit(runRoles(args[1:], os.Environ(), os.Stdout, os.Stderr))
	}
	// identity is the person's name in busmon.toml (v0.21): no project, no broker.
	if len(args) >= 1 && args[0] == "identity" {
		os.Exit(runIdentity(args[1:], os.Getenv, os.Stdout, os.Stderr))
	}
	// projects lists every known project (v0.21): templates, broker, services,
	// agents. It answers "which project?", so it needs none, and works with Redis down.
	if len(args) >= 1 && args[0] == "projects" {
		os.Exit(runProjects(args[1:], realProjectsDeps(), os.Stdout, os.Stderr))
	}
	// plugin holds herdr's entry points (v0.20); it needs no project and no broker.
	if len(args) >= 1 && args[0] == "plugin" {
		os.Exit(runPlugin(args[1:], realPluginDeps(), os.Stdout, os.Stderr))
	}
	// up provisions a project and opens its herdr workspace: no project flag, no broker of its own.
	if len(args) >= 1 && args[0] == "up" {
		os.Exit(runUp(args[1:], realUpDeps(os.Stdout, os.Stderr), os.Stdout, os.Stderr))
	}
	// service drives the project's user services (v0.12); run is what a unit
	// executes and loads its environment file before it connects. list and
	// remove --all need no project; down stops a project's services.
	if len(args) >= 1 && args[0] == "service" {
		if len(args) >= 2 && args[1] == "run" {
			os.Exit(mainServiceRun(project, host, args[2:]))
		}
		os.Exit(runServiceCmd(project, args[1:], realServiceCmdDeps(host), os.Stdout, os.Stderr))
	}
	if len(args) >= 1 && args[0] == "down" {
		os.Exit(runDown(args[1:], realServiceCmdDeps(host), os.Stdout, os.Stderr))
	}
	// version reads a compile-time constant — no project, no broker. Handle it
	// before the project check and before bus.Connect.
	if len(args) >= 1 && args[0] == "limits" {
		out, _ := json.MarshalIndent(bus.Limits(), "", "  ")
		fmt.Println(string(out))
		return
	}
	// quota and statusline read local files only: no project, no broker.
	if len(args) >= 1 && args[0] == "quota" {
		os.Exit(runQuota(args[1:], os.Stdout, os.Stderr))
	}
	if len(args) >= 1 && args[0] == "statusline" {
		os.Exit(runStatusline(args[1:], os.Stdin, os.Stdout))
	}
	if len(args) >= 1 && args[0] == "version" {
		fmt.Printf("agentbus protocol v%d\nagentbus version %s\n", bus.ProtocolVersion, buildinfo.Version)
		return
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}

	self := envOr("AGENT_BUS_AGENT", "hermes")
	if err := identityGuard(os.Getenv, args[0], args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentbus: "+err.Error())
		os.Exit(2)
	}

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
		var to []string
		for {
			var t string
			if rest, t = extractFlag(rest, "--to"); t == "" {
				break
			}
			to = append(to, t)
		}
		if len(rest) < 2 {
			die("usage: report <agent> [--auto] [--to <agent>]... <message>")
		}
		for _, t := range to {
			if !bus.ValidName(t) {
				die(fmt.Sprintf("report: --to %q: invalid agent name", t))
			}
		}
		kind := bus.ReportNote
		if auto {
			kind = bus.ReportAuto
		}
		msg := strings.Join(rest[1:], " ")
		if note := reportShapeNote(msg); note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
		id, err := b.Report(ctx, rest[0], kind, msg)
		if err != nil {
			die(err.Error())
		}
		// A report wakes nobody; --to also tells the agents waiting for it.
		for _, t := range to {
			if _, err := b.Cmd(ctx, rest[0], t, bus.CmdDirective, id, reportNotice(id, msg)); err != nil {
				die(fmt.Sprintf("report %s published, but telling %s failed: %v", id, t, err))
			}
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
		rest, subject := extractFlag(rest, "--subject")
		if len(rest) < 2 {
			die("usage: challenge <target> [--ref R] [--subject S] <message>")
		}
		if subject != "" {
			if err := bus.ValidGateSubject(subject); err != nil {
				die(err.Error())
			}
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
		if err := b.OpenChallenge(ctx, target, ref, bus.FormatGateMeta(self, subject, msg)); err != nil {
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
		authorProv, selfProv := verdictProviders(ctx, b, author, self)
		if _, err := b.AppendVerdict(ctx, bus.Verdict{
			Subject: subject, Author: author, Reviewer: self,
			Decision: decision, Message: rationale, Ref: ref,
			AuthorProvider: authorProv, ReviewerProvider: selfProv,
		}); err != nil {
			die(err.Error())
		}
		// 2. Live notification on :cmd so busmon and the author see it (as before).
		if _, err := b.Cmd(ctx, self, author, bus.CmdVerdict, ref, cmdMsg); err != nil {
			die(err.Error())
		}
		// 3. Gate resolution, best-effort: the gates opened on this reviewer for
		//    the subject or the ref, else the author's gate named by --ref. A
		//    missing/stale ref is a notice, not a fatal error — that is what lets
		//    a cmd-requested review (no challenge) still be recorded.
		for _, msg := range verdictGates(ctx, b, author, self, subject, ref) {
			fmt.Fprintln(os.Stderr, msg)
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
		if len(rest) >= 1 && rest[0] == "close" {
			os.Exit(runGateClose(ctx, b, self, rest[1:], os.Stdout, os.Stderr))
		}
		if len(rest) < 1 {
			die("usage: gate <agent> | gate close …")
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

	case "ask":
		os.Exit(runAsk(ctx, b, rest, os.Stdin, os.Stdout, os.Stderr))

	case "asks":
		os.Exit(runAsks(ctx, b, rest, os.Stdout, os.Stderr))

	case "answer":
		os.Exit(runAnswer(ctx, b, rest, os.Getenv, os.Stdout, os.Stderr))

	case "swap":
		os.Exit(runSwap(ctx, b, realSwapDeps(os.Environ()), self, rest, os.Stdout, os.Stderr))

	case "health":
		os.Exit(runHealth(ctx, b, self, rest, os.Stdout, os.Stderr))

	case "roles": // only doctor and adopt reach here (they need the project's bus)
		if rest[0] == "adopt" {
			os.Exit(runAdopt(ctx, b, self, rest[1:], os.Environ(), os.Stdout, os.Stderr))
		}
		os.Exit(runDoctor(ctx, b, realDoctorDeps(os.Environ()), rest[1:], os.Stdout, os.Stderr))

	case "retire":
		os.Exit(runRetire(ctx, b, realSwapDeps(os.Environ()), self, rest, os.Stdout, os.Stderr))

	case "swaps":
		os.Exit(runSwaps(ctx, b, rest, os.Stdout, os.Stderr))

	case "plan":
		wd, _ := os.Getwd()
		os.Exit(runPlan(ctx, b, wd, rest, os.Stdout, os.Stderr))

	case "pause", "resume":
		d := pauseDeps{Set: b.SetPause, Get: b.Pause, Resume: b.Resume, Budgets: b.Budgets, Now: time.Now, Env: os.Getenv}
		if cmd == "pause" {
			os.Exit(runPause(ctx, d, self, rest, os.Stdout, os.Stderr))
		}
		os.Exit(runResume(ctx, d, self, rest, os.Stdout, os.Stderr))

	case "agents":
		if len(rest) > 0 && rest[0] == "wait" {
			os.Exit(runAgentsWait(ctx, b.Agents, time.Sleep, rest[1:], os.Stdout, os.Stderr))
		}
		if len(rest) > 0 && rest[0] == "forget" {
			os.Exit(runAgentsForget(ctx, forgetDeps{Agents: b.Agents, Armed: func(ctx context.Context, a string) (bool, error) {
				m, err := b.ArmedAgents(ctx)
				_, ok := m[a]
				return ok, err
			}, Forget: b.ForgetAgent, Now: time.Now}, rest[1:], os.Stdout, os.Stderr))
		}
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

		// v0.13: the dispatch service publishes every provider's windows from
		// first-party readers (agentbus quota); refresh no longer reads
		// ccstatusline nor writes {p}:budget.

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
			fmt.Printf("refresh: %d agent snapshot(s)\n", len(snaps))
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
		fromNow := false
		floor := since
		switch {
		case floor == "":
			fromNow = true
			f, ferr := b.ServerFloor(ctx)
			if ferr != nil {
				die("could not resolve server-time floor: " + ferr.Error())
			}
			floor = f
		case floor != "0" && !strings.Contains(floor, "-"):
			floor += "-0" // accept a bare <ms> cursor
		}
		os.Exit(runSubscribe(ctx, b, agent, consumer, idle, floor, fromNow, loop, os.Stdout))

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
		Pane:     os.Getenv("HERDR_PANE_ID"),
		Session:  os.Getenv("CLAUDE_CODE_SESSION_ID"),
		Client:   os.Getenv("AGENT_BUS_CLIENT"),
		Provider: bus.ProviderFor(os.Getenv("AGENT_BUS_CLIENT"), os.Getenv("AGENT_BUS_PROVIDER")),
		Model:    os.Getenv("AGENT_BUS_MODEL"),
		ClientFP: os.Getenv("AGENT_BUS_CLIENT_FP"),
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
