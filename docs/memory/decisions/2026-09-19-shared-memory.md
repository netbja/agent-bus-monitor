---
id: 2026-09-19-shared-memory
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-09-19
last_verified: 2026-09-19
author: codex
confidence: verified
---
# One shared reference, explicit read/write duties

## Decision

Use reviewed Markdown in this repository for project decisions, reusable lessons
and questions. Use Obsidian to view those same files. Keep Redis for delivery and
operational request state. No new memory service or automatic transcript ingestion
is introduced. A later search index must be rebuildable from the reference files.

Shared memory requires both the files and a boot/handoff duty in every agent's
instructions. They must be delivered together. Private Claude/Codex/other memory
is a scratchpad or a pointer, not an independently authoritative project history.

Existing brain notes are preserved as external historical material. Promote a
lesson only after checking its source, scope and applicability; do not import
another project's status into this project's reference or declare its vault dead.

## Evidence

Bernard authorized implementation jointly by Codex and Claude on 2026-09-19 after
comparing the Markdown/Obsidian/Redis proposal. The implementable specification is
[the shared protocol](../PROTOCOL.md) and [access boundaries](../ACCESS.md).
The local audit found two brain files last modified July 31 and no references to
that vault in this project's roles/skills. Claude reports a living journal in
ai-tradex-solana maintained by its sentinel; this is his observation, not an
independent audit of that project's runtime here.

## Limits

Git does not make an uncommitted file cross-host memory. The first slice defines
reference paths and agent duties; it does not prove every running session has
loaded them, configure Obsidian remotely, or enroll other projects. Track those
checks in [the adoption questions](../open-questions/2026-09-19-adoption.md).
Redis is not inherently incapable of durable storage: the choice here is about
reviewability, existing tools, and avoiding a second editable reference.
