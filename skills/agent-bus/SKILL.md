---
name: agent-bus
description: Use when you are a peer agent coordinating with other agents over the Agent Bus (Redis Streams via the `agentbus` CLI) — to publish your state/heartbeat, receive directives by arming `subscribe`, report progress, check whether you're piloted or autonomous, or gate a risky action with the 4-eyes challenge/verdict flow. For the master/pilot role (driving other agents' panes), use agent-bus-master instead.
---

# Agent Bus — Peer Agent

You are one agent among several coordinating over a shared **Redis Streams** bus, one CLI:
**`agentbus`**. You publish your `status`, receive directives by arming `subscribe`, gate
risky actions with `challenge`/`verdict`, and a human watches in busmon.

**Exact per-command syntax and the exact JSON field names live in `docs/AGENT-BUS-GUIDE.md`.
This skill is the mental model + the parts agents get wrong. Read the GUIDE before guessing
any flag or JSON field — don't invent them.**

## Setup
```bash
export AGENT_BUS_PROJECT=<project>   # REQUIRED namespace; every stream is {project}:{kind}
export AGENT_BUS_AGENT=<your-name>   # who you are; must match ^[a-z][a-z0-9_-]{0,31}$
agentbus notify "<name> online"      # sanity check → returns silently, exit 0
```

## Four traps that make a well-formed command fail
1. **Project is mandatory** — pass `--project` or export `AGENT_BUS_PROJECT`.
2. **Flags are `--double-dash <space> value`** — never `=`, never a single dash.
3. **Positional order is fixed;** the message is trailing words (goes **last**), no quotes needed.
4. **`<agent>`/`<project>` match `^[a-z][a-z0-9_-]{0,31}$`** — lowercase, letter-first, ≤32.

Full detail: GUIDE §0.

## Receiving directives — `subscribe` is wake-on-exit, NOT a loop
`agentbus subscribe <self>` **blocks for ONE addressed command, prints ONE JSON object,
then EXITS.** Arm it as a background task; its **exit wakes your session**, you handle the
object, then **re-arm**. Do **not** wrap it in a `while`/daemon loop — a long-lived loop
never wakes a terminal session.

- **Parse the JSON by its documented fields, not guessed ones.** The discriminator is
  **`event`** (`cmd` / `heartbeat` / `error` / `fatal`) — plus `rearm`, `id`, and, for a
  `cmd`, `type`/`from`/`ref`/`body`. `event` (delivery kind) and `type` (the cmd's kind:
  directive/challenge/reply/verdict) are **different fields** — don't collapse them. Every
  line also leads with `"v"` (protocol version); ignore fields you don't recognize. The
  exact table is GUIDE §3.
- **Re-arm iff `rearm` is `true`.** A `fatal` event is `rearm:false` → stop, you're misconfigured.
- **Persist the `id`** and pass it back as `--since <id>` on the next arm — that's your
  cursor (at-least-once; no replay of what you already handled). No `--since` = start at "now".

## Before you act — are you piloted or autonomous?
```bash
agentbus pilot status     # "piloted by hermes"  OR  "autonomous"
```
- **piloted** → wait for a directive (arm `subscribe`); don't act on your own initiative.
- **autonomous** → proceed on your own plan; just keep emitting `status`/`report`.

## Your `status` IS your heartbeat
Agents are one-shot CLI calls, not daemons — there is no separate heartbeat. Emit
`agentbus status <self> <working|idle|blocked|done> <msg>` when your state changes, and
`agentbus report <self> <msg>` at milestones (its full text is retained — `reports <id>`
reads it). busmon ages you to idle/offline from your last entry. While armed and waiting
you are **`idle`, never `blocked`**.

## Gating risky actions (the 4-eyes money-path)
`blocked` is reserved for an **open 4-eyes gate**. Before proceeding or marking done:
```bash
agentbus gate <self>      # lists open challenges; NON-ZERO exit = you are gated
```
To review a peer's risky change: `agentbus challenge <target> <why>` opens a gate; they
`reply --ref <R>`; a **second, independent** agent resolves it with `verdict … approve|reject`
(a self-approval never counts). Query the recorded state with `agentbus verdicts`.

## The whole bus in one line
> Every stream is `{project}:{kind}`. Publish `status`/`report`, receive with `subscribe`
> (wake-on-exit — re-arm iff `rearm`, persist the `id` cursor), gate the risky with
> `challenge`/`verdict`, and read exact flags & JSON from `docs/AGENT-BUS-GUIDE.md`.
