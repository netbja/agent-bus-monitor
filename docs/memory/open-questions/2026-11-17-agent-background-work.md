---
id: 2026-11-17-agent-background-work
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Agents' background shells and sub-agents visible in Agents (the person's item 24)

## Finding/decision

An agent's row shows its background work as a count ("… 2 checks running 4m"); its background shells and sub-agents are not listed one by one.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Fold it into the existing idea i2 ("Sub-agent child rows in busmon", `agentbus idea list`): one spec for both, child rows under the agent, read-only.

## When it applies

When busmon's Agents rows or the background read change.

## Evidence

[cmd/busmon/background.go](../../../cmd/busmon/background.go) (what is shown today).

## Limits / what would invalidate it

What each client exposes about its sub-agents differs; the spec starts from what can be read without screen parsing.
