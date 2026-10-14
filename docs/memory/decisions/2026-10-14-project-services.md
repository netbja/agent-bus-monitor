---
id: 2026-10-14-project-services
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-14
last_verified: 2026-10-14
author: claude
confidence: inferred
---

## Finding/decision

Since v0.12, `agentbus dispatch` and the missions conductor run as **user services** of the
operating system's own manager: systemd `--user` on Linux, launchd on macOS. They no longer run
as herdr tabs. This revises the point "a herdr pane, not systemd" of the
[event-driven dispatch decision](2026-10-10-event-driven-dispatch.md). The rest of that decision
stands: one automated pane writer, a wake line only, fenced writes.

Shape of the decision, each part with its reason:

- **The system's own manager, no supervisor of ours.** systemd and launchd already restart,
  log, and stop on logout. A supervisor of our own would repeat that, worse. One unit per
  project and kind (`agentbus-<p>-<kind>.service`, label `dev.agentbus.<p>.<kind>`), running
  `agentbus --project <p> service run <kind>`.
- **Never at login.** The systemd unit has no `[Install]` section and is never enabled. The
  launchd plist lives outside `~/Library/LaunchAgents`. Only `agentbus up` or `service start`
  starts a project.
- **An explicit fallback, never a hidden process.** Without a usable manager, `up` says why and
  opens herdr tabs, as before. `--tabs` forces that. It never starts a `nohup` process.
- **Exit 0 is final, 1 is restarted.** The cases that are not restarted:
  - an explicit stop;
  - the project deleted;
  - an invalid configuration;
  - a spent restart budget (5 failures in 10 minutes: launchd has no start limit, so the
    service counts them itself).

  Each records why before it exits. A service that finds another instance holding the lease
  (an old tab) waits in `standby` and takes over once the lease is free. The leases and the
  conductor's lock are unchanged.
- **A deleted project stays deleted.** `up` writes the `{p}:services` marker. Without it a
  service exits 0, and its state writes are guarded by `EXISTS`. A conductor whose lease lapses
  with its project is not restarted, because its lease would recreate the project's keys.
  `busmon --delete` removes the local units before it deletes the keys.
- **State in Redis, logs on disk.** Every 5 s each service publishes its state (`ready`,
  `waiting-herdr`, `idle`, `busy`, `failed`, …) in `{p}:services`, so busmon shows it from any
  machine. Its log rotates at 1 MiB × 3. busmon reads both, never parents a service, and never
  starts one.
- **No secret in a unit.** The broker connection and the herdr session are in
  `~/.config/agentbus/services/<p>.env`, mode 0600. A unit or plist holds only paths and
  names.

Why the herdr pane no longer holds: closing the workspace, or herdr itself, killed the
conductor halfway through an integration. Nothing restarted it after a crash, and its log was
only visible in the tab.

## When it applies

Any change to how dispatch or the conductor are started, stopped, restarted or observed; to
`agentbus up`/`down`/`service`; to the `{p}:services` key; to `setup.sh uninstall`; or to
`busmon --delete`.

## Evidence

- [Design](../../superpowers/specs/2026-10-14-project-services-design.md).
- Branch `feat/services`. The tests use doubles of systemctl, launchctl and herdr, and a
  disposable Redis (`AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392`). They cover:
  - `service/` (units, plist, env file, log, budget, detection);
  - `cmd/agentbus` (`service run` exit rules, `up`/`down`/`service`);
  - `cmd/busmon` (state facts, overlay, `--delete`);
  - `tests/installer_test.sh` (fake systemctl).

## Limits / what would invalidate it

- macOS (launchd) is tested only against a fake `launchctl`, not natively.
- A service runs on the machine where `up` ran. Another machine sees its state, not its log.
- `busmon --delete` (SCAN then DEL) can still race one state write into an orphan key. The
  `EXISTS` guard makes it unlikely, but it is not fenced by a lease.
