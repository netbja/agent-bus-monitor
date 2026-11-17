---
id: 2026-11-15-herdr-exit
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-15
last_verified: 2026-11-17
author: coder
confidence: verified
---

## Finding/decision

**herdr is gone: the project's own holder hosts every agent's terminal.** Step 10 of the herdr exit
(the cutover) deletes the herdr plugin (`herdr-plugin.toml`, `agentbus plugin`), every herdr call
and every `HERDR_*` read. `up`, `start`, `restore`, `relaunch`, `reload`, `swap`, `suspend`,
`resume`, `retire`, the pop and the dispatcher's self-repair start, read and close terminals through
the holder (`termhost.Host`, `holder.NewHost`), which runs as a project service. The rules that
stand unchanged: nothing types into an existing terminal; an old terminal closes only once its
replacement is proven; the dispatcher is the one automated writer. An in-tree process asks the
dispatcher for a host operation through the relay (§4.2), never drives the holder itself.

**The holder starts with the other services** (amended 2026-11-17, [holder
hardening](../../superpowers/specs/2026-11-17-holder-hardening-design.md) H1, the person's answer to Q1):
when `services.start_at_login` is on, `agentbus up` and `agentbus service login` enable the current
build's holder unit with dispatch and missions (systemd: its `[Install]` section and `enable`;
launchd: its LaunchAgents copy), and disable every other build's: an older build drains and is never
started again. It ships with reboot-resume A (Q2), so a reboot never brings the team back without
the judgement that holds self-repair after an intentional one.

What stays from the [plugin decision](2026-10-19-herdr-plugin.md): templates in
`~/.config/agentbus/projects`, no migration code, and a picker that never provisions.

## When it applies

Any change that would start, read or close an agent's terminal, or that mentions herdr. A new herdr
call, `HERDR_*` read or herdr exec is refused: a test forbids it.

## Evidence

- Spec: [herdr exit](../../superpowers/specs/2026-11-10-herdr-exit-design.md), step 10; the relay is
  §4.2, merged as step 10a (PR #539).
- The cutover PR (branch `feat/herdr-10b-cutover`); it merges only after the person tags the last
  herdr release and passes the live check its description lists.
- H1: `service/holderlogin.go` (`HolderLoginStart`), `holderAtLogin` in `cmd/agentbus/servicelogin.go`,
  branch `feat/holder-step2`.

## Limits / what would invalidate it

A host where the holder cannot run (no PTY, no run directory that passes §4.4's check) has no
terminals at all; there is no fallback to herdr. The last herdr release stays the way back.
