package main

import "github.com/netbja/agent-bus-monitor/cli"

// globalFlags are read before the command, in every command.
var globalFlags = []cli.Flag{
	{Long: "project", Short: 'p', Arg: "<project>", Help: "project namespace (or AGENT_BUS_PROJECT)"},
	{Long: "host", Short: 'H', Arg: "<host>", Help: "Redis host (overrides REDIS_HOST)"},
}

// Recurring flags, one meaning wherever they appear.
var (
	fJSON  = cli.Flag{Long: "json", Short: 'j', Help: "print JSON"}
	fRef   = cli.Flag{Long: "ref", Short: 'r', Arg: "<id>", Help: "thread or challenge id"}
	fForce = cli.Flag{Long: "force", Short: 'f', Help: "do it despite the safety checks"}
	fEsc   = cli.Flag{Long: "escalate-to", Short: 'e', Arg: "<agent>", Help: "who receives escalations (default master)"}
)

// commands is the one table behind `agentbus help`, `help <cmd>`,
// `<cmd> --help` and the short flags. Every name main() dispatches has an
// entry (TestEveryDispatchedCommandIsInTheTable).
var commands = []cli.Command{
	// --- publishing and reading
	{
		Name: "status", Text: true, Usage: []string{"status <agent> <working|idle|blocked|waiting|done> [message...]"},
		Summary:  "declare your state; it is also your heartbeat",
		Examples: []string{"agentbus -p demo status coder working \"task 3: parser\""},
	},
	{
		Name: "report", Text: true, Usage: []string{"report <agent> [--auto] [--to <agent>]... <message...>"},
		Summary: "publish a report (full text kept); --to also tells who waits for it",
		Flags: []cli.Flag{
			{Long: "auto", Short: 'a', Help: "mark it automatic, not a note"},
			{Long: "to", Short: 't', Arg: "<agent>", Help: "also send a directive pointing at it (repeatable)"},
		},
		Examples: []string{"agentbus -p demo report coder -t master \"task 3 done\""},
	},
	{
		Name: "reports", Usage: []string{"reports [--json] [<id>]"},
		Summary:  "list recent reports, or print one report's full text",
		Flags:    []cli.Flag{fJSON},
		Examples: []string{"agentbus -p demo reports", "agentbus -p demo reports 1792537337865-0"},
	},
	{
		Name: "notify", Text: true, Usage: []string{"notify <message...>"},
		Summary:  "post a notice for the record; wakes nobody",
		Examples: []string{"agentbus -p demo notify \"broker restarted\""},
	},
	{
		Name: "cmd", Text: true, Usage: []string{"cmd [--ref <id>] [--no-wake] <target> <command...>"},
		Summary: "send a directive; prints its id (the thread root)",
		Flags: []cli.Flag{
			fRef,
			{Long: "no-wake", Short: 'n', Help: "deliver without waking the target"},
		},
		Examples: []string{"agentbus -p demo cmd coder \"run the suite\"", "agentbus -p demo cmd -r 1792537337865-0 coder \"and the vet\""},
	},
	{
		Name: "thread", Usage: []string{"thread <thread-id>"},
		Summary:  "show every :cmd entry of one thread, oldest first",
		Examples: []string{"agentbus -p demo thread 1792537337865-0"},
	},
	{
		Name: "listen", Usage: []string{"listen [status] [report] [notify] [cmd]"},
		Summary:  "tail the streams live (debugging)",
		Examples: []string{"agentbus -p demo listen cmd"},
	},
	{
		Name: "subscribe", Aliases: []string{"watch"},
		Usage:   []string{"subscribe [--loop] [--since <cursor>] <agent> [idle_secs]", "subscribe --drain <agent>"},
		Summary: "wait for one addressed command, print it as JSON, exit; --drain empties the queue",
		Flags: []cli.Flag{
			{Long: "since", Short: 's', Arg: "<cursor>", Help: "floor: the id of the last event handled (0 = all retained)"},
			{Long: "drain", Short: 'd', Help: "print every waiting entry as JSONL and exit (what a woken agent runs)"},
			{Long: "loop", Short: 'l', Help: "keep reading (headless use)"},
		},
		Examples: []string{"agentbus -p demo subscribe --drain coder", "agentbus -p demo subscribe -s 1792537337865-0 coder 300"},
	},

	// --- 4-eyes
	{
		Name: "challenge", Text: true, Usage: []string{"challenge <target> [--ref <id>] [--subject <s>] <message...>"},
		Summary: "open a 4-eyes gate on an agent (the gated agent's verdict on --subject closes it)",
		Flags: []cli.Flag{fRef,
			{Long: "subject", Short: 's', Arg: "<s>", Help: "the verdict subject the review is for (no spaces)"}},
		Examples: []string{"agentbus -p demo challenge coder \"PR 12 touches the money path\"",
			"agentbus -p demo challenge foureyes -s roles-impl \"review roles-impl at 27233fb\""},
	},
	{
		Name: "reply", Text: true, Usage: []string{"reply --ref <id> <target> <message...>"},
		Summary:  "answer on a thread",
		Flags:    []cli.Flag{fRef},
		Examples: []string{"agentbus -p demo reply -r 1792537337865-0 master \"yes, on main\""},
	},
	{
		Name: "verdict", Text: true, Usage: []string{"verdict (--pr <n> | --subject <s>) [--ref <id>] <author> <approve|reject> [message...]"},
		Summary: "record a 4-eyes verdict in the ledger (resolves a matching gate)",
		Flags: []cli.Flag{
			{Long: "pr", Short: 'P', Arg: "<n>", Help: "the pull request number"},
			{Long: "subject", Short: 's', Arg: "<s>", Help: "any other subject"},
			fRef,
		},
		Examples: []string{"agentbus -p demo verdict -P 12 coder approve \"tests cover the edge\""},
	},
	{
		Name: "verdicts", Usage: []string{"verdicts [--json] [--pr <n> | --subject <s>]"},
		Summary: "4-eyes state of a subject (exit 0 approved, 2 pending, 3 rejected), or recent verdicts",
		Flags: []cli.Flag{
			{Long: "pr", Short: 'P', Arg: "<n>", Help: "the pull request number"},
			{Long: "subject", Short: 's', Arg: "<s>", Help: "any other subject"},
			fJSON,
		},
		Examples: []string{"agentbus -p demo verdicts -P 12", "agentbus -p demo verdicts"},
	},
	{
		Name: "gate", Usage: []string{"gate <agent>", "gate close <agent> <ref> --note <text> [--as <person>]",
			"gate close (--subject <s> | --mine | --by <name>) [--dry-run] [--note <text>] [--as <person>]"},
		Summary: "list an agent's open challenges, or close stale ones without a verdict (its challenger or a person)",
		Flags: []cli.Flag{
			{Long: "note", Short: 'N', Arg: "<text>", Help: "why it closes (the audit trace; required unless --dry-run)"},
			{Long: "subject", Short: 's', Arg: "<s>", Help: "every gate opened for this subject"},
			{Long: "mine", Short: 'm', Help: "every gate you opened"},
			{Long: "by", Short: 'b', Arg: "<name>", Help: "every gate opened by name"},
			{Long: "dry-run", Short: 'n', Help: "list what would close, write nothing"},
			{Long: "as", Short: 'a', Arg: "<person>", Help: "close as a person (any gate; never implied by the environment)"},
		},
		Examples: []string{"agentbus -p demo gate coder", "agentbus -p demo gate close --mine -n",
			"agentbus -p demo gate close foureyes 1793128514117-0 -N \"merged as #135\""},
	},

	// --- team state
	{
		Name: "pilot", Usage: []string{"pilot <claim|renew|release|status> [--ttl <dur>]"},
		Summary:  "the pilot lease: who drives the panes",
		Flags:    []cli.Flag{{Long: "ttl", Short: 't', Arg: "<dur>", Help: "lease duration (default 90s)"}},
		Examples: []string{"agentbus -p demo pilot status", "agentbus -p demo pilot claim -t 5m"},
	},
	{
		Name: "agents", Usage: []string{"agents [--json]", "agents forget <agent>... [--force]",
			"agents wait <agent> [--since <unix-ms>] [--timeout <dur>]"},
		Summary: "every agent's declared state; forget retires gone agents; wait for an agent's first status",
		Flags: []cli.Flag{fJSON, fForce,
			{Long: "since", Arg: "<unix-ms>", Help: "wait: only a status at or after this time counts"},
			{Long: "timeout", Arg: "<dur>", Help: "wait: how long to wait (default 30s)"}},
		Examples: []string{"agentbus -p demo agents", "agentbus -p demo agents forget old-coder"},
	},
	{
		Name: "pane", Usage: []string{"pane <agent>"},
		Summary:  "print an agent's herdr pane",
		Examples: []string{"agentbus -p demo pane coder"},
	},
	{
		Name: "bind", Usage: []string{"bind <agent> [--pane <pane>] [--pid <pid>]"},
		Summary: "bind a verified pane identity (lists candidates without --pid)",
		Flags: []cli.Flag{
			{Long: "pane", Short: 'P', Arg: "<pane>", Help: "herdr pane id"},
			{Long: "pid", Short: 'i', Arg: "<pid>", Help: "the client's process id"},
		},
		Examples: []string{"agentbus -p demo bind foureyes -P w1-3 -i 41235"},
	},
	{
		Name: "usage", Usage: []string{"usage [--json]", "usage <agent> <json>"},
		Summary:  "per-agent model and context, or write one snapshot",
		Flags:    []cli.Flag{fJSON},
		Examples: []string{"agentbus -p demo usage"},
	},
	{
		Name: "budget", Usage: []string{"budget [--json]", "budget <provider> <json>"},
		Summary:  "account usage windows per provider, or publish one",
		Flags:    []cli.Flag{fJSON},
		Examples: []string{"agentbus -p demo budget"},
	},
	{
		Name: "board", Usage: []string{"board [--json]", "board claim <task> [--branch <b>]", "board state <task> <state> | board done <task> | board drop <task>"},
		Summary: "the shared task board: who owns what",
		Flags: []cli.Flag{
			fJSON,
			{Long: "branch", Short: 'b', Arg: "<b>", Help: "the task's branch"},
		},
		Examples: []string{"agentbus -p demo board", "agentbus -p demo board claim parser -b coder/parser"},
	},
	{
		Name: "request", Text: true, Usage: []string{
			"request [--json]",
			"request send <task> <target> [--ref <id>] [--ttl <dur>] <body...>",
			"request accept <task> | request block <task> <reason...>",
			"request done <task> [--commit <sha>] [--branch <b>] <reply...>",
		},
		Summary: "tracked requests: sent, accepted, blocked, done",
		Flags: []cli.Flag{
			fJSON, fRef,
			{Long: "ttl", Short: 't', Arg: "<dur>", Help: "deadline for the first acceptance"},
			{Long: "commit", Short: 'c', Arg: "<sha>", Help: "the delivered commit (missions)"},
			{Long: "branch", Short: 'b', Arg: "<b>", Help: "the delivered branch"},
		},
		Examples: []string{"agentbus -p demo request accept parser", "agentbus -p demo request done parser \"merged in #12\""},
	},
	{
		Name: "shutdown", Usage: []string{"shutdown [--force]"},
		Summary:  "tell every agent the work is over (refused while work is in flight)",
		Flags:    []cli.Flag{fForce},
		Examples: []string{"agentbus -p demo shutdown"},
	},
	{
		Name: "refresh", Usage: []string{"refresh [--quiet]"},
		Summary:  "read local transcripts and publish each agent's usage",
		Flags:    []cli.Flag{{Long: "quiet", Short: 'q', Help: "print nothing"}},
		Examples: []string{"agentbus -p demo refresh -q"},
	},
	{
		Name: "health", Text: true, Usage: []string{"health [list | detail <id> | log [<after-id>] | note <id> <text...>]"},
		Summary:  "the project's measured health and its notes",
		Examples: []string{"agentbus -p demo health", "agentbus -p demo health detail services"},
	},
	{
		Name: "pause", Text: true, Usage: []string{"pause --for <dur> | --until <HH:MM> | --until-reset [--to <agent>] [reason...]", "pause --status"},
		Summary: "nobody is woken until then",
		Flags: []cli.Flag{
			{Long: "for", Short: 'f', Arg: "<dur>", Help: "pause for a duration"},
			{Long: "until", Short: 'u', Arg: "<HH:MM>", Help: "pause until a local time"},
			{Long: "until-reset", Short: 'R', Help: "pause until the coordinator's quota resets"},
			{Long: "to", Short: 't', Arg: "<agent>", Help: "who gets the resume directive (default master)"},
			{Long: "status", Short: 's', Help: "print the current pause"},
		},
		Examples: []string{"agentbus -p demo pause -f 2h \"release window\"", "agentbus -p demo pause -s"},
	},
	{
		Name: "resume", Text: true, Usage: []string{"resume [message...]"},
		Summary:  "end a pause now",
		Examples: []string{"agentbus -p demo resume"},
	},
	{
		Name: "ask", Usage: []string{"ask <agent> [--file <questions.json> | < questions.json] [--replace <id>] [--task <plan-task>]", "ask <agent> --withdraw <id> [--question <qid> [--note <text>]]"},
		Summary: "put structured questions to a person",
		Flags: []cli.Flag{
			{Long: "file", Short: 'F', Arg: "<file>", Help: "the questions (JSON); default stdin"},
			{Long: "replace", Short: 'r', Arg: "<id>", Help: "replace an open ask"},
			{Long: "task", Short: 't', Arg: "<plan-task>", Help: "the plan task this ask is about (busmon's Plan view)"},
			{Long: "withdraw", Short: 'w', Arg: "<id>", Help: "withdraw an ask, or one question of it"},
			{Long: "question", Short: 'Q', Arg: "<qid>", Help: "the question to withdraw"},
			{Long: "note", Short: 'N', Arg: "<text>", Help: "why it is withdrawn"},
		},
		Examples: []string{"agentbus -p demo ask master -F questions.json"},
	},
	{
		Name: "asks", Usage: []string{"asks [--open] [--json]"},
		Summary:  "list asks and their answers",
		Flags:    []cli.Flag{{Long: "open", Short: 'o', Help: "only open ones"}, fJSON},
		Examples: []string{"agentbus -p demo asks -o"},
	},
	{
		Name: "answer", Text: true, Usage: []string{"answer <ask-id> <question> (--choice <label> | --text <words...>) [--as <person>]"},
		Summary: "answer one question of an ask",
		Flags: []cli.Flag{
			{Long: "choice", Short: 'c', Arg: "<label>", Help: "pick an offered choice"},
			{Long: "text", Short: 'T', Help: "answer with the remaining words"},
			{Long: "as", Short: 'a', Arg: "<person>", Help: "who answers (default your identity)"},
		},
		Examples: []string{"agentbus -p demo answer 1792537337865-0 q1 -c yes"},
	},

	// --- services, dispatch, missions, swaps
	{
		Name: "dispatch", Usage: []string{"dispatch [--horizon <dur>] [--escalate-after <dur>] [--escalate-to <agent>]", "dispatch --status"},
		Summary: "the wake dispatcher, in the foreground (the service runs it for you)",
		Flags: []cli.Flag{
			{Long: "horizon", Short: 'z', Arg: "<dur>", Help: "older entries are stale (default 24h)"},
			{Long: "escalate-after", Short: 'A', Arg: "<dur>", Help: "escalate an agent still unwoken after this (default 10m)"},
			fEsc,
			{Long: "status", Short: 's', Help: "print dispatch up (exit 0) or down (exit 1)"},
		},
		Examples: []string{"agentbus -p demo dispatch -s"},
	},
	{
		Name: "missions", Usage: []string{"missions [--repo <dir>] [--escalate-to <agent>]", "missions --status"},
		Summary: "the missions conductor, in the foreground (the service runs it for you)",
		Flags: []cli.Flag{
			{Long: "repo", Short: 'C', Arg: "<dir>", Help: "the repository (default the working directory)"},
			fEsc,
			{Long: "status", Short: 's', Help: "print whether the conductor runs"},
		},
		Examples: []string{"agentbus -p demo missions -s"},
	},
	{
		Name: "mission", Usage: []string{
			"mission load <path>@<commit> [--force <reason> | --recover] [--wait <dur>]",
			"mission retry <change>/<id> | mission reassign <change>/<id> <agent>",
			"mission skip <change>/<id> --reason <text> | mission abort <change> --reason <text>",
			"mission status [<change>] | mission show <change>/<id>",
		},
		Summary: "operate missions (queued for the conductor)",
		Flags: []cli.Flag{
			{Long: "force", Short: 'f', Arg: "<reason>", Help: "load over a live change"},
			{Long: "recover", Short: 'R', Help: "rebuild state from merge trailers"},
			{Long: "wait", Short: 'w', Arg: "<dur>", Help: "how long to wait for the conductor (default 30s)"},
			{Long: "reason", Short: 'r', Arg: "<text>", Help: "why (skip, abort)"},
		},
		Examples: []string{"agentbus -p demo mission load docs/changes/x/missions.json@HEAD", "agentbus -p demo mission status"},
	},
	{
		Name: "swap", Usage: []string{"swap <role> (--profile <profile> | --back) [--reason <text>] [--now] [--force]"},
		Summary: "replace an agent by its role on a fallback profile, or back",
		Flags: []cli.Flag{
			{Long: "profile", Short: 'P', Arg: "<profile>", Help: "one of the role's fallback profiles"},
			{Long: "back", Short: 'b', Help: "return to the role's own profile"},
			{Long: "reason", Short: 'r', Arg: "<text>", Help: "recorded in the ledger"},
			{Long: "now", Short: 'n', Help: "even during a running turn"},
			fForce,
		},
		Examples: []string{"agentbus -p demo swap coder -P codex"},
	},
	{
		Name: "retire", Usage: []string{"retire <role> [--reason <text>] [--now]"},
		Summary: "close one role's tab, with the swap checks",
		Flags: []cli.Flag{
			{Long: "reason", Short: 'r', Arg: "<text>", Help: "recorded in the ledger"},
			{Long: "now", Short: 'n', Help: "even during a running turn"},
		},
		Examples: []string{"agentbus -p demo retire architect"},
	},
	{
		Name: "plan", Text: true, Usage: []string{
			"plan [show] [--json]",
			"plan load <path>@<commit>",
			"plan link <task> --pr <n> | plan done <task> | plan note <task> <text...> | plan clear",
		},
		Summary: "the batch's plan, as busmon's Plan view (P) shows it",
		Flags: []cli.Flag{
			fJSON,
			{Long: "pr", Arg: "<n>", Help: "link: the task's pull request"},
		},
		Examples: []string{"agentbus -p demo plan load docs/plans/v0.22.json@HEAD", "agentbus -p demo plan link roles --pr 102", "agentbus -p demo plan done roles"},
	},
	{
		Name: "swaps", Usage: []string{"swaps [--role <role>] [--json]"},
		Summary:  "the swap ledger",
		Flags:    []cli.Flag{{Long: "role", Short: 'R', Arg: "<role>", Help: "only this role"}, fJSON},
		Examples: []string{"agentbus -p demo swaps"},
	},
	{
		Name: "up", NoProject: true,
		Usage:   []string{"up <project> [<path>] [--as <name>] [--tabs] [--resume] [--dry-run] [--escalate-to <agent>] [bootstrap flags]"},
		Summary: "provision a project, start its services, open its herdr workspace",
		Flags: []cli.Flag{
			{Long: "as", Short: 'a', Arg: "<name>", Help: "your name in busmon"},
			{Long: "tabs", Short: 'T', Help: "run dispatch and missions in tabs, not as services"},
			{Long: "resume", Short: 'R', Help: "resume the agents' sessions"},
			{Long: "dry-run", Short: 'n', Help: "print what would happen"},
			fEsc,
			{Long: "session", Short: 'S', Arg: "<s>", Help: "herdr session (default HERDR_SESSION)"},
			{Long: "no-dispatch", Help: "bootstrap: no dispatcher"},
			{Long: "no-missions", Help: "bootstrap: no conductor"},
			{Long: "index", Help: "bootstrap: index the repository"},
			{Long: "cron", Help: "bootstrap: install the sentinel's cron"},
			{Long: "yes", Help: "bootstrap: no confirmation (-y)"},
		},
		Examples: []string{"agentbus up demo ~/src/demo", "agentbus up demo -n"},
	},
	{
		Name: "down", NoProject: true, Usage: []string{"down <project>"},
		Summary:  "stop a project's services; deletes nothing",
		Examples: []string{"agentbus down demo"},
	},
	{
		Name: "service", Usage: []string{
			"service status|start|stop|restart|remove [dispatch|missions]",
			"service logs [dispatch|missions] [-n 50]",
			"service list | service remove --all",
		},
		Summary: "the project's user services (systemd --user, launchd)",
		Flags: []cli.Flag{
			{Long: "all", Short: 'a', Help: "remove: every project's services"},
			{Long: "env", Arg: "<file>", Help: "service run (units only): the environment file"},
			{Long: "stderr", Help: "service run (units only): log to stderr"},
		},
		Examples: []string{"agentbus -p demo service status", "agentbus -p demo service logs dispatch -n 100", "agentbus service list"},
	},

	// --- no project, no broker
	{
		Name: "projects", NoProject: true, Usage: []string{"projects [--json]"},
		Summary:  "every known project: templates, broker, services, agents",
		Flags:    []cli.Flag{fJSON},
		Examples: []string{"agentbus projects"},
	},
	{
		Name: "tokens", NoProject: true, Usage: []string{"tokens [--project <p> | --all] [--days N] [--by agent|day] [--json]", "tokens --unattributed [--json]"},
		Summary: "tokens spent per project (all side by side), per agent or per day; no cost, by rule",
		Flags: []cli.Flag{{Long: "all", Help: "every project side by side (the default; only --project named on the command line narrows it, never AGENT_BUS_PROJECT)"},
			{Long: "days", Arg: "<N>", Help: "days of the chart, 1 to 8 (default 7)"},
			{Long: "by", Arg: "agent|day", Help: "one project by agent (today) or by day"},
			{Long: "unattributed", Help: "what no project's session record names: a read-only scan of this host's transcripts"}, fJSON},
		Examples: []string{"agentbus tokens", "agentbus tokens --project agent-bus-monitor --by agent", "agentbus tokens --unattributed"},
	},
	{
		Name: "identity", NoProject: true, Usage: []string{"identity [set <name>]"},
		Summary:  "your name as a person in busmon",
		Examples: []string{"agentbus identity set bernard"},
	},
	{
		Name: "quota", NoProject: true, Usage: []string{"quota [--json]"},
		Summary:  "every provider's usage windows, read now from local sources",
		Flags:    []cli.Flag{fJSON},
		Examples: []string{"agentbus quota"},
	},
	{
		Name: "models", NoProject: true, Usage: []string{"models [--refresh] [--provider <p>] [--json]"},
		Summary: "this machine's model table",
		Flags: []cli.Flag{
			{Long: "refresh", Short: 'r', Help: "read every client's catalogue again"},
			{Long: "provider", Short: 'P', Arg: "<p>", Help: "only this provider"},
			fJSON,
		},
		Examples: []string{"agentbus models -r"},
	},
	{
		Name: "roles", NoProject: true,
		Usage: []string{
			"roles doctor [--json] [--publish]   (needs --project)",
			"roles fallbacks <role> [--json]",
			"roles reviewers [--json]   (models of other providers than coder's that can review)",
			"roles skill-users <skill>...   (the roles to tell when those skills change)",
			"roles draft <role> --task <words...> [--out <dir>] [--force]   (a role drafted for a task: inert)",
			"roles adopt <role> --ask <id> [--from <dir>]   (needs --project; only after the person answered adopt)",
			"roles validate|exists|tier|field|launch|install|template|check-toml ...   (scripts)",
		},
		Summary: "the role manifest; doctor compares configured, launched and used models",
		Flags: []cli.Flag{
			fJSON,
			{Long: "publish", Short: 'u', Help: "doctor: leave verdicts for busmon"},
			{Long: "missing-ok", Help: "install: a missing skill is not an error"},
			{Long: "task", Arg: "<words...>", Help: "draft: what the role is for (the rest of the line)"},
			{Long: "out", Arg: "<dir>", Help: "draft: where the draft goes (default .agent-bus/drafts)"},
			{Long: "force", Help: "draft: replace an existing draft"},
			{Long: "ask", Arg: "<id>", Help: "adopt: the ask the person answered 'adopt'"},
			{Long: "from", Arg: "<dir>", Help: "adopt: the drafts directory (default .agent-bus/drafts)"},
		},
		Examples: []string{"agentbus -p demo roles doctor", "agentbus roles fallbacks coder"},
	},
	{
		Name: "setup", NoProject: true, Internal: true,
		Usage:   []string{"setup [--repo <dir>] install|update|uninstall|status [setup.sh's flags]"},
		Summary: "the installer in Go, run by scripts/setup-go.sh (its flags and help are setup.sh's)",
		Flags: []cli.Flag{
			{Long: "repo", Arg: "<dir>", Help: "the checkout to install from (the thin script passes it)"},
		},
		Examples: []string{"agentbus setup --repo ~/src/agent-bus-monitor install --dry-run"},
	},
	{
		Name: "mem", NoProject: true,
		Usage: []string{
			"mem check [--stale-after <dur>] [--strict]",
			"mem new <lesson|decision|question> <slug> --title <title> [--scope <scope>] [--note-project <p>]",
			"mem sync   (all: [--root <dir>])",
		},
		Summary: "the repository's shared memory: check, new note, sync AGENTS.md",
		Flags: []cli.Flag{
			{Long: "root", Short: 'C', Arg: "<dir>", Help: "the repository (default the working directory)"},
			{Long: "stale-after", Short: 'S', Arg: "<dur>", Help: "check: a note older than this is stale (default 60d)"},
			{Long: "strict", Short: 'x', Help: "check: warnings fail"},
			{Long: "title", Short: 'T', Arg: "<title>", Help: "new: the note's title"},
			{Long: "scope", Short: 's', Arg: "<scope>", Help: "new: project or transferable"},
			{Long: "note-project", Short: 'N', Arg: "<p>", Help: "new: the project it concerns"},
		},
		Examples: []string{"agentbus mem check", "agentbus mem new lesson cursor-floor -T \"A floor discards\""},
	},
	{
		Name: "limits", NoProject: true, Usage: []string{"limits"},
		Summary:  "the writers' size and retention limits",
		Examples: []string{"agentbus limits"},
	},
	{
		Name: "version", NoProject: true, Usage: []string{"version"},
		Summary:  "the protocol version and the build",
		Examples: []string{"agentbus version"},
	},
	{
		Name: "help", NoProject: true, Usage: []string{"help [<command>]"},
		Summary:  "this list, or one command's usage, flags and examples",
		Examples: []string{"agentbus help", "agentbus help subscribe", "agentbus subscribe --help"},
	},

	// --- internal entry points: long flags only, no expansion
	{
		Name: "plugin", NoProject: true, Internal: true,
		Usage:    []string{"plugin projects|busmon|busmon-pane|workspace-event"},
		Summary:  "herdr's entry points",
		Examples: []string{"agentbus plugin projects"},
	},
	{
		Name: "statusline", NoProject: true, Internal: true,
		Usage:   []string{"statusline [--then <command>]", "statusline --print-install [--then <command>]"},
		Summary: "Claude Code's statusLine: records rate limits from stdin",
		Flags: []cli.Flag{
			{Long: "then", Arg: "<command>", Help: "the status line command to run after"},
			{Long: "print-install", Help: "print the settings.json snippet"},
		},
		Examples: []string{"agentbus statusline --print-install"},
	},
}
