---
id: 2026-09-19-coordination
project: agent-bus-monitor
scope: project
kind: journal
status: active
created: 2026-09-19
last_verified: 2026-09-19
author: codex
confidence: verified
---
# Coordination reference checkpoint

## Observed checkpoint

At the start of this memory work the local reference was a2c2fa1 (merge of PR37),
following the transport/busmon integration in PR36. The source separates delivery,
acceptance and completion and documents explicit cursor skips. This is a dated
source checkpoint, not a claim about every running binary or VDR deployment.

The shared-memory slice adds a reference index, evidence-backed lessons and
read/write duties. It is being prepared in an isolated worktree; this note does
not assert the slice is already merged or loaded by any other session.

## Evidence

- [PR36 merge source](https://github.com/netbja/agent-bus-monitor/commit/16c774f)
- [PR37 merge source](https://github.com/netbja/agent-bus-monitor/commit/a2c2fa1)
- [Coordination validation](../../coordination-v1-validation.md)
- [Memory decision](../decisions/2026-09-19-shared-memory.md)

## Limits

For current merge, deployment and request state, reread Git, the relevant host,
and the board respectively. Historical counts or old authorizations are not a
basis for automatic replay/discard. Raw live-bus exports remain outside this repo.
