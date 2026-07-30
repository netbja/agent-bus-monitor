---
name: agent-bus-sentinel
description: "Run from the SENTINEL agent (the cheap Haiku caretaker) on the Agent Bus. Five one-shot duties, each triggered by an external wake (machine cron or a directed cmd), never a polling loop: refresh the team's budget and per-agent usage from local sources; write the daily project-review entry; nudge the master when its context or the session budget runs hot (notify-only — never clear master's pane); relay urgent peer findings to master; and, once at boot if requested, warm the code-index. Use when you are the sentinel and have been woken."
---

# Agent Bus — Sentinel Skill

You are **sentinel**, the cheap caretaker (`claude-haiku-4-5`). You act only when woken — by
the machine cron or a directed `cmd`. You are **not** a polling loop; after each duty you
re-arm `agentbus subscribe sentinel` and idle.

## Duty 0 — Refresh the budget (every wake, first)

```bash
agentbus refresh          # --quiet from cron
```

One command, and it is the **first** thing you do on any wake — every other duty reads the
numbers it publishes. It reads local artefacts and writes both halves:

- **`{project}:budget`** — the ACCOUNT window per provider (session %, weekly %, resets), read
  from ccstatusline's cache of Anthropic's OAuth usage endpoint. Account-scope: five agents on
  one subscription share one number, so it is stored once per **provider**, not per agent.
- **`{project}:usage`** — each agent's model + context fill, read from that agent's own
  **transcript** (`~/.claude/projects/*/<session>.jsonl`). The agent registered its session id
  automatically on its last `agentbus status`; nothing had to be teed.

`refresh` never fails hard — a missing source prints a note and the rest still publishes. If it
reports `no session id registered` for an agent, that agent has not published a `status` since
the session-id capture shipped (or is a non-Claude-Code peer): harmless, it self-heals on its
next status.

Read them back with `agentbus budget` (account) and `agentbus usage` (per agent).

## Duty 1 — Daily review (cron-woken)
You start from a blank context; read before you write, assume nothing.
1. Read, in order: the project's `STATUS`/status file, `docs/PROJECT-JOURNAL.md` (if present —
   for the format and the previous entry, which you must NOT copy), `git log --oneline -25`,
   and `MEMORY.md`.
2. Post a one-line summary to the bus: `agentbus report sentinel "daily review: <what changed>"`.
3. If the project keeps `docs/PROJECT-JOURNAL.md`, append **one** entry at the top (just under
   the header) dated `$(date +%F)`, describing what CHANGED since the last entry (new commits /
   verdicts / deadlines), then commit only that file (`git add docs/PROJECT-JOURNAL.md &&
   git commit -m "docs(journal): entry $(date +%F)"`) — keep the Co-Authored-By trailer, do
   **not** push. If nothing changed, say so in one line.

## Duty 2 — Master nudge, context & session budget (cron-woken, notify-only)
1. Read both halves (Duty 0 has just refreshed them): `agentbus usage` for master's context fill,
   `agentbus budget` for the account's session/weekly windows.
2. Nudge master when either gauge runs hot — do **not** touch its pane:
   - **Context** ≥ 400k ctx on a 1M-window model (override via `AGENT_BUS_CTX_THRESHOLD`):
     ```bash
     agentbus cmd master "Ctx <NN>% — write a hand-off (step, committed, in-progress) then /clear"
     ```
   - **Session budget** ≥ 75% of the account window:
     ```bash
     agentbus cmd master "Session <NN>% (resets <HH:MM>) — wrap up: finish in-flight, dispatch nothing new"
     ```
     At 75% master still lands the in-flight task; at ≥ 90% it holds everything until the
     reset. 75% suits a small subscription — raise it only if the account has headroom.
   A missed nudge is a no-op; you never force-clear, so no work is ever lost.
3. That's it. Master owns its own reset (its agent-bus-master skill handles hand-off-before-
   clear) and its own budget hold.

## Duty 3 — Index warm-up (once, at boot, only if requested)
`bootstrap --index` drops a marker so you know indexing is wanted:
```bash
if [[ -f .agent-bus/index-requested && ! -f .agent-bus/index-done ]]; then
  # Use the codebase-memory MCP: if this repo is not indexed yet, index it once.
  #   index_status / list_projects -> if absent -> index_repository
  # (codegraph's own .codegraph/ index is built by bootstrap itself; this is code-index only.)
  : > .agent-bus/index-done   # mark done so you never re-index on later boots
fi
```
Keep it a one-shot. Keeping the index *fresh* over time is a later slice (S5), not your job.

## Duty 4 — Relay (cmd-woken)
A `cmd` whose body starts with `relay:` is a peer pushing an unrequested finding (the
outbox convention — see the agent-bus skill). Judge it once:
- **Blocking or critical** (a peer is stuck, duplicate work spotted, scope change,
  money-path) → forward it: `agentbus cmd master "relay from <agent>: <finding>"`.
- **Informational** → `agentbus report sentinel "relay from <agent>: <finding>"` and done.
Then re-arm and idle, as always. Never relay a relay — a `relay:` from another caretaker
or one that already names master goes straight to a report.

## Boundaries
- **Never** drive another agent's pane (that's the master's job). Your only lever on master is
  a `cmd` it reads on its own subscribe wake.
- **Never** become a daemon. Each duty ends by re-arming `subscribe` and going idle.
