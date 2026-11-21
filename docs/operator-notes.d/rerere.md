- **`rerere.enabled` is on in the shared repository, `rerere.autoUpdate` is not.** Master ran
  `git config rerere.enabled true` once (merge-conflicts spec §3, the person's yes of
  2026-11-21). The cache is shared by every worktree, so a conflict resolved once replays for
  the next agent who hits it. A replayed resolution stays unstaged: the agent still reads it,
  stages it, and runs the tests. Never set `rerere.autoUpdate`, and no agent resolves conflicts
  automatically. `git rerere forget <path>` drops a wrong recorded resolution.
