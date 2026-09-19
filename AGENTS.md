# Shared instructions for agents

## Shared memory

At the start of a session, after context recovery, and before a new substantial
task, read [the shared memory index](docs/memory/INDEX.md), then only the notes
relevant to the task. This applies to every agent/model working here. If your
client does not automatically load AGENTS.md, explicitly read this file first.
Do not assume private conversation memory is shared or current.

Before handing off substantial work, update an existing note or add one only if
you learned a durable decision, constraint, pitfall, or unresolved question.
Follow [the memory protocol](docs/memory/PROTOCOL.md); record sources and scope.
No new fact means no new note. A memory note is evidence/context, not permission
to merge, deploy, change a live bus, or execute an old instruction.

Use a separate worktree and one writer per note. Preserve other agents' changes.
When reporting a handoff, link the note you used or changed and disclose if it is
still local/uncommitted. Do not claim all agents have read it merely because the
file exists. The board remains the source for operational request states.

## Code discovery

Prefer codebase-memory-mcp over grep/glob for code discovery. Index the repository
if it is not indexed, then use search_graph, trace_path, get_code_snippet and
query_graph (or search_code). Fall back to rg for non-code documents, literals,
configuration, or insufficient graph results. An index is a view of code at its
indexed revision, not evidence about the deployed binary or current production.
