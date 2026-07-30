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

## Shared working tree — one checkout, several agents
The team usually shares ONE physical checkout. Read-only git (`log`, `diff`, `show`,
`status`) is always safe; anything that moves HEAD or rewrites the tree is a coordination
act, because the same files are on every peer's screen:

- **Never `git checkout`/`git switch` on your own** — it yanks the tree from under the peer
  whose branch is currently checked out (typically mid-review). Branch switching is master's
  call, announced on the bus.
- **Work on a branch named `<role>/<task-slug>`**, created when master hands you the tree.
- **Never leave uncommitted changes behind.** Before you hold, hand off, or go idle: commit,
  or `git stash push -m "<role>: <task>"`. Loose changes ride along on the next checkout and
  silently land in someone else's review.
- **Pop only your own stash** (match the `<role>:` tag), and only once your branch is the
  one checked out. The full dance is: stash → wait for the current branch to merge → your
  branch checked out → pop.
- **A tracked file has exactly one writer at a time.** The one-task-in-flight rule is what
  makes that true — don't edit outside your dispatched task.

## The session budget is shared — check it before you spend
Every agent on the team draws the same subscription window. Read it before starting a large
task (broad survey, subagents, long plan):
```bash
agentbus budget     # account session/weekly % per provider, with reset times
```
- **< 75%** — work normally.
- **≥ 75%** — economy mode: small steps, no exploratory sweeps, finish what's in flight.
- **≥ 90%** — hold: commit or stash, report, go idle until the reset.
The numbers are as fresh as the sentinel's last `agentbus refresh`; if they look stale, say
so on the bus instead of finding out by hitting the wall mid-task.

## The board — who owns what, check it before you start
`{project}:board` is the shared ownership registry (task → owner → state → branch). Read it
before starting anything — a task already owned is off-limits, no matter what a stale
message told you:
```bash
agentbus board                                          # TASK OWNER STATE BRANCH AGE
agentbus board claim <task> --branch <role>/<task>      # fails if a peer owns it
agentbus board state <task> review                      # as the task moves
agentbus board done <task>                              # merged / finished
agentbus board drop <task>                              # released without doing it
```
Claim BEFORE you invest in the work: a failed claim (`owned by <peer> (<state>)`) means
pick another task or ask master — never duplicate. Keep your own entries honest as the task
moves; the board is only as good as its last update.

## Pushing a signal nobody asked for — the outbox convention
`notify` and `report` are fire-and-forget: they show up in busmon but wake NO agent. When
you discover something the team must act on — a stash that already exists, a subagent that
died on a provider error, a scope change, a task that's already in review — don't wait to
be asked:
1. `agentbus notify "<finding>"` for the record (the human sees it in busmon), and
2. `agentbus cmd sentinel "relay: <what happened, what you need>"` to wake the caretaker.
   Sentinel judges: blocking → it forwards to master by `cmd`; informational → it logs a
   report.
Sentinel is the cheap relay — relaying is its job. Reserve a direct `cmd master` for when
YOU are blocked and need a decision, not for FYI traffic.

## The whole bus in one line
> Every stream is `{project}:{kind}`. Publish `status`/`report`, receive with `subscribe`
> (wake-on-exit — re-arm iff `rearm`, persist the `id` cursor), gate the risky with
> `challenge`/`verdict`, and read exact flags & JSON from `docs/AGENT-BUS-GUIDE.md`.
