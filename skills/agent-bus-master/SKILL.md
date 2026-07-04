---
name: agent-bus-master
description: "Run from the MASTER agent (the pilot-lease driver) inside herdr to coordinate peer agents over the Agent Bus: resync an agent by injecting text into its herdr pane, unblock an agent stuck on an on-screen question, and dispatch a multi-task implementation plan across an implementer + reviewer pair. Use when you hold the pilot lease and need to drive other agents' panes, or when you're running a task-by-task plan through the bus."
---

# Agent Bus — Master Skill

You are the **master** (you hold the pilot lease), running inside herdr. This skill drives peer
agents' herdr panes over the Agent Bus.

## Check first
- `HERDR_ENV=1` — you must be inside a herdr pane (you control panes via the `herdr` CLI). If unset, stop.
- `AGENT_BUS_PROJECT` and `AGENT_BUS_AGENT` exported (see `docs/AGENT-BUS-GUIDE.md`).
- You hold the lease: `agentbus pilot status` prints `piloted by <you>`. If not, claim it
  (`agentbus pilot claim`) or stop — only the master drives panes.

## Agent → pane bridge
Each peer registers its pane (`HERDR_PANE_ID`) via its `agentbus status` heartbeat:
```bash
agentbus agents --json          # {"claude1":{"state":...,"pane":"w1:p1"},...}
agentbus pane claude1           # just the pane id; non-zero exit if none
```
herdr pane ids are NOT durable. Before acting, confirm the stored id is still live with
`herdr pane list`; if it's gone, re-resolve by matching the agent/cwd in that output.

## Resync — inject text into an agent's pane
```bash
pane=$(agentbus pane claude1) || { echo "claude1 has no registered pane"; exit 1; }
herdr pane send-text "$pane" "<text / context to inject>"
herdr pane send-keys "$pane" Enter
```

## Unblock — answer an agent stuck on a question
1. **Detect** blocked peers:
   ```bash
   herdr pane list --json | jq -r '.result.panes[] | select(.agent_status=="blocked") | .pane_id'
   ```
   Map each blocked pane back to its bus agent by inverting `agentbus agents --json`.
2. **Read the question:** `herdr pane read "$pane" --source detection`.
3. **Alert a human** (one-way Signal + the bus — they may be away):
   ```bash
   hermes-notify "claude1 is BLOCKED: <question>"
   agentbus notify "claude1 BLOCKED: <question> — reply in busmon: @claude1 <answer>"
   ```
4. **Receive the answer over the bus.** The human answers in busmon (`@claude1 <answer>`, type `@`
   for agent autocomplete) or `agentbus cmd claude1 <answer>` — either way a cmd to `claude1`.
   Watch directed cmds read-only: `agentbus listen cmd`.
5. **Inject** the answer (the Resync step) into the blocked pane to unblock it. **Only inject into
   agents you have confirmed are currently `blocked`** — never interrupt an actively-working agent.

### Known edge
A cmd to a currently-blocked agent also lands in that agent's `subscribe` consumer group, so on its
next re-arm it could re-receive the answer as a directive — mitigated by the agent persisting its
`--since` cursor (see the bus guide). Don't re-inject an answer you've already delivered.

## Broadcast team budget
Give the team a regular budget readout. Each agent's status-line script tees its usage to the bus
(see `docs/AGENT-BUS-GUIDE.md` → "Status-line usage tee"); read it and post a one-line summary:
```bash
agentbus usage                                  # the team budget table (or --json)
agentbus notify "budget — claude1 99%/36m · claude2 41%/2h"   # periodic one-line summary
```
Distribution is **notify + pull**: the summary lands on `{project}:notify` (visible in busmon and to
the human), and agents read `agentbus usage` themselves on demand. Never push budget via `cmd` to
each agent — that wakes every agent's `subscribe`.

## Pipeline dispatch — task-by-task with review gate

Driving a multi-task plan through an implementer (e.g. `claude-worker`) + a reviewer (e.g.
`claude-4eyes`):

1. **Point at the plan, don't paste it** — if peers share your repo/cwd, `agentbus cmd claude-worker
   "Start Task N of <plan-file-path>. TDD, one task, report + hold when green."` beats pasting the
   task's full text; peers already have the file, pasting wastes tokens and drifts from the source
   of truth if the plan changes.
2. **One task in flight at a time.** Don't dispatch N+1 until N clears BOTH: worker reports done,
   AND the reviewer reports approve. An unreviewed "done" isn't done.
3. **`agentbus reports --json` entries carry the full text in `.Full`** — no separate lookup needed,
   the plain-text tail view is the one that truncates.
4. **If you poll `agentbus reports --json` yourself (e.g. from a background watcher), track the last
   seen `.ID`, not the array length** — the endpoint is a capped/rolling recent-reports window, so
   the count plateaus once you're past the cap and a length-diff check silently stops firing even
   though new reports keep arriving.
5. **Route review over `cmd`, not the formal `challenge`/`verdict` gate** — reserve that mechanism
   for an actual blocking risk (money-path, prod migration), not routine per-task review; plain
   cmd→report round-trips are cheaper and sufficient.
6. **Independently verify what you can** (`git log`, `gh run list`, a real command) rather than
   trusting self-reports alone — cheap, and catches a report that's technically true but incomplete.
7. **Plan-level sign-off before declaring the whole thing done** — one final review against the
   plan's Definition of Done, not just the last task's diff. Per-task review doesn't prove the
   pieces integrate.

## Gotchas

- **Shared session budget**: the whole team often draws one account's session pool. Near the limit,
  broadcast a hold — finish the in-flight task, get it reviewed and committed, then idle — don't
  dispatch anything new until the reset. Resume normally once it drops back down.
- **Not every peer stays subscribe-armed** — an agent may be deliberately unsubscribed to cut cost
  (expensive model, long idle stretches). `agentbus cmd` alone won't wake it. Check `agentbus agents`
  staleness; if it's stale by design, reach it via Resync (pane injection) instead — and keep the
  injection terse, unsubscribed doesn't exempt it from a shared session budget either.
- **Context-window creep**: watch each pane's status-line `Ctx:` figure alongside session%. Near a
  high-context threshold, have the agent write a hand-off (current step, what's committed, what's
  pending) BEFORE you `/clear` its pane — never clear first and sort it out after.
