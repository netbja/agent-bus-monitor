---
id: 2026-11-15-herdr-exit
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-15
last_verified: 2026-11-15
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

## Limits / what would invalidate it

A host where the holder cannot run (no PTY, no run directory that passes §4.4's check) has no
terminals at all; there is no fallback to herdr. The last herdr release stays the way back.
