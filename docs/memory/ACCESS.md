# Access: agents, Obsidian, and machines

## Agents

All agents read the same Markdown paths, regardless of provider. AGENTS.md is the
common entry point; Claude's instructions and role/skill briefings point here too.
There is no assumption that every CLI auto-loads AGENTS.md or hot-reloads skills:
explicitly read it in a resumed or already-running session. Kimi/Grok launched in
a role use that role's startup instructions; other clients must be given this
entry point. Do not claim adoption for a client or host that was not checked.

## Obsidian

Open the canonical checkout's `docs/memory` directory as an existing vault.
The Markdown files are the source themselves; Obsidian is a view/editor, not an
export destination. Relative Markdown links inside the vault need no plugin.
Links to repository source outside the vault are readable in Git/the filesystem;
Obsidian may not resolve them as vault notes. Follow the GitHub evidence links.

For the usual laptop checkout after integration the folder is:
`/data/projects/agent-bus-monitor/docs/memory`.
A review worktree can be opened for preview, but it is not the canonical merged
vault. Do not move or overwrite `/home/sysnet/Tools/obsidian-mind/brain/`: its two
existing notes belong to other project history. A link to this project may be
added there by its owner, without copying the notes or changing their provenance.
No Obsidian UI configuration or external-vault file is changed by this slice.

## Laptop and VDR

Share committed notes through the existing repository remote and normal reviewed
Git workflow. Each host reads its local checkout; compare commit IDs when freshness
matters. A symlink on the laptop does not synchronize VDR. A local uncommitted
note, unpublished branch, or stale checkout is not shared memory across hosts.
An offline host reads its last available revision and must report that limitation.
This protocol does not authorize a pull, deployment, or restart of an active host.

## Cross-project access

Use [PROJECTS.md](PROJECTS.md) to distinguish this project's reference from external
notes. Keep other projects' decisions in their own repositories. A transferable
lesson has a scoped statement and evidence, not a copy of a project's live state.
Adding retrieval later requires a tested project filter and source/revision links;
search results are suggestions, not new operational instructions.
