# Changelog

One entry per release. The full notes for each are in [`docs/releases/`](docs/releases/).
Update a machine with `./setup.sh update`; busmon shows `↑ vX.Y.Z` in its header when a newer
release exists.

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
