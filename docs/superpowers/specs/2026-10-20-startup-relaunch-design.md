# Startup and relaunch (v0.21)

Status: approved by Bernard (2026-10-20), implemented on `feat/v021`; the amendments made while
coding are marked **Built:**. Code by Claude, review by Codex. Builds on the herdr plugin
(v0.20, by the other Claude): its three `cmd/agentbus/up.go` changes and its shared project-listing
function land first (`docs/recon/2026-10-20-plugin-coordination.md`); this release rebases on them.

## Problem

After a laptop reboot, Bernard reconnects to a project and nothing works:

- the project's herdr session restores the workspace and its tabs, but the agents' processes are
  gone; `agentbus up <p>` finds the workspace, focuses it and stops ("already open"), so no agent
  is relaunched;
- he cannot easily find the project's name: `agentbus --list` does not exist, `busmon --list`
  needs Redis, the templates sit in a plugin directory;
- the missions service of a project whose directory is not yet a Git repository exits for good
  ("invalid configuration: --repo is not a git repository"); busmon keeps showing `missions failed`
  even after `git init`, until someone restarts the service by hand;
- busmon and `agentbus up` need `--as <name>` to know the person; without it he is `pilot`.

What already works and stays: `agentbus up` restarts stopped project services (it resets their
restart budget and starts them); nothing starts at login (decision 2026-10-14-project-services).

## 1. `agentbus projects`

A read-only list of every known project, usable when Redis is down, placed above the
project-required exit in `main()` (like `mem`, `limits`, `version`).

- Source: the shared listing function written for `busmon --pick` (templates through
  `rolecfg.ProjectsDir`, plus `bus.Projects` when the broker answers, plus the installed service
  units from `service.Manager`). No second implementation.
- One line per project: name, directory, broker (`present` / `absent` / `?` when Redis is
  unreachable), services (`running` / `stopped` / `none`), agents (`N alive / M dead / K unknown`,
  from section 2's liveness rule; `?` without Redis).
- Ends with the next step when something is down: `agentbus up <p>` to restart it.
- `--json` for scripts. Exit 0 even when a source is unreachable (the line says `?`).
- **Built:** agents are counted for the template's agent tabs by the snapshot rule only (`?` without
  a pid); the pane check needs herdr and the workspace open, so it stays in `up`.

## 2. `agentbus up <p>` relaunches dead agents

When the workspace labelled `<p>` is already open, `up` no longer stops at "focused": it checks
each agent tab of the template and relaunches the dead ones, in their existing pane.

- **Which tabs.** Template tabs whose command is an `agent-launch <role> <p>` line (the agents);
  busmon and the service tabs (`--tabs` mode) are handled as today. A template tab with no pane of
  that label in the workspace is created (`herdr tab create`, as `fill` does) and launched.
- **Dead or alive.** An agent is alive when its `{p}:agents` snapshot carries a pid that runs with
  the recorded `pid_start` (`dispatch.ProcStart`) — the identity rule the dispatcher already uses.
  It is dead when the pid is absent, gone, or reused. Without a snapshot, or without Redis, the pane
  decides: herdr reports an agent running in it (`pane list` `agent` field) → alive; a plain shell →
  dead. When neither source can tell, the tab is left alone and reported `unknown`.
- **Relaunch.** `herdr pane run <pane> <template command>` in the tab's pane, exactly what `fill`
  does at first open. Never in a pane where herdr reports a running agent or a busy foreground
  process (a shell running something else is not ours to interrupt): reported `skipped (busy)`.
- **`--resume`.** Optional: for a Claude Code role with a recorded `AgentSnapshot.Session`, the
  command becomes `agent-launch <role> <p> --resume <session>` (v0.17.4). Default off: a full
  context is not always worth resuming. Other clients ignore it with a note.
- **Output.** One line per agent tab: `alive`, `relaunched`, `relaunched (resumed)`, `skipped
  (busy)`, `unknown`, then the existing services summary. `--dry-run` prints the plan only.
- **Order.** Services first (as today), then agents, master first then the template order, so the
  dispatcher is up when the agents register.
- The relaunch never closes a pane, never types anything but the template command, and never
  touches a pane of another workspace.
