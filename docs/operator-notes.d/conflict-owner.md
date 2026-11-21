- **The merge queue's conflict check (conflict-owner §2, build A: detection, nothing sent yet).** Each
  pass judges the approved heads and the PRs the forge flags with `git merge-tree --write-tree` against
  the base tip (`mergequeue.CheckConflicts`), oldest approved first. It runs in an empty bare repository
  made for the pass in the project's state directory and removed after, with only `PATH` and an
  isolated `HOME`, no global or system config or attributes, and `--attr-source=<empty tree>`, its
  objects through `objects/info/alternates`: no merge driver, hook or attribute of the repository
  runs, and no ref or `FETCH_HEAD` is written (absent heads are fetched once with `--refmap=`; local
  mode fetches nothing). git older than 2.42 is "too old", nothing judged. A confirmed conflict
  leaves the items for `Conflicting` with `branch`, `head`, `base_tip` and `files` (the first 10);
  a forge flag git finds clean is an item again; what git could not judge (absent head, time) stays as
  before and is named in the record's `conflict_check`, also printed by `agentbus merge-queue`.
  `mergequeue.ConflictOwner` (the board's claim or request branch, else a `<role>/` prefix that is a
  registered agent, else master) is built and tested, used by build B, which sends the hand-offs.
