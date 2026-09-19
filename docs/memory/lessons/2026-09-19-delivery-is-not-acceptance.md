---
id: 2026-09-19-delivery-is-not-acceptance
project: agent-bus-monitor
scope: transferable
kind: lesson
status: active
created: 2026-09-19
last_verified: 2026-09-19
author: codex
confidence: verified
---
# Output, acceptance and completion are different evidence

## Finding

Successful subscriber output plus technical ACK is not proof an agent read the
message. Explicit request accept records ownership; request done records a reply.
A crash after output but before ACK can cause duplicate delivery. A crash after
ACK but before action does not trigger automatic action replay.

## When it applies

Read request state from the existing board. `queued` means no attempt recorded;
`output_written` means complete output and technical ACK were recorded together.
The emitted JSON reports `uncertain` because ACK occurs after output. A complete
cmd JSON can therefore accompany exit 75 when ACK fails. Deduplicate by project
and message ID, and inspect tracked task state before repeating work.

First acceptance checks expiry and retained body. Already accepted work can be
resumed after block and completed after expiry. Generic board claim/state/done
reject tracked requests. Explicit board drop removes metadata without cancelling
the command or action. Stream retention removes bodies, not the board record.

## Evidence

- [Shared contract and review clarifications](../../coordination-v1-contract.md)
- [Request transitions](https://github.com/netbja/agent-bus-monitor/blob/16c774f/bus/requests.go)
- [CLI protocol reference](../../AGENT-BUS-GUIDE.md)

## Limits

Agent identity is declared by the CLI environment, not independently authenticated
as a person. No exactly-once agent-action guarantee. A missing acceptance record
must never be presented as proof that an agent ignored the task.
