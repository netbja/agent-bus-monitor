---
id: 2026-11-17-quota-reliability
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Per-provider quota reliability (the person's item 23)

## Finding/decision

The quota readings come from a different local source per provider, each with its own freshness and failure modes; a stale or missing reading drives the warn, drain and hold.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Audit first, per provider: the source, how old a reading can be, what happens when it fails. Then fix the weakest. No new reader before that audit.

## When it applies

When a quota decision (warn, drain, hold) is doubted, or a provider is added.

## Evidence

[quota/claude.go](../../../quota/claude.go), [quota/codex.go](../../../quota/codex.go).

## Limits / what would invalidate it

A provider without a local source stays unknown, never zero.
