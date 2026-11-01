# Changelog

One entry per release. The full notes for each are in [`docs/releases/`](docs/releases/).
Update a machine with `./setup.sh update`; busmon shows `↑ vX.Y.Z` in its header when a newer
release exists.

## Unreleased

- The project services and the writers read `config.toml` too, below the environment, each with its
  own rule unchanged: `[dispatch]` `context_alert`, `prompt_grace`, `quota`, `quota_warn`, `quota_hold`,
  `quota_max_age`, `quota_long_hold`, `tokens`, `auto_clear`; `[bus]` `report_max`, `report_full_max`,
  `verdict_max` (per writer); `[health.runaway]` `silent_after`, `context_growth`, `growth_span`,
  `screen_repeats`, which a service could never get from the shell (`agentbus up` does not copy them).
  A service reads the `config.toml` of the `XDG_CONFIG_HOME` its env file names, at its next start.
  A bad value falls back to the next source and is said once: in the service's log at its start, on
  stderr for an `agentbus` command (never the value; a service never stops over it).
- `agentbus identity set <name>` records the name in `config.toml` (append-only, as before), and a
  name already in `busmon.toml` no longer refuses it: the new one wins, and the command says the old one
  is shadowed. `agentbus identity` and `agentbus up`'s default `--as` read `config.toml`, then
  `busmon.toml`; `agentbus identity` names the file on stderr. The settings files are found through
  `$XDG_CONFIG_HOME` or `$HOME` only when absolute, as the password file and the services already are
  (a relative `XDG_CONFIG_HOME`, which busmon used to accept, is now ignored, as the XDG spec says).
- busmon reads its settings from `~/.config/agentbus/config.toml` (or `$XDG_CONFIG_HOME/agentbus/`),
  key by key above `busmon.toml`, which is still read: flag, then environment, then `config.toml`, then
  `busmon.toml`, then the default. New key `[updates] check = false` turns the update check off, like
  `AGENT_BUS_NO_UPDATE_CHECK=1`. A file problem is still a note, now naming the file, line and key and
  never the value. One change: a negative `display.limit` in a file now replays all history, as the
  flag and the environment always did, instead of falling back to 25. Nothing changes without a file.
- Docs for the public release: a French README (`README.fr.md`), a "First run" section (name rule,
  the client's trust dialog, busmon without a project, an empty team, the `anonymous` default sender),
  the busmon notice line, and where pull requests go (GitHub today; GitLab, Bitbucket and a local mode
  planned for v0.26), in the README and both operational guides.
- busmon: a quiet line at the right end of the footer for what happened by itself while it runs: a
  pause the dispatcher or an agent set (a quota pause in orange), the project resumed, an automatic
  `/clear`, a reload begun, done or failed, a swap to a fallback or back, a health check worsening or
  recovering (a fail mended with a sentinel note since shows that note, `sentinel: <note>`). One at a time, 4 s (2 s while others wait,
  past five the rest in one `N more automatic actions (R: recap)` line), fading out over its last second
  in true colour; no bell, no mark, no count, never the person's own act, never an ask or a message.
  The hints give way from the right; a flash or a dialog holds it back, and one already on screen keeps the
  time it had left.
- `busmon` without a project now says `--project <p>` (it said `-project`) and points to `busmon --list`
  and `busmon --pick`.
- `agentbus up` with an invalid project name (`MyProject`) now says why it is refused and what is
  allowed, and suggests the lowercase name with the rest of the command, instead of only its usage line.
- `agentbus up` no longer prints bootstrap's `recall: … open it with agentbus up <p>` hint, which told the
  person to run the command they had just run (`bootstrap recall` run by hand still prints it).
- `agentbus agents` on a project no agent has joined yet says so instead of printing nothing.
- busmon: `←` and `→` step the lower panel's tabs (Activity, Board, Requests, Plan) while one of them
  has the focus, wrapping as herdr's tabs do; on an opened page they still step its list. `w` with no
  question open opens the compose window to master (with one open it still opens it, as `!` does).
- The CLI's default sender, when `AGENT_BUS_AGENT` is unset, is now `anonymous` (it was a
  different built-in name).
  The same rules apply: refused as a writer inside a herdr pane. Entries already on a bus keep the
  sender they were written with; nothing migrates.
