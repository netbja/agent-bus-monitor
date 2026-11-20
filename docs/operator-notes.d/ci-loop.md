- **A failed CI check goes back to its author, at most `ci.max_handoffs` times, then to the person (ci-loop,
  spec `2026-11-19-ci-loop-design.md`).** GitHub with `gh` only: the merge-queue pass's one `gh pr list` also
  reads `statusCheckRollup`; GitLab, Bitbucket and the local forge get no loop until they have a PR reader.
  `ci.loop` (default on, `AGENT_BUS_CI_LOOP=off`) and `ci.max_handoffs` (default 3; 0 sends every red PR
  straight to the stop). A PR is red when an entry failed (`FAILURE`, `TIMED_OUT`, `STARTUP_FAILURE`; a status
  `FAILURE` or `ERROR`) and nothing still runs; `CANCELLED` and `ACTION_REQUIRED` are not failures, the
  latest of two same-named entries wins, a draft is skipped. The owner is the one distinct `author` of the
  verdict ledger's entries on the head (self-addressed verdicts ignored), else master; the owner's directive
  wakes it, master gets a copy with `wake=no`. One episode (PR, head, sorted failing names; its sha1 digest in
  `{p}:ci`, a runtime hash, `pr:<n>` = `digest|count|stopped|done`) is handed off once; a new head counts
  toward the same cap, green does not reset it. At the cap the loop stops: master's directive (`wake=no`) and
  the person's notice (For you) naming `agentbus ci reset <pr>` (the person's only, refused in an agent's
  environment; also busmon's `/ci reset <pr>`). An owner that declared `blocked` or `waiting` holds a
  hand-off, never the stop. Every write is `bus.FencedCIStep` (one fenced script, each part recorded):
  **at least once**, a duplicate harmless (same text, same `episode`), never exactly once; a closed PR's field
  is deleted (`bus.FencedCIForget`). The loop's failures are the merge-queue record's `ci` field, named by
  its health check beside the queue's outcome.
  - **The base is checked before a hand-off.** For the bases of the red PRs that reach a new decision, one
    `gh api graphql` call per base reads its latest commit's rollup. A PR with a failing check that also
    fails on a red base is held (neither handed off nor stopped, nothing counted), and master is woken once
    per base episode (`FencedCmdOnce`, mark `ci:base:<branch>` in `{p}:quota`); a check that still runs on
    the base makes the PR wait; a base that cannot be read sends nothing for its PRs (named in `ci`). A base
    with no CI on push has an empty rollup: it is not red, and the loop runs as written. The base is matched
    **by check name**: a CI that posts a different context for a PR than for a push (Jenkins
    `…/pr-merge` against `…/branch`) never holds a PR, which then goes back to its author as before.
  - **Silence is never "all green".** A check that never finishes (`EXPECTED` or `PENDING` forever, a
    required check never posted because the CI server is down), a base that stays running, or a base read
    that keeps failing keeps the PR silent, its stop included.
  - **The hand-off gives a link, not the log.** It names `gh run view --log-failed` only for a GitHub
    Actions check run (a `CheckRun` with a workflow); a commit status or another app's check run gives its
    URL alone. The agent must be able to reach that URL (an internal Jenkins, for example).
