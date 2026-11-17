---
id: 2026-11-17-holder-g2-split
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Holder G2: splitting the holder into a PTY owner and a restartable core

## Finding/decision

The holder-hardening spec's option G2 splits the holder like herdr-core, so a core crash or upgrade keeps every program running (5-7 PRs, 2-3 weeks).

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): An architect decision, after G1 part 2: G1 then G2 is possible, G1's per-terminal emulator and tail being what G2's core would need.

## When it applies

When the holder's crash or upgrade behaviour is planned.

## Evidence

[the holder-hardening spec](../../superpowers/specs/2026-11-17-holder-hardening-design.md) (option G2).

## Limits / what would invalidate it

A second protocol and process; the owner's own upgrade still needs a drain.
