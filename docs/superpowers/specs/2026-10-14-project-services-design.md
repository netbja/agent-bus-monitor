# v0.12 — project services: dispatch and missions without tabs

Status: implemented on `feat/services`. Asked by Bernard on 2026-10-14; scope agreed with Grok and
Codex (handoff `.superpowers/brainstorm/CLAUDE-background-services-2026-10-14.md`, local).

## Problem

Since v0.8 and v0.10, `dispatch` and `missions` run as herdr tabs. Closing the workspace, or
herdr itself, stops them, which interrupts an integration halfway. Nothing restarts them after a
crash. Their logs are only visible while someone looks at the tab.

## Decision

Each project runs two **user services**, supervised by the operating system's own manager:
- **systemd `--user`** on Linux;
- **launchd** on macOS (a LaunchAgent).

No supervisor of ours is added. When neither manager is usable, `agentbus up` says so and falls
back to the old tabs. It never starts a hidden `nohup` process.

- **Units, one per project and kind:** `agentbus-<project>-<kind>.service`, or the launchd label
  `dev.agentbus.<project>.<kind>`, where `<kind>` is `dispatch` or `missions`. Units are never
  enabled at login, so a reboot starts no project.
  - systemd has no `[Install]` section, and the unit is never enabled.
  - The plist lives outside `~/Library/LaunchAgents`, in `~/.local/share/agentbus/launchd/`, and
    is loaded only by `agentbus up` or `agentbus service start`.
- **One entry point:** both managers run `agentbus --project <p> service run <kind>`, from the
  absolute path of the `agentbus` that installed the unit. `service run` does five things:
  - It loads `~/.config/agentbus/services/<p>.env`, written by `up`, before it connects to Redis.
    The file holds `REDIS_*`, `HERDR_SESSION`/`HERDR_SOCKET_PATH`/`HERDR_BIN*`, `PATH`, `LANG`
    and `XDG_*`. It is `0600` in a `0700` directory, and a group- or world-readable file is
    refused. No secret appears in a unit, a plist, an argument or a log.
  - It logs to `~/.local/state/agentbus/<p>/<kind>.log`, rotated at 1 MiB with three files kept,
    and to stderr (the systemd journal).
  - It publishes its state in Redis (`{p}:services`, below) every 5 seconds.
  - It runs the existing loop: `superviseDispatch`, or `mission.Run`. The leases and the
    conductor lock are unchanged.
  - It chooses its exit code so that the manager restarts only what can recover.
- **Exit and restart rule, the same for both managers:**

  | Situation | Exit | Restarted |
  |---|---|---|
  | Explicit stop (SIGTERM), project deleted, another instance holds the lease, invalid configuration, restart budget spent | 0 | no |
  | Transient failure (broker unreachable, git error, …) | 1 | yes |

  - Each case that is not restarted records its reason first.
  - systemd uses `Restart=on-failure`, `RestartSec=5s`, `StartLimitBurst=5`/`StartLimitIntervalSec=600`.
    launchd uses `KeepAlive={SuccessfulExit=false}` and `ThrottleInterval=10`.
  - Because launchd has no start limit, `service run` keeps its own **restart budget**: at most 5
    failed starts in 10 minutes (`<kind>.restarts`). After that it records `gave-up` and exits 0.
  - An explicit `up` or `service start` clears the budget.
- **A deleted project is never resurrected.** `up` writes the marker `{p}:services` (field
  `installed`). Without that marker, `service run` exits 0 with `project gone`. The state writes
  are a Lua `HSET` guarded by `EXISTS`. The conductor is never re-run on a project whose marker
  is gone, because its lease would recreate the project's keys. `busmon --delete` stops and
  removes the project's local units and env file before it deletes the keys.
- **State, visible from any machine** (`{p}:services` hash, one JSON field per kind):
  `state`, `detail`, `at` (unix ms), `pid`, `host`, `error` (last error, sanitised), `log` (path
  on `host`). The states:
  - dispatch: `starting`, `waiting-herdr` (herdr cannot be reached: the process is alive, the
    service is not ready), `ready`, `standby` (another dispatcher holds the lease);
  - missions: `idle` (ready, waiting for missions), `busy` (a change is active);
  - both: `stopped`, `failed`, `gave-up`, `project-gone`.

  A record older than 20 s is shown as not running, with its last state and age.
- **busmon** reads the hash every 5th tick, as it does for the dispatch facts.
  - The header shows `dispatch ready`, `dispatch waiting herdr`, `missions idle`, `missions
    busy`, `missions failed` and so on.
  - `F3` (or `s`) opens a **Services** overlay: per service the state, detail, last activity,
    last error, host, log path, and the last 40 log lines when the log is on this machine.
  - busmon never starts, stops or parents a service. It only reads the state and the logs.
- **Lifecycle commands:**
  - `agentbus up <p>` installs or updates the units and the env file, clears the budget, starts
    the services, then opens busmon and the agents. `--no-dispatch` and `--no-missions` skip a
    service; `--tabs` forces the old tabs.
  - `agentbus down <p>` stops the project's services, without deleting anything. It says when an
    integration was interrupted: the test run is killed with its process group, and the conductor
    redoes it at the next start (`Startup` repairs a change left in `integrating`). The agents
    and the workspace stay open.
  - `agentbus service status|start|stop|restart|logs|remove [<kind>]` covers one project.
    `agentbus service list` and `agentbus service remove --all` need no project and no broker;
    `setup.sh uninstall` uses them.
  - Closing busmon or the workspace no longer stops dispatch or the conductor.
- **Existing installations.** An older template still has `dispatch` and `missions` tabs. In
  service mode, `up` does not open those tabs. A tab started by the picker exits at once, because
  the service holds the lease.

## Not done

- macOS has no native test run. The plist and the `launchctl` calls are tested against a fake
  `launchctl`.
- A service runs on the machine where `up` ran. On another machine busmon shows its state, but
  not its log.
