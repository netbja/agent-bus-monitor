- **What waits for the person's merge is derived, never remembered** (merge queue, spec
  `docs/superpowers/specs/2026-11-05-merge-queue-design.md`, package `mergequeue/`). `mergequeue.Derive` (pure) joins
  the verdict ledger with the open PRs **by head commit**, never by `pr:<n>` subject: an item is an open, non-draft PR
  whose head is a subject's deciding approved head (`bus.RollUp` APPROVED, the decider's `Head`, `deliver`'s own rule);
  every other open PR is counted (`Unapproved`: a newer push, no review, an approve trimmed out, a late approve on an
  older head), never silent. An approved PR that GitHub reports `mergeable: CONFLICTING` is no item either: the
  person cannot merge it; it is listed in the record's `conflicting` (PR number and the PR's own base, which may differ
  from `delivery.base`; `agentbus merge-queue` prints "approved but conflicting with their base, to rebase:
  #500 onto main") for master to have it rebased. `UNKNOWN` (not computed yet)
  stays an item. A closed or merged PR is not in the open list, so never an item. Sources: `gh pr list --state open --json … --limit 1000` (`ReadGitHub`; a list of exactly
  the limit is `ErrIncomplete`, a pass read as unknown), else the git fallback (GitHub without `gh`, GitLab and
  Bitbucket for now, local): `RemoteTips` (`ls-remote --heads`), `FetchBase` (`--no-write-fetch-head --no-tags
  --refmap=`: the empty refmap stops git moving `refs/remotes/origin/<base>`; no ref written, tested byte for byte),
  `InBase` (an ancestor, or no diff: "contents in base", a squash or a revert); local mode reads `LocalTips` (a local
  branch whose tip is the head; no origin with `delivery.forge = auto` is local, as for `deliver`). Every command runs
  with `mergeQueueEnv` (the github check's `probeEnviron`: no askpass, `GH_PROMPT_DISABLED=1`, `GIT_TERMINAL_PROMPT=0`,
  `LC_ALL=C`), 10 s each; agentbus reads no token. The dispatch service's chore (`internal/dispatchsvc/mergequeuechore.go`,
  `dispatch.MergeQueueConfig`, 5 min, 20 s, only while `HoldsDispatch`) replaces the fenced cache `{p}:mergequeue`
  (`bus/mergequeue.go`, a hash, `PEXPIRE` 15 min, runtime, ignored by `HasKeysBesidesDispatch`): `attempted_at` every
  pass, `items_at` and the items only on a success, so a failed pass keeps the last items and never makes them look
  fresh; the reason is said. Readers: busmon (on `requestPoll`, optional `mergeQueueReader`; For you rows at
  `rankMerge`, a decision, `(as of HH:MM: reason)` while a pass fails, one `merge:unknown` row (never a decision) when
  nothing is known; Enter or the reminder open `ovMerge`, `y` the PR's link or in local mode `git switch <base> && git
  merge --no-ff <head>`, `Y` the reviewers' messages), `agentbus merge-queue [--json] [--live]` (read-only: `--live`
  derives in the caller's checkout and writes nothing; exit 0 items, 64 none, 2 unknown), health `merge-queue` (for
  you, never fail), a `decision` notification per item (episode `merge:<pr or branch>|<head>`; none for an item a
  waiting plan task announces, and the check's own warn is never an event), and `plan.Derive` (`Inputs.Merge`, read
  by `plan.ReadWithVerdicts` and busmon's poll): an approved merge task whose subject is queued waits for the person
  with no PR link; nothing makes it done.