- **Built:** "busy" is any foreground process other than a shell (`herdr pane process-info`);
  busmon and the other non-agent tabs are rerun the same way (shell → rerun, else `running`).
  `--dry-run` reads only: no bootstrap, no service, no focus. A per-project lock
  (`~/.cache/agentbus/up-<p>.lock`) serialises ups. The person's recorded name is the default
  `--as`.
- This is not a second automated pane writer (decision 2026-10-10-event-driven-dispatch): it runs
  only on the person's gesture (`agentbus up`, or the workspace-open event below, which the person
  triggers), exactly like the first open already runs the template commands.

### Trigger when the workspace opens

**Built:** herdr 0.9.5 emits `workspace.created` and `workspace.focused` to plugins, with
`HERDR_PLUGIN_EVENT_JSON` = `{"event":…,"data":{"workspace":{"workspace_id","label",…}}}` (checked
with a throwaway probe plugin, unlinked after). Re-focusing the focused workspace emits nothing.
The manifest runs `./agentbus plugin workspace-event` on both. The hook differs from `up` on
purpose: no bootstrap; stopped services are started with the manager's start only (no unit or
environment rewrite: a binary at another path would otherwise reconfigure and restart a running
dispatcher); no tab is created, so a retired role stays retired; it skips while an up holds the
lock and for a minute after one ended (the lock file's mtime), so up's own `workspace create`
event never types into panes up is filling. The original text follows.


If herdr 0.9.3 emits `workspace.created`/`workspace.focused` to plugins (reported by Kimi,
unverified), the plugin manifest gains one `[[events]]` entry that runs `agentbus up <p>` for a
workspace whose label is a known project, so "I open the project, everything restarts". The
event carries no `HERDR_SESSION`: the session is derived from `HERDR_SOCKET_PATH` (the plugin's
`up.go` change). If the event does not exist, nothing is added: `agentbus up <p>` stays the gesture.
To verify before coding: `herdr` emits the event with the workspace label; one run per opening.

## 3. missions waits for a Git repository

`agentbus service run missions` with a `--repo` that is not a Git repository no longer exits with
"invalid configuration". It publishes the state `waiting` with the detail `no Git repository in
<dir>: run git init` (**Built:** the state is `waiting-git`, shown `missions waiting git`, like
`waiting-herdr`; it checks on each 5 s heartbeat, which also keeps the record fresh), checks again every 30 s (no restart budget spent, no log flood: one line when
it starts waiting, one when the repository appears), and starts the conductor as soon as `git
rev-parse` succeeds. busmon shows `missions waiting` (attention colour) with that detail in F3; the
`services` health check reads `waiting` as `warn` with the remediation `git init in <dir>`.

## 4. The person's name, asked once

- **Built:** `agentbus identity [set <name>]` (no project, no broker) reads and records it; setup
  calls it, so bash never edits TOML. `--yes` and `AGENTBUS_SETUP_NO_IDENTITY=1` skip the question.
- `./setup.sh install` asks, on a terminal only, `Your name on the bus (lowercase, e.g. bernard):`
  when `~/.config/agentbus/busmon.toml` has no `[identity] as`, validates it with the agent-name
  rule and writes it there (the file is created 0600 if absent; other sections untouched).
  Non-interactive installs and tests skip the question.
- `agentbus up` and `bootstrap` use it as the default for `--as`; an explicit `--as` still wins.
  busmon already reads it (v0.18 settings).

## Tests

- `agentbus projects`: Redis down, no templates, units without template, `--json`; placement
  above the project-required exit.
- Relaunch: pure decision table (snapshot alive/dead/reused pid, no snapshot + agent/shell/busy
  pane, no Redis) with fakes for herdr and `/proc`; `--dry-run`; `--resume` only with a session;
  never runs in a busy pane; tab missing → created.
- missions: no repository → `waiting` state, no exit, then starts after `git init` (temp dir).
- setup: name asked only on a TTY (fake TTY absent in tests → skipped); existing file preserved.
- Installer tests keep fake docker/herdr/systemctl; Redis tests only on `AGENTBUS_TEST_REDIS_URL`.

## Out of scope

Autostart at login; relaunching agents of a workspace that is not open (`up` opens it as today);
any change to the plugin's own commands; sentinel changes (v0.23); busmon UI items (v0.22).
