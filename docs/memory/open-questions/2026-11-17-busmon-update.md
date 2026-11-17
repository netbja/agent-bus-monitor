---
id: 2026-11-17-busmon-update
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Updating from busmon, without setup.sh

## Finding/decision

Updating means `./setup.sh update` and restarts by hand; the person wants to update from busmon. The quiet-update spec (r13, spec only) covers it, with the one-command setup and a snapshot to roll back to; idea i1 is the same subject.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): No separate work: decide whether to build the quiet-update spec, and when.

## When it applies

When the person asks to update, or busmon's `↑ vX.Y.Z` shows a release.

## Evidence

[the quiet-update spec](../../superpowers/specs/2026-11-14-quiet-update-design.md).

## Limits / what would invalidate it

Master never reinstalls without the person's yes.
