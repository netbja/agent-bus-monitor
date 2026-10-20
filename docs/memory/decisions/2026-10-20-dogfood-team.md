---
id: 2026-10-20-dogfood-team
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-20
last_verified: 2026-10-20
author: claude
confidence: verified
---
# agent-bus is developed as an agent-bus project

## Finding/decision

From v0.21.1 on, agent-bus-monitor is developed by an Agent Bus team of its own: the project
`agent-bus-monitor`, its own herdr session, a master, coders, foureyes and sentinel from
`roles.toml`, busmon, the board, the verdict ledger and missions. Ad-hoc herdr panes are no longer
used for this work. Bernard decided this on 2026-10-20, after v0.21.0.

The reason is that the tool finds its own bugs when the team lives in it. v0.21 alone showed three
that only real use revealed:

- service units flipping between two binaries;
- the relaunch typing into a pane that was running work;
- a shell restored by herdr outside the launcher.

The ad-hoc way also left its review state in scratch files instead of `{p}:verdicts`.

The team runs on the tool it changes, so these rules keep a bad change from breaking the team:

- **Work only in a worktree.** Use `.worktrees/<branch>`; never code in the main checkout. The
  main checkout is the herdr plugin's root: it runs `./agentbus` and `./busmon` from there, and
  `setup.sh` builds there. A `go build` in it changes what the plugin runs.
- **The team runs the installed release.** That is `~/.local/bin` (the units run `buildinfo.Bin`),
  plus the checkout's binaries, which only `./setup.sh install` from `main` writes. A branch build
  stays in its worktree.
- **Tests use the disposable broker only:** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392`
  (container `agentbus-v08-test`; after a reboot, run `docker start agentbus-v08-test`). Never the
  live :6380 that the team itself uses. Installer tests fake docker, herdr and systemctl.
- **4-eyes goes on the bus.** Use `agentbus challenge` and `agentbus verdict --pr N`. A merge needs
  `agentbus verdicts --pr N` to read APPROVED. foureyes is Codex. Any test changed to bless new
  behaviour is named in the PR and justified. A reviewer stops and reports rather than weakening a
  test.
- **The person releases.** Merge, tag, `gh release`, `./setup.sh install` and service restarts each
  need Bernard's explicit go. Master asks with `agentbus ask`; no agent does any of these steps on
  its own.
- **Rollback is a release, too.** If an installed release breaks the team, reinstall the previous
  tag from a worktree checkout of it (`./setup.sh install` there), then restart the services.
  Restarting the services alone does not roll back: they keep running the broken binary.

## When it applies

Every change to this repository from v0.21.1 on, by any agent.

Other projects (ai-tradex-solana, hyperliquid) keep running the installed release. A release
reaches them too, so the person's go covers both.

## Evidence

- The v0.21 review: [spec](../../superpowers/specs/2026-10-20-startup-relaunch-design.md) ("Revision
  after review"), PR #94, five Codex rounds.
- [Project services decision](2026-10-14-project-services.md) and the
  [herdr plugin decision](2026-10-19-herdr-plugin.md), on which the worktree and installed-binary
  rules depend.
- Roadmap and open work at the start: [journal checkpoint](../journal/2026-10-20-v021-handoff.md).

## Limits / what would invalidate it

- **A team on one broker is a single point of failure.** If the live Redis is down, the team cannot
  coordinate, while it may be the very thing being fixed. Then fall back to plain Git and one agent,
  and say so.
- **The old `busdev` project** on the broker, the v0.10 attempt, has no template. Retire it with
  `busmon --delete busdev` once its leftover mission worktree
  (`~/.local/state/agentbus/busdev/roles-go`) is confirmed obsolete.
