# Shared memory — agent-bus-monitor

Read this index at session start/recovery; follow only the relevant links.
Notes are shared through reviewed Git changes, not through an agent's private
memory. Check the note's scope, status, verification date and evidence before use.

## How to use it

- [Read/write protocol and note template](PROTOCOL.md)
- [Human access in Obsidian and laptop/VDR access](ACCESS.md)
- [Project boundaries and cross-project knowledge](PROJECTS.md)
- [Validation and delivery status](VALIDATION.md)

## Decisions

- [Markdown reference, Obsidian view, Redis operational state](decisions/2026-09-19-shared-memory.md)

## Lessons

- [Inspect pending AND unread addressed commands before choosing a cursor](lessons/2026-09-19-pel-is-not-backlog.md)
- [Delivery, acceptance and completion are separate facts](lessons/2026-09-19-delivery-is-not-acceptance.md)
- [Installed binaries and test endpoints require explicit verification](lessons/2026-09-19-runtime-and-test-endpoints.md)
- [A worktree in /tmp holds work that nothing else holds](lessons/2026-09-19-worktrees-in-tmp.md)

## Open questions and historical checkpoints

- [Adoption and recovery decisions still to verify](open-questions/2026-09-19-adoption.md)
- [Journal index](journal/INDEX.md)

Current state must be reread from its owner: Git/PR for merge, runtime for deployed
version, board for tracked requests. A dated memory does not certify that state
is still true. This index must stay short; do not paste transcripts or bus dumps.