- `agentbus up` right after a fresh `docker compose up` no longer fails with `marking
  <p>:services: LOADING`: it waits for the broker to answer PING (up to 10 s, only while it is
  loading its data or refusing connections) before marking the project's services.
- `herdr plugin install netbja/agent-bus-monitor` (what herdr's plugin list offers) now stops at
  the plugin's build step with how to install from a clone (`./setup.sh install`), and installs
  nothing. Before, it registered a plugin whose binaries did not exist. `herdr plugin link`, which
  setup uses, is unchanged.

## v0.24.0 — the installer in Go by default, busmon's pages read better, reloads checked first

- `./setup.sh` is now the Go installer (`agentbus setup`), same subcommands and flags; the bash one
  stays one release as `scripts/setup-legacy.sh`; `--force` warns and is ignored.
- `agentbus reload` checks the relaunch before it stops anything, brings a pop role back through
  `agent-spawn`, and an agent's own open question blocks only `--fresh`.
- busmon: the F3 Redis card opens a read-only Redis page, F3 refreshes every 5 s, `←`/`→` move
  between cards; account gauges in eighths of a cell that slide; the header counts what you owe
  and what agents owe each other; pages read `Label:  value`; a plan task waiting for you starts
  with Enter or `s`; the input line and the filter edit at a cursor.
- Health `redis-version`: the broker against the latest Redis release of its own major, checked
  daily; a newer major is only named.
- `{p}:usage` reads only the session proved with the agent's process.
- A new pop role, `infra` (Ansible, Terraform/OpenTofu), adopted: manual profile, approval by the
  person, secrets denied.
- Specs, not built: the feedback loop and one config file. The README and the operational guides
  (EN, FR) cover the release.

## v0.23.0 — what needs you, agents that outlive their context, the installer in Go

- For you lists only the gates you opened or that hold you; a reviewer's verdict closes its own
  gate (with `--ref`, never a newer delta's); `agentbus gate close` and its sweeps close stale
  gates without a verdict.
- busmon: herdr-style lower tabs, a fourth tab Plan (`4`), Enter expands a line under its message
  column, auto-scroll resumes at the bottom, `n`/`p` in the reader, every LLM provider in its own
  colour, and a first-launch tour (F1 then `t` replays it).
- The Requests tab says who owes whom an answer, one row per thread, by section; answered and old
  threads fold (`o`).
- Tokens per project, agent and day: `agentbus tokens` and busmon's `U` page, with the account
  windows' 24-hour history; no cost, by design.
- F3 shows dispatch, missions and the Redis broker as three cards; Enter or `l` opens a log.
- Agent lifecycle: the verified session follows `/clear`; `agentbus handoff` records a hand-off;
  `agentbus reload <role>` restarts an idle agent on its role, resuming its session (sentinel after
  a client update, master for sentinel, a person anyone); the dispatcher's automatic `/clear` after
  a hand-off ships **off** (`AGENT_BUS_AUTO_CLEAR=1`).
- The installer in Go, `scripts/setup-go.sh` (`agentbus setup`), beside `./setup.sh`, which stays
  the installer; it adds a broker ownership check before every compose call.
- No write as the default sender from a herdr pane; a desktop notification for an agent lost to an identity
  mismatch; a pop stuck on its client's dialog is named; health `cmd-delivery` ignores
  unregistered agents.
- A read-only Codex reviewer role, `reviewer-codex`. The README and the operational guides (EN, FR)
  cover everything since the herdr plugin.
- Restart each project's dispatch service after `./setup.sh update`; relaunch each agent for the
  new tool rules (`agentbus --project <p> reload <role> --as <you>`, with `--fresh` for a client
  other than Claude Code, or exit it and `agentbus up <p> --resume`: `up` leaves a live agent
  alone).

[Notes](docs/releases/v0.23.0.md)

## v0.22.0 — busmon on bubbletea, the context alert, specialist roles

- busmon runs on bubbletea v2 + lipgloss v2 (tview is gone). Its look changed: code in overlays
  wraps, digits pick tabs, Enter expands a line then opens the reader, the mouse scrolls, selects
  and focuses; keys, flags and `busmon.toml` are unchanged.
- Context fill per agent from Claude Code's status line (`{p}:context`); at 80 %
  (`AGENT_BUS_CONTEXT_ALERT`) one hand-off directive, a notice and a desktop notification per
  session; busmon's ctx cell shows `88k 85%`; health check `context`.
- A pane stuck on its client's prompt rings the desktop once per episode
  (`AGENT_BUS_PROMPT_GRACE`, default 30 s).
- `agentbus up` closes a relaunched agent's old pane once the new one is proven, if it is still a
  plain shell.
- Built-in quota readers from logs: Kimi Code (`moonshot`) and agy (`google`). The Claude quota no
  longer flaps from an idle session's stale status line.
- The Plan view: `agentbus plan`, derived task states, busmon's `P` overlay and header item.
- Specialist roles (schema 2 profiles), the trust gate in `up`, `roles reviewers`, `roles
  draft`/`adopt`; a grok adapter and the read-only `ui-grok`/`ui-kimi` advisors.
- A profile's permission wins over the client's config; a Claude Code profile without an explicit
  permission (or with `"default"`) is refused at load.
- Claude Code launches report the launcher's resume argv to herdr's session restore.
- Sentinel proposes a client reload after an update; health `client-updates`, busmon flag `update`.
- `agentbus help` and GNU short flags from one table, for both binaries.
- busmon: `for you` hint, one `@` completion, unread marks, a pause popup on send, compose cursor
  and scrollbar fix, messages keep their line breaks.
- Restart each project's dispatch service after `./setup.sh update`; relaunch agents for the
  launcher changes.

[Notes](docs/releases/v0.22.0.md)

## v0.21.0 — startup and relaunch

- `agentbus up <p>` on an open workspace checks every template tab and relaunches a dead agent in a
  new pane split beside its old one, or in a new tab; it never types into an existing pane, and a
  tab with anything running in any pane is left alone. `--dry-run` prints the plan, `--resume`
  resumes a Claude Code agent's recorded session through the launcher.
- Opening a project's workspace (herdr's `workspace.created`/`workspace.focused`) starts its stopped
  services; the hook touches no pane. After a reboot herdr's own session restore brings agents back.
- `agentbus projects` lists every known project (templates, service units, the broker's own) with
  broker, services and agents, even with Redis down.
- missions waits for a Git repository (`missions waiting git`, health `warn`) instead of failing.
- `./setup.sh install` asks your name once (`agentbus identity set <name>`, busmon.toml); `up` uses
  it as `--as`.
- Every project service runs the agentbus setup installed (`buildinfo.Bin`), whoever ran `up`.

## v0.20.0 — agent-bus as a herdr plugin

- This checkout is the herdr plugin `netbja.agentbus`, linked by `./setup.sh install`; herdr-plus is
  no longer installed or needed (an older one is reported by `status`, never removed). setup owns
  only the registration and key-binding block it wrote.
- `Ctrl+B` then `Up` opens the Projects picker (`busmon --pick`): templates from
  `~/.config/agentbus/projects` with their broker state, filter by typing, Enter opens a project the
  broker confirms; it never provisions. The **Agent Bus: busmon** action opens busmon for the
  focused workspace's project.
- Templates live in `~/.config/agentbus/projects` (`AGENTBUS_PROJECTS_DIR`), resolved by
  `agentbus roles projects-dir`; no migration code: move existing ones by hand once
  (`mv "$(herdr plugin config-dir cloudmanic.herdr-plus)"/projects/*.toml ~/.config/agentbus/projects/`).
- `agentbus up` reads the herdr session from `HERDR_SOCKET_PATH` when `HERDR_SESSION` is unset and
  refuses services when neither names one; the herdr binary is `HERDR_BIN_PATH`, `HERDR_BIN`, `herdr`.
- Health checks the plugin's registration, checkout and version instead of herdr-plus.
- Fixed: plugin panes are opened without `--workspace`, which herdr 0.9.3 refuses (#92).

## v0.19.0 — project health core

- The dispatch service runs bounded, non-overlapping health checks under its lease. Results,
  transition history and fail wakes are fenced and stamped with Redis time; uncertain or stale
  evidence stays `unknown`.
- `agentbus health` lists checks, shows detail and remediation, reads transitions, and stores a
  separate sentinel note. A transition into `fail` wakes sentinel once per episode, with one daily
  review directive.
- Sentinel may restart one identified failed service when its restart budget allows, or refresh a
  failed local model catalogue. Other changes are escalated for a human decision.
- The service refreshes per-agent usage at most every 15 minutes and the host's model catalogue
  daily under a local lock. Usage writes are fenced.
- busmon adds a health pill, F5 details and health transitions in the R recap. Missing or stale
  readings never render as healthy.
- Checks cover services and agent identity/launch, model drift, runaway work, cmd delivery and
  targets, quota freshness, tracked secret-like paths, Redis exposure/password source, service env
  permissions, installed versions and the herdr probe. The runaway check only warns; it never
  interrupts an agent.
- Secret scanning, canary, project key inventory, project-defined checks and Redis password
  generation/rotation remain outside this release.

[Notes](docs/releases/v0.19.0.md)

## v0.18.0 — busmon attention and project recap

- busmon distinguishes a human-answered `waiting` declaration from an unanswered one using
  retained, addressed `human=yes` commands; open asks remain separate attention items.
- Configurable waiting reminders group due asks and unanswered declarations, respect snoozes, and
  wait until the person is out of a draft or another modal.
- `R` opens a deterministic project recap from snapshots and retained events. A confirmed action
  can ask the project's escalation target (master by default) for a summary; no request is sent
  without an explicit `y` confirmation.
- Global settings live in `~/.config/agentbus/busmon.toml` (or `$XDG_CONFIG_HOME/agentbus/`);
  command-line flags and environment variables retain precedence. Local visit and snooze state is
  stored atomically under `~/.cache/agentbus/` with user-only permissions.

[Notes](docs/releases/v0.18.0.md)

## v0.17.4 — reachable role tools, safe session resume, and both quota windows

- `setup.sh install/update` installs `agent-launch` and `agent-spawn` in `BIN_DIR`; status reports
  them and uninstall removes only this repository's symlinks.
- `agent-launch <role> <project> --resume <session-id>` resumes a Claude Code conversation with
  its role rules and launcher identity; other clients refuse the option.
- busmon shows both account quota windows in wide and medium layouts, with markers per window;
  narrow keeps the tightest. The agent detail has one Account row with both resets and says the
  account is shared by every agent using that provider.
- `i` in an agent detail opens a reply compose window addressed to that agent.
- Human bus messages are answered with a threaded `agentbus reply`, so the answer appears in
  busmon; dispatch does not escalate a target whose name is a retained human sender identity.

[Notes](docs/releases/v0.17.4.md)

## v0.17.3 — shared secret-file denies for Claude Code roles

- A top-level `[tool_rules]` table applies validated Claude Code permission rules to every role.
  The shipped Read/Edit denies cover common environment, key and wallet filenames while leaving
  `.env.example` available. They also apply in `bypassPermissions`; they are defense in depth, not
  a boundary, since processes started by an agent can still read their environment.
- The agent-bus skill recommends running betterleaks or gitleaks before a PR when installed.

[Notes](docs/releases/v0.17.3.md)

## v0.17.2 — `install.sh` removed

- `install.sh` (an alias of `./setup.sh install`) is gone: use `./setup.sh install`. Docs, the
  roles-script hint and the installer test follow. Nothing else changes.

## v0.17.1 — a gauge bar for each provider's quota in busmon

- busmon's header draws each provider's tightest quota window as a gauge (10 cells wide, 5 medium)
  before its percentage; the narrow layout keeps the percentage alone.

## v0.17.0 — master pops agents without the classifier, and asks before deploy

- A role that pops others (`pops = true`, master) launches with one Claude Code allow rule per
  ordinary role, so `agent-spawn architect` no longer reaches the auto-mode classifier.
- A role marked `approval = "person"` (deploy) gets an ask rule instead: Claude Code asks in
  master's pane before `agent-spawn deploy`. A convenience, not a lock: a quoted role reaches the
  classifier, which may let it run.
- `[roles.<r>.tool_rules]` adds Claude Code rules of a role's own; a rule allowing every shell
  command, or naming `agent-spawn`, is refused.

[Notes](docs/releases/v0.17.0.md)

## v0.16.0 — a compose window in busmon

- `I` opens a multi-line compose window: Enter is a new line, Ctrl+S sends, Tab the recipient, Esc
  keeps the draft. Ctrl+O or a multi-line paste moves the line's text there.
- The question panel's free answer uses the same window; pasting works everywhere.
- Agents use a code-index MCP server first when one is available.

[Notes](docs/releases/v0.16.0.md)

## v0.15.3 — a quota hold that does not flap

- A quota hold ends only when the window that set it is read low, renewed or past its reset; an
  aged reading at the limit no longer lets another window release it (master got "Project
  resumed" every ten minutes).
- master declares `waiting` only while one of its questions is open.

[Notes](docs/releases/v0.15.3.md)

## v0.15.2 — a model table that keeps itself current

- `agentbus models` lists every provider's current models (OpenAI, Anthropic, xAI, Moonshot,
  Google) from each client's own catalogue, refreshed daily by the sentinel and by setup.
- A role can name a tier (`tier = "frontier"`) instead of a model: each launch takes the current one.
- `roles doctor` adds `gone` and `newer`, on stated evidence only.

[Notes](docs/releases/v0.15.2.md)

## v0.15.1 — swaps you can trace, and models that drift are seen

- A question you answer by message is closed by master, and busmon offers the question panel when
  you write to an agent that has one open.
- `agentbus roles doctor` shows each agent's configured, launched and used model; busmon flags
  `model≠`, the sentinel checks daily, `agentbus swap <role> --back` relaunches after your yes.
- `agentbus swaps` keeps the history of every swap; `swap --back`, `agentbus retire <role>`.
- roles.toml uses Claude's aliases (opus, sonnet, haiku), so a renamed model never breaks a launch.

[Notes](docs/releases/v0.15.1.md)

## v0.15.0 — work resumes after a quota reset, and a long wait has a way out

- A tab blocked by its own client (Claude Code's limit menu, a permission prompt) now reads
  `waiting` with the flag `prompt` in busmon, and its detail names the pane to answer in.
- An escalation caused by a blocked or busy pane is re-armed once the pane is ready, and goes to
  you when its recipient cannot act (master escalated to itself and stayed stuck).
- When a provider's hold ends, its agents with interrupted work are told to resume, once.
- A long hold (over 2 h) asks for a decision. `fallback_profiles` in `roles.toml` and
  `agentbus swap <role> --profile <p>` let master, or you, replace an agent by another model
  until the reset, with every check made before anything is closed.

[Notes](docs/releases/v0.15.0.md)

## v0.14.6 — a quota reset granted early is seen

- Anthropic granted a quota reset before the weekly one, but project-b stayed paused for 96
  hours: the only Claude reading was the old "weekly limit" refusal in a transcript, believed
  until its announced reset. Now a refusal no longer counts once Claude has answered since, in any
  session of the account; the hold and the project pause end at once and master is told to go on.
- An early end of a quota pause now sends master the resume directive (it ended silently).
- `agentbus quota` shows `served 2m ago` for a provider with no window to read.

[Notes](docs/releases/v0.14.6.md)

## v0.14.5 — panels you can tell apart

- Each busmon panel has its colour, btop-like: Agents blue, Activity green, Board violet,
  Requests yellow. The focused panel's frame is drawn in its colour and its title becomes a
  coloured pill; the other frames are faded and their titles keep their colour. Only colours
  changed: the frames and their alignment are the same.

[Notes](docs/releases/v0.14.5.md)

## v0.14.4 — a stopped agent says why

- While the project is paused, an agent that declared working or idle reads `paused` in busmon's
  state column; while its provider is at its limit, `quota`. master no longer shows `working`
  while Claude's quota has stopped everyone. Blocked, waiting and done stay as declared; the
  detail keeps the declared state and says why the agent is stopped.

[Notes](docs/releases/v0.14.4.md)

## v0.14.3 — an agent at work is seen at work

- Agents declare `working` before they start (skill `agent-bus`): deploy worked six minutes
  on project-b while busmon showed it idle.
- Safety net: the dispatch service looks at every verified pane every 15 s; busmon flags `busy`
  an agent whose pane runs a turn without declaring `working`, beside its declared state, never
  instead of it. The detail shows the observation (`Observed`).

[Notes](docs/releases/v0.14.3.md)

## v0.14.2 — a person's answer is never lost

- A plain `agentbus subscribe` (no `--since`) still skips an agent's backlog, but now delivers
  what a person wrote to that agent within 24h (answers to its questions, busmon directives).
  Two answers sent seconds apart reached master as one: the second was acknowledged unseen
  when master re-armed without its cursor.

[Notes](docs/releases/v0.14.2.md)

## v0.14.1 — questions you can open, texts you can read

- busmon opens the questions with `!` or `w`, and with Enter in the detail of an agent that asks
  (`!` did nothing there; F4 is taken by some desktops).
- A report written on one line gets the paragraphs, bold labels and choice lists its author typed
  inline; agent detail lines wrap under their column instead of at column 0.

[Notes](docs/releases/v0.14.1.md)

## v0.14.0 — questions for you, and text you can read

- `agentbus ask`: an agent asks a person structured questions (options, a recommended one, free
  text always possible). busmon opens them with `!` / F4 as tabs, like Claude Code's questions; each
  answer is sent at once and wakes the agent; an answered question never comes back; a changed
  recommendation replaces the ask, keeping the answers already given. `agentbus asks`, `agentbus answer`.
- The header counts open questions; the agent's detail starts with them.
- Reports and messages are shown as Markdown readable in a terminal (headings, lists, paragraphs).

[Notes](docs/releases/v0.14.0.md)

## v0.13.0 — quotas: stop before the limit, for any AI

- The dispatch service reads every provider's usage windows from first-party local sources
  (Claude Code through `agentbus statusline`, Codex through its app-server, Grok's log; any other
  AI through `quota.toml`: a file, a script, or unlimited) and acts: one warning to master at 85%
  of a window, the provider's agents held at 95% until the reset, the project paused when master's
  own provider runs out.
- busmon: one gauge per provider (`claude 5h 82% ~25m`), `held` on agents; `agentbus quota`.
- No dependency on ccstatusline any more: `agentbus refresh` no longer reads it nor writes `{p}:budget`.

[Notes](docs/releases/v0.13.0.md)

## v0.12.7 — what an agent needs from you, at the top

- busmon: an agent's detail shows its last report in full right after its status message; for an
  agent `waiting`, under "What <agent> needs from you", with how to answer.
- The Requests count shows what is addressed to you (`1 for you`), and the agents' own open threads
  apart (`2 between agents`); the header counts only yours.

[Notes](docs/releases/v0.12.7.md)

## v0.12.6 — results reach master

- `agentbus report <agent> --to <agent> <message>`: the report, plus a directive to whoever waits
  for it (`Report <id> for you … : <first line>`). A plain report still wakes nobody.
- Role prompts (coder, foureyes, deploy, architect, security) report `--to master`; the agent-bus
  skill says why.
- master no longer re-arms every few minutes while the dispatcher runs: the dispatcher wakes it.
  When it must stay armed, it uses a one-hour idle window.

[Notes](docs/releases/v0.12.6.md)

## v0.12.5 — project pause; @all as one line

- `agentbus pause --for 2h | --until 02:15 | --until-reset [reason]`, `pause --status`, `resume`:
  nobody is woken while the project is paused; at the deadline the dispatcher ends the pause and
  sends master one resume directive. busmon's header: `paused · resumes in 1h52 (quota)`.
- master pauses the project itself when the quota runs out (sentinel reminds it at 90%), instead
  of a sleep in its own session.
- `@all` reaches only the agents in `{p}:agents` (not the conductor), and Activity shows the fan-out
  as one `→ @all (N)` line.

[Notes](docs/releases/v0.12.5.md)

## v0.12.4 — a quieter waiting

- Only the word `waiting` moves now: it fades in and out over 3 seconds (20% to 100%, eased).
  The ♙ glyph and the header badge stay still. Without true colour, nothing moves.

[Notes](docs/releases/v0.12.4.md)

## v0.12.3 — an agent waiting for you; auto-scroll stays on

- New state `waiting` (a person must decide): busmon sorts it first with a pulsing ♙, shows
  `♙ N waiting for you` in the header and rings the terminal bell once per new question. The master
  skill declares it for every decision it needs from the human.
- Activity: selecting, clicking or opening a line no longer turns auto-scroll off; only `f` and a
  wheel scroll up do.

[Notes](docs/releases/v0.12.3.md)

## v0.12.2 — Activity auto-scroll toggle, dotted freshness gauge

- busmon: `f` turns Activity's auto-scroll off and on, from any panel; the title says
  `auto-scroll on` or `auto-scroll off · N new`.
- Today's day separator reads `── Wed 2026-10-14 - (today) ──`.
- The Agents panel's `updated` gauge is drawn in dots (`●●●●●○○○○○`).

[Notes](docs/releases/v0.12.2.md)

## v0.12.1 — dormant agents, cleanup from busmon, escalation target, how to start work

- `setup.sh` no longer reports a missing herdr-plus projects action when herdr is just not
  running (outside a herdr session).
- busmon: agents silent for over a day are hidden (`a` shows them) and left out of `@all`; `d` /
  `Delete` then `y` forgets an agent or closes an old request thread; the Requests count covers the
  last 24 hours; the Activity day separator says `today`. New `agentbus agents forget <agent>...`.
- Fixes: a just-launched agent no longer shows a 1970 age; `up` names the right herdr session;
  `roles.toml` moves to Opus/Sonnet/Haiku 5.5.
- `agentbus up --escalate-to <agent>`: the services escalate to that agent instead of `master`,
  and a later `up` keeps it. For a team whose coordinator has another name.
- Operational guide (EN/FR), new section 6: starting a small task and a multi-day change; and
  scenario B2, moving a team from before v0.12.

[Notes](docs/releases/v0.12.1.md)

## v0.12.0 — project services: dispatch and missions outlive the workspace

- `agentbus up` runs dispatch and missions as user services (systemd `--user`, launchd), never at
  login, with an explicit fallback to herdr tabs (`--tabs`).
- Bounded restarts that never loop on a stop, a deleted project, another instance or a bad
  configuration.
- busmon shows each service's state (`F3` for details and logs). New commands: `agentbus down`
  and `agentbus service status|logs|restart|…`.

[Notes](docs/releases/v0.12.0.md)

## v0.11.0 — a first run that works; setup, update, uninstall; the docs agent

- `./setup.sh install | update | uninstall | status`. `install.sh` stays as an alias.
- herdr-plus is now registered with herdr, which fixes `plugin_action_not_found`. The external
  skills are cloned, which fixes `skill not found: wayfinder`.
- `agentbus up <project>` provisions a project and opens its workspace, busmon first.
- A `docs` agent can be handed a change when it completes. busmon shows a discreet update notice.
- The security read of the launcher is fixed (env_refs, API key, elevated options).

[Notes](docs/releases/v0.11.0.md)

## v0.10.3 — quieter verdicts, a hardened launch cache

- `verdict` no longer reports a mission review as `no open challenge`.
- The launch cache reuses only files whose content it can verify.

[Notes](docs/releases/v0.10.3.md)

## v0.10.2 — the operator is a person; no more Python

- busmon writes as `--as <name>` (or `pilot`), shown as `♙ Bernard [human · pilot]`.
- `@all` wakes every agent, and a broadcast nobody reads needs a second Enter.
- The role launcher is Go (`agentbus roles`).

[Notes](docs/releases/v0.10.2.md)

## v0.10.1 — bound identity, change complete

- `agentbus bind` makes clients such as Codex wakeable by the dispatcher.
- The conductor tells master once when a change is complete.

[Notes](docs/releases/v0.10.1.md)

## v0.10.0 — missions, shared-memory tooling, installer

- A large change runs as missions: a plan in Git, a deterministic conductor, 4-eyes reviews, and
  merges into `change/<change>` only.
- `agentbus mem` keeps the shared memory checkable. Adds the installer.

[Notes](docs/releases/v0.10.0.md)

## v0.9.0 — busmon redesign

- busmon is a btop-style dashboard: attention-sorted agents, Activity in columns, three widths,
  dark and light themes.

[Notes](docs/releases/v0.9.0.md)

## v0.8.0 — event-driven dispatch

- `agentbus dispatch` wakes an idle agent when work lands for it, by typing one wake line into a
  ready, verified pane.

[Notes](docs/releases/v0.8.0.md)

## v0.7.0 — configurable agent clients and security review

- Claude Code, Codex or Kimi CLI per role, with mixed teams.
- An on-demand security role.

[Notes](docs/releases/v0.7.0.md)

## v0.6.1 — tests stop reaching for the production broker

[Notes](docs/releases/v0.6.1.md)

## v0.6.0 — coordination recovery and shared agent memory

[Notes](docs/releases/v0.6.0.md)

## v0.1.0 – v0.5.1

The Redis Streams bus, per-project namespaces, the pilot lease and 4-eyes gate, the verdict
ledger, threads, the protocol version and report fidelity. These releases have no separate
notes; see `git log v0.5.1`.
