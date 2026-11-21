- **`agentbus update` removes the old release trees nothing needs** (quiet-update-auto spec §5, the first
  build step). After a successful update, right after the old snapshots, each
  `${XDG_DATA_HOME:-~/.local/share}/agentbus/src/vX.Y.Z` that its clone lists as a worktree is removed
  with `git worktree remove` (never `--force`) unless it is the installed version, a kept snapshot names
  it (a rollback needs it), a holder build of it is still on disk (a unit or a binary copy), or it has
  changes (yours: kept and named). The old holders that neither run nor answer are collected first, so a
  forgotten one does not pin its tree. An unreadable fact keeps every tree. Nothing else under `src` is
  touched. `agentbus update --list` shows each tree and why it is kept.
