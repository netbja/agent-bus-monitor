# Changelog

One entry per release. The full notes for each are in [`docs/releases/`](docs/releases/).
Update a machine with `./setup.sh update`; busmon shows `↑ vX.Y.Z` in its header when a newer
release exists.

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

- Anthropic granted a quota reset before the weekly one, but hyperliquid stayed paused for 96
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
  on hyperliquid while busmon showed it idle.
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
