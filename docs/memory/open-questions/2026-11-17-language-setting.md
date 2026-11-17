---
id: 2026-11-17-language-setting
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# A language setting for busmon and the CLI (the person's item 17)

## Finding/decision

busmon and agentbus speak English only; the person would choose the language of what they read.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Spec first: which surfaces follow the setting (busmon's screens, the CLI's messages to the person), with one registry key in `config/keys.go`. What agents read on the bus stays English, so one language serves every model.

## When it applies

When busmon or CLI text for the person is written or translated.

## Evidence

[config/keys.go](../../../config/keys.go) (where the setting would live).

## Limits / what would invalidate it

Decided by the person; nothing is built until they say yes.
