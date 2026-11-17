---
id: 2026-11-17-fable-permissions
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# The Fable profile's permission mode

## Finding/decision

`roles.toml`'s claude-fable profile (architect's alternate, `pop architect --profile claude-fable`) runs with `permission = "bypassPermissions"`.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): The person decides: keep bypass (as the other autonomous profiles) or a mode that asks. Nothing changes until then.

## When it applies

When the claude-fable profile is popped, or the profiles' permissions are reviewed.

## Evidence

[roles.toml](../../../roles.toml) (`[profiles.claude-fable.options]`).

## Limits / what would invalidate it

A mode that asks blocks an autonomous pop until the person answers in its terminal.
