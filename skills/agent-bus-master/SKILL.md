---
name: agent-bus-master
description: "Run from the MASTER agent (the pilot-lease driver) inside herdr to coordinate peer agents over the Agent Bus: resync an agent by injecting text into its herdr pane, and unblock an agent stuck on an on-screen question (detect it, alert a human, then inject the human's answer). Use when you hold the pilot lease and need to drive other agents' panes."
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
