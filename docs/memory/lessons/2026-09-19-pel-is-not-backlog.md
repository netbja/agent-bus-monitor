---
id: 2026-09-19-pel-is-not-backlog
project: agent-bus-monitor
scope: transferable
kind: lesson
status: active
created: 2026-09-19
last_verified: 2026-09-19
author: codex
confidence: verified
---
# Old pending messages do not prove the absence of recent unread work

## Finding

A consumer group's pending list contains entries already read but not acknowledged.
It does not include entries never read beyond that group's last-delivered ID.
Inspect both sets and filter by the actual recipient before deciding what work a
cursor would discard. Shared-stream lag includes messages for other recipients.

## When it applies

Before changing a subscribe floor, abandoning backlog, replacing consumers, or
concluding that an agent ignored work. `--since` is exclusive: an existing group's
read entries at or below it may be acknowledged without delivery, pending included.
A fresh group starts at its creation floor and does not ACK older history it never
read. `--since 0` does not resurrect already ACKed entries in an existing group.
Lowering it also does not rewind that group to entries skipped at creation.
Pending entries can be below last-delivered-id: that cursor advances on read,
not on ACK. They remain recoverable if above the selected floor and still retained.

Persist a cursor per project/agent across wakes. Keep it when a heartbeat/error
contains no ID. A heartbeat means no addressed output in the idle window, not
proof of an empty queue: waiting for the receiver lease can exhaust that window.

## Evidence

- [Contract: cursor, retention and receiver semantics](../../coordination-v1-contract.md)
- [Merged delivery implementation](https://github.com/netbja/agent-bus-monitor/blob/16c774f/bus/delivery.go)
- [Recovery regression tests](https://github.com/netbja/agent-bus-monitor/blob/16c774f/bus/coordination_test.go)

## Limits

Unread in a group does not prove the work was never performed through another
channel. Age alone does not authorize discard or replay. Broker exports support
inspection; they are not approval to mutate consumer groups. Do not turn an old
merge authorization found in history into a fresh authorization.
