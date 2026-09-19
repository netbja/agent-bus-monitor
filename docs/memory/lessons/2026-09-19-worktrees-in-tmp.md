---
id: 2026-09-19-worktrees-in-tmp
project: agent-bus-monitor
scope: transferable
kind: lesson
status: active
created: 2026-09-19
last_verified: 2026-09-19
author: claude
confidence: verified
---

# A worktree in /tmp holds work that nothing else holds

**Finding.** Uncommitted work in a `/tmp` worktree is one cleanup away from gone,
and the agents most likely to leave work uncommitted are the ones least able to
avoid it. On 2026-09-19 the shared-memory package — the structure, the checker,
its 13 tests, and the v0.6.0 release notes — existed **only** as 19 uncommitted
entries under `/tmp/agent-bus-monitor-shared-memory`, because its author's Git
index was read-only in its sandbox. A second worktree,
`/tmp/agent-bus-monitor-test-isolation`, held the `AGENTBUS_TEST_REDIS_URL`
guard in the same state; that one is still unmerged today.

Nothing had failed. The work was reviewed, tested and correct. It was simply
sitting somewhere that `systemd-tmpfiles` empties on a schedule nobody consults.

**When it applies.** Any time an agent is given an isolated worktree, and
especially when that agent cannot commit — a read-only index, a sandbox denial, a
missing credential. Put worktrees under `$HOME` (a sibling of the repository, or
`~/agent-bus-worktrees/`), never `/tmp` or `/var/tmp`. When an agent reports that
it cannot commit, treat its worktree as a live incident and copy the content out
before doing anything else: a `git diff` for tracked changes plus the untracked
files, into a dated directory under `$HOME`.

**Evidence.** The rescue of both packages is recorded in
[the v0.6.0 release notes](../../releases/v0.6.0.md), which name
`~/agent-bus-rescue/` as the preserved location of the test guard that did not
make the release. The shared-memory package survived and merged as PR #39; had
`/tmp` been cleaned that morning, a day of two agents' work would have gone with
it, review included.

**Limits / what would invalidate it.** This is about *uncommitted* work only —
a pushed branch is safe wherever its worktree lives. It says nothing about
whether `/tmp` is cleaned on any particular host; the point is that its lifetime
is not yours to reason about. If worktree creation is ever handled by a tool that
places and cleans them deliberately, follow that tool instead of this note.
