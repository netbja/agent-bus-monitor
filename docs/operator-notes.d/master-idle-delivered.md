- **What master has not delivered is judged by git alone (master-idle §3.1, part A).** Each merge-queue pass
  (forge and `none` modes, the `gh` path too) reads origin's tips (`RemoteTips`), fetches the base, and writes
  `undelivered` in `{p}:mergequeue`: the approved deciding heads within 72 h that are no tip on origin, not
  dropped, whose plan task is not done, and that `mergequeue.Delivery` does not find in the base. Its test, per
  head, cheapest first: `InBase`; the branch's zero-context patch-id (`git diff -U0 <fork> <head> | git patch-id
  --stable`) among the base's (a squash); every non-merge commit's among them (a rebase-merge, only for a head
  with no merge commit in `<fork>..<head>`). The base's patch-ids are those of the non-merge commits since the
  judged forks, read once per pass; `Delivery` caches them by SHA across passes (a restart recomputes) and drops
  what left every range. Every diff runs `-U0 --no-ext-diff --no-textconv --no-renames` (and `show
  --no-show-signature`): no driver of the repository's config runs. Accepted risk (the person): a one-commit
  head whose `-U0` change equals an unrelated base commit's (the same version bump) reads delivered. A failed
  read fails the pass (the last list kept). `forge.None` (new): `auto` resolves an origin on an unknown host to
  it (was local); `deliver` pushes and prints no page. `agentbus merge-queue drop <head> "<reason>"` (only a
  `delivers` role or the person; a short SHA resolved against the approved heads, ambiguous refused) writes
  `{p}:mergequeue:dropped` (a record kind, `{reason, by, at}` from Redis `TIME`), `undrop` deletes it, the pass
  prunes drops past 72 h in its fenced write; `merge-queue` prints the undelivered and the dropped heads. The
  identity guard checks `drop`/`undrop` (no longer read-only).
