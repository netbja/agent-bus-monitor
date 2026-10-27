# Shared memory — agent-bus-monitor

Read this index at session start/recovery; follow only the relevant links.
Notes are shared through reviewed Git changes, not through an agent's private
memory. Check the note's scope, status, verification date and evidence before use.

## How to use it

- [Read/write protocol and note template](PROTOCOL.md)
- [Human access in Obsidian and laptop/host1 access](ACCESS.md)
- [Project boundaries and cross-project knowledge](PROJECTS.md)
- [Validation and delivery status](VALIDATION.md)

## Decisions

- [agent-bus is developed as an agent-bus project: worktrees, installed release, 4-eyes on the bus (active)](decisions/2026-10-20-dogfood-team.md)
- [agent-bus as a herdr plugin: linked, Go, own templates, the picker never provisions (active)](decisions/2026-10-19-herdr-plugin.md)
- [One dispatcher per host, conductor stays per project; decided, not built (active)](decisions/2026-10-26-host-dispatcher.md)
- [Context fill as a percentage: from Claude Code's statusLine, published per agent (active)](decisions/2026-10-27-context-fill.md)
- [Project services: dispatch and missions under systemd/launchd, not tabs (active)](decisions/2026-10-14-project-services.md)
- [Event-driven dispatch: the one automated pane writer (active)](decisions/2026-10-10-event-driven-dispatch.md)
- [Provider-neutral roles and security review](decisions/2026-09-27-provider-neutral-security.md)
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
