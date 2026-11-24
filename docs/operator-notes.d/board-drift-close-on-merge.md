- **A review request closes when its branch merges (board-drift step 2, spec §3).** `request review
  <task> --branch <b> [--commit <sha>] [--more]`, run in the branch's checkout, records the head
  (`--commit`, else the branch's tip) and `review_base`, the base tip it read (`origin/<base>` after a
  fetch; the local `<base>` in local mode). It refuses a head already in that base ("nothing to
  review"); a failed fetch records no base, and the request is then never closed on merge. Each pass,
  the merge queue judges the review requests that have a branch and a `review_base` with its
  `Delivered` test (the branch's remote tip, else its local one, else the recorded commit, which covers
  a branch auto-deleted after the merge; a head already in its `review_base` is never judged; local
  refs that cannot be read judge nothing). Each review counts (`review_n`): the write applies only to
  the review the pass read, so one put in review again meanwhile is left. `request done` on a request
  closed on merge answers as on any done request: no error, no reply. A delivered one is closed by
  the fenced `FencedRequestMerged`: done, `closed_on_merge {head, base}`, no reply, so nobody is woken
  (Q3); busmon's request detail says "closed on merge <head> into <base>". With `--more` the request
  goes back to `accepted` instead, the head added to `merged_steps`, and the last step (no `--more`)
  closes it. An `accepted` request is never closed on merge. One `request-merged` event either way.
