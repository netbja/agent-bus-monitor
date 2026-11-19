# A failed check goes back to its author, a few times, then to the person (ci-loop)

Status: spec, r10, not built. r10, the person's answers (§8, all decided) and three points: the
log hint names `gh run view --log-failed` only for an Actions check run (A); the base is checked
before a hand-off, so a red base is not the author's failure (B, §4 "The base"); a check that never
finishes, or a base that stays running, makes a PR silent, written in the operator notes (C).
Status before r10: spec, r9. r2: every write of one action in one script (reviewer-codex), and the owner rule's source and fields spelled out. r3 (reviewer-codex): a Redis error inside the script does not undo earlier writes. The types are preflighted, each part's progress is recorded, and the retry writes only what is missing. The guarantee is stated separately for our process stopping and for Redis refusing a command. r4 (foureyes): a hand-off only once the head's CI has finished; self-addressed verdicts ignored; master's swap key checked too; a held owner does not hold the stop; the declared state is named; the latest of two same-named checks wins. r5 was withdrawn (more machinery). r6, on master's ruling (simple and robust): the guarantee is stated as at least once, with a harmless and bounded duplicate, and no exactly-once claim. The record keeps a digest of the episode, not the raw names. `{p}:ci` is classified as runtime (foureyes r4, build notes). r7 (reviewer-codex): a stopped field stays stopped across new episodes, and only the stop's own unfinished parts are retried. The entries' mark has its own `ci:pr:<n>` prefix (foureyes, r6). r8, master's decision: a held owner delaying an unfinished hand-off's master copy is a known limitation, with a test, and no new mechanism. r9 (reviewer-codex): the delayed copy is sent only while its episode is still red, and is otherwise superseded. Idea 1 of
[the agent-orchestrator study](../../memory/open-questions/2026-11-19-ci-loop-and-status-ideas.md).
The person said yes to a CI loop. It must stay simple and robust: when a PR's checks fail, a
bounded loop hands the failure to the author, with a retry cap and a stop to the person. §8 records
the person's answers (2026-11-19, r10): every question is decided.

## 1. What exists, and what is missing

| Already built | Where |
|---|---|
| A pass every 5 minutes (20 s budget) that runs `gh pr list` once and records the open PRs, the approved ones as the merge queue | `cmd/agentbus/mergequeuechore.go`, `mergequeue/forge.go` (`ReadGitHub`) |
| The forge's free text, cleaned: one line, no control characters, redacted | `mergequeue.cleanText` |
| A dispatcher directive sent once per episode, fenced, waking its target the usual way | `bus.FencedCmdOnce` (`bus/once.go`) |
| A notice to the person, once per episode, fenced | `bus.FencedNotifyOnceForPerson` |
| Who wrote a head: the verdict ledger's `author` on every verdict that names that `head` | `bus.Verdict` |
| An agent's state (`blocked`, `waiting`, `working`, `idle`) | `{p}:agents` |

Missing: the check results are never read; nobody is told when a PR's checks fail.

**Scope.** GitHub with `gh` only, the forge the pass already reads. GitLab, Bitbucket and the
local forge have no PR reader yet (`mergequeuechore.go`, the git fallback). They get no CI loop and
no error until they have one (§8 Q7). Their PR readers go on the roadmap as a separate step: the
project must run on any forge. With no forge there is no status to read, and nothing to do. This repository has no GitHub Actions, so here
the loop stays silent: it is for the person's other projects.

## 2. Reading the checks

The same `gh pr list` call gets one more field, `statusCheckRollup`. There is no new process, no
new cadence and no new credential: `gh` uses its own credentials, as today.

A PR's checks are **red** when at least one entry has failed:

- a check run (`__typename` `CheckRun`) whose `status` is `COMPLETED` and whose `conclusion` is
  `FAILURE`, `TIMED_OUT` or `STARTUP_FAILURE`;
- a commit status (`StatusContext`) whose `state` is `FAILURE` or `ERROR`.

Anything else does not count as a failure:

- `PENDING`, `QUEUED` or `IN_PROGRESS`;
- `SUCCESS`, `NEUTRAL` or `SKIPPED`;
- `CANCELLED` and `ACTION_REQUIRED` (§8 Q4). A PR whose only check is `ACTION_REQUIRED` stays
  silent, which the person accepts.

**The head's CI must be finished.** The loop acts on a PR only when its rollup has **no entry still
running**: no check run that is not `COMPLETED`, and no commit status that is `PENDING` or
`EXPECTED` (foureyes, r2). Then one push gives one hand-off, which lists every failure at once.
Checks that fail one by one would otherwise make a new episode each, and use the cap on a single
head. A PR whose CI is red but still running waits for the next pass.

If a check never finishes, the PR is never handed off (C). A status stuck at `EXPECTED` or
`PENDING`, for example a required check never posted because the CI server is down, keeps the PR
silent. There is no new mechanism: the operator-notes fragment (§7) says so, so that silence is never
read as "all green". The same holds for a base that stays running (§4, "The base").

**Duplicate names.** A re-run can leave two rollup entries with the same name. The latest wins, by
the entry's time (for a check run, its completion, else its start; for a status, its creation).
The exact JSON field names `gh` gives are read off a real rollup at build time, not assumed here. A
re-run that passed is then not counted red.

Each failing entry keeps two fields, both cleaned with `cleanText`, and its kind:

- its name: `name` for a check run, `context` for a commit status;
- its URL: `detailsUrl`, or `targetUrl` for a commit status;
- whether it is a GitHub Actions check run: a `CheckRun` whose app is GitHub Actions (the exact
  field is read off a real rollup at build time, like the others).

The list is sorted by name, and at most 10 are kept, plus "and N more". **The log is never read.**
No CI output reaches the bus, Redis or a pane. The author reads the log itself, from the URL.

**The log hint names `gh run view --log-failed` only for an Actions check run** (A). That command
reads only an Actions run. A Jenkins job, or any CI that posts commit statuses (`StatusContext`) or
its own check runs to GitHub, is detected the same way, but its entry gives the URL alone. The
operator-notes fragment says the agent must be able to reach that URL (an internal Jenkins, for
example): the loop hands over a link, not the log.

A draft PR is skipped. If the read fails (timeout, gh missing, a cut list), the pass sends nothing,
the same as when the merge queue cannot be read; that is never taken as "all green".

## 3. The owner

**Where the data comes from.** The owner is read from the verdict ledger, `{p}:verdicts`. That is a
Redis stream, read with `bus.Verdicts(ctx, "")`, which the pass already calls each time for the
merge queue (`vs` in `deriveRun`). There is no further read, no forge call and no subprocess.

**Which field.** Each ledger entry has two names:

- `author`: the agent that opened the challenge, which is the one that wrote the change;
- `reviewer`: the agent that gave the verdict.

`head` is the full sha the reviewer read (`verdict --head`). The owner rule reads `author` and
ignores `reviewer`. If two reviewers approve one head, both entries still carry the same author:
the coder who challenged them. That counts as one author, not several.

**A self-addressed verdict is ignored** (`reviewer == author`, which some reviewers have written).
`author` is the name the reviewer types; it is not copied from the challenge. Ignoring these follows
`mergequeue/derive.go` (`candidates`) and `bus/verdicts.go` (foureyes, r2).

**The rule.** For the PR's `headRefOid`, take the set of distinct `author` values over the ledger
entries whose `head` equals it and whose `reviewer` is not the `author`:

- one author: that agent is the owner;
- none, meaning no entry names that head (a head pushed without a review, or one whose entries the
  ledger has trimmed): master, meaning the services' escalation target, as `escalateTo` reads it
  today;
- two or more: master. Two agents each challenged the same commit, for example a coder and docs
  agent on one shared branch. Neither is "the" author, and master knows who did what. This is
  rare, and sending it to master is safe.

In this team every pushed head is a reviewed head: master pushes only after both approvals. So in
the usual case there is exactly one author, the coder, and the ledger holds the answer with no new
record.

**The board is not used.** A request records a branch only when it is done (`--branch`), and only
sometimes; the ledger records a head on every approve.

**Master is kept informed, without being woken.** The owner's directive is copied to master with
`wake=no` (Q1). Master dispatches the work, so it must know a PR went back to its author. When the
owner is master, there is no copy.

## 4. The loop

Episode: the PR number, the head sha, and the sorted names of the failing checks.
For example: `pr:42 3f2a…c1 build,test-linux`. It is the dedupe key. The record keeps only its
digest (see Writes), and the script compares digests wherever it compares episodes.

On each pass, for each red PR:

0. **Already stopped:** nothing, whatever the episode: a new head, or other failing checks. The stop
   is terminal until the PR closes or the person runs `ci reset` (§5). The one exception is the
   stop's own unfinished parts, which are retried for its original episode (Writes, step 3).
1. **Same episode as last time:** if its action is complete, nothing; a re-run of the same head with
   the same failures sends nothing again. If its action is unfinished (a part not in `done`), only
   the missing parts are written, and nothing is decided again.
1b. **The base holds it** ("The base" below): the PR shares a failing check with a base that is red,
   or still running, or the base could not be read. Nothing this pass, and nothing is counted. This
   comes before steps 2 to 4, so a held PR is neither handed off nor stopped: a red base is not the
   author's failure, so it must not use the author's tries, nor the last of them (§8 Q10). A PR
   already stopped stays stopped (step 0), and an unfinished action of an earlier pass is finished
   (step 1): it was decided when the base was not red.
2. **Hand-offs already at the cap, `ci.max_handoffs` (default 3, Q2):** this is **the stop**. The
   person gets one notice for the PR (§5), master gets a directive with `wake=no`, and the owner gets
   nothing more. A held owner does not hold the stop, because the stop writes nothing to the owner
   (foureyes, r2). Otherwise, if master were the owner and `waiting` on an ask, the person's notice
   could wait without end.
3. **A hand-off, owner `blocked` or `waiting`:** nothing this pass. The next pass after the state
   ends sends it. This is the state the agent **declared**, in `{p}:agents`: `waiting` for a
   person's decision, `blocked` for a 4-eyes gate. It is not busmon's screen reading. Whether the
   owner's pane may be woken stays the dispatcher's usual rule, and the entry waits in the stream
   until then.
4. **Otherwise:** one directive to the owner, which wakes it, and the hand-off count goes up by one.

The count is kept per PR, across heads. A fix is pushed as a new head; if that head fails again, it
counts toward the same cap. A green pass does not reset the count (Q3).

The directive reads:

> CI failed on PR #42 (branch `fix/x`, head `3f2a…c1`), hand-off 1 of 3: `build`
> (https://…), `test-linux` (https://…). Read the log from the link or with
> `gh run view --log-failed`. Fix it on the branch, have the fix reviewed as usual, and report to
> master. The loop stops after hand-off 3 and gives the PR to the person.

The words "or with `gh run view --log-failed`" are written only when at least one listed entry is an
Actions check run (§2, A); otherwise the directive reads "Read the log from the link."

A pass whose PR is no longer open (merged, closed) deletes that PR's record, so a reopened PR, a
rare case, starts at zero.

### The base, checked before a hand-off ("stop the line", B)

If the base breaks, or a flaky test hits everyone, every PR goes red on the same check. Each author
would get a hand-off for a failure that is not theirs, and the caps would be used up. On
`pull_request`, GitHub Actions tests the merge commit with the current base, so a PR can go red
because of its base without being rebased. A red base is not the author's failure (§8 Q10).

**The read.** Each pass, after `gh pr list`, takes the distinct base branches (`baseRefName`) of the
red PRs (those §4 would otherwise decide, at step 1b) and reads the rollup of each base's latest
commit: **one call per base**, usually one, through `gh api graphql`. The exact query is fixed at
build time. No red PR, no call. The base's rollup is parsed and classified with §2's rules,
duplicate names included:

- **still running:** at least one entry not finished (§2), whatever else failed;
- **red:** finished, and at least one entry failed;
- **not red:** finished with no failure, or an **empty rollup** (a base with no CI on push).

**Per PR, by check name.** A red PR's failing set is compared, name by name, with the base's entries:

- **Base red** and the PR's failing set includes at least one check that also fails on the base:
  the PR is **held** (step 1b): not handed off this pass, and nothing is counted.
- **Base still running** and the PR's failing set includes at least one check that is still running
  or failed on the base: the PR **waits**, as a PR with running checks waits today (§2).
- **Otherwise** (base not red, or the PR fails only on checks the base passes or does not have): the
  loop runs as written. A PR red on a check the base passes, beside a base red on another check, is
  handed off normally.
- **Base read fails** (timeout, `gh` error, a cut answer): every red PR on that base sends nothing
  this pass, the same as a failed PR read. It is never taken as "base green".

**Master, once per base episode.** When a base is red and holds at least one PR, master gets one
directive, **woken** (master owns the base, as build sheriff). It names the branch, the base head
sha, the base's failing checks with their URLs (cleaned, at most 10 plus "and N more", as §2), and
the number of PRs held. It is written with `bus.FencedCmdOnce`, mark `ci:base:<branch>`, episode
the base digest, so the entry's `episode` reads `ci:base:<branch>=<digest>`. The digest is built
like the PR digest: the hex sha1 of the branch, the base head sha and the sorted failing check
names, joined by NUL. There is no new record: the mark is `FencedCmdOnce`'s own, in `{p}:quota`, one
field per base branch. The same base episode on the next pass writes nothing more; a new base head,
or another failing set, is a new episode and sends again. The count of held PRs is the one of the
pass that wrote the directive; it is not updated. A swap of master returns `ErrSwapInProgress`, and
the next pass sends it, as the other once-writers do.

**When the base goes green**, the PRs' next runs decide again: a PR still red after the base is
green is a normal hand-off, and its count is unchanged by the passes it was held. A stop (cap
reached) is not affected: a stopped PR stays stopped.

The extra call is measured with the `gh pr list` timing condition in §7: it is the same budget.

### Writes: one fenced script, with the progress of each part recorded

One action (a hand-off or a stop) is written by **one Lua script**, fenced by the lease token like
every dispatcher write. The script is `bus.FencedCIStep`, in `bus/`. It does not call the existing
once-writers one after another.

The action has parts:

- a hand-off: the owner's entry, and master's copy (`wake=no`) unless the owner is master;
- a stop: master's directive (`wake=no`), and the person's notice (`for=person`).

**The guarantee: at least once, and a duplicate is harmless.** The loop promises that each hand-off
(and each stop) reaches its recipients **at least once** while the PR stays red with the same
episode. It does not promise exactly once, and nothing in it tries to: there is no outbox and no
consumer-side dedupe. A duplicate does no harm. It is the same text, with the same episode in its
`episode` field, so the owner reads the same failure twice and fixes it once. Duplicates also
cannot use the cap: the count is decided once per episode (step 5) and does not move on a resend.

**The record** is `{p}:ci`, a hash with one field per PR (`pr:<n>`). Its value is
`digest|count|stopped|done`:

- `digest` is the episode's dedupe key: the hex sha1 of the PR number, the head sha and the sorted
  failing check names, joined by NUL. Check names are forge text and may contain `|` or `,`, so the
  record never holds them raw (foureyes, r4). The names travel only in the entries' text;
- `done` is the set of parts already written for that episode (`o` owner, `m` master, `n` notice);
- the script reads and writes it, never a Go struct round-trip;
- times are Redis `TIME`.

**Keys:**

- `{p}:dispatch`, the lease;
- `{p}:ci`;
- the `cmd` stream;
- the `notify` stream;
- the owner's swap key, `{p}:swap:<owner>`, and master's, `{p}:swap:<master>`.

**Arguments:**

- the token;
- the field;
- the episode;
- the cap;
- the owner, master, and whether the owner is master;
- the three texts, validated in Go first (text, names, types, as `onceCmdFields` does). Each entry
  carries `episode` = `ci:pr:<n>=<digest>`, the way `onceCmdFields` builds it. The `ci:` prefix is
  the mark's own, so a swap's resume match (`bus/swap.go`) and the quota marks can never take it
  for one of theirs (foureyes, r6).

**In order:**

1. If the lease is not the token, return `not-holder`.
2. **Preflight the types.** If `{p}:ci` is not a hash and not absent, or a stream key is not a
   stream and not absent, return `bad-type`. That covers a key a hand or another tool wrote with
   another type, the case where a later XADD or HSET would fail.
3. Read the field.
   - **If it is stopped:** if its digest equals the argument and its stop has a part not in `done`,
     go on to step 4 and finish that stop. Otherwise return `stopped-before`, **whatever the
     argument's digest**. A stopped field is never decided again; only the PR closing or `ci reset`
     deletes it (reviewer-codex, r6).
   - If its digest equals the argument and every part of its action is in `done`, return `same`.
4. If any recipient of a part not yet written is being swapped, return `swap`: the owner for the
   owner's part, master for master's part (on a hand-off and on a stop) (foureyes, r2). The script
   takes both swap keys, `{p}:swap:<owner>` and `{p}:swap:<master>`.
   The next pass tries again, as the once-writers do (`ErrSwapInProgress`).
5. **Decide, once per episode.** This is reached only for a field that is absent or not stopped. If
   the field's digest is not the argument, this is a new action:
   - a stop if the count is at or above the cap, else a hand-off;
   - HSET the field to `episode|count'|stopped'|` with nothing done, before any entry, where count'
     is count+1 for a hand-off and stays count for a stop.

   If the field's episode equals the argument, the action is the one already recorded and
   unfinished. Its count is not decided again.
6. **Each missing part:** XADD it, then at once HSET the field with that part added to `done`.
7. Return `sent` or `stopped`.

Steps 1 to 4 write nothing. The held cases (§4 step 1b, the base; §4 step 3, a hand-off to an owner
that is `blocked` or `waiting`) are decided in Go before the call, so they write nothing.

**Retry.** Redis runs a script with no other command in between, so an interruption of our process
(a crash, a lost lease, a timeout) falls before the script or after it. Redis does not undo a
script's earlier writes when a later command errors, so a script that errors at step 5 or 6 leaves
what it wrote: the decision, and the parts in `done`. The pass returns the error, and the
merge-queue record and health name it ("ci: write failed: …"). The next pass, 5 minutes later,
sees the same red episode and writes the parts not in `done`.

**Where a duplicate comes from, and its bound.** A part is written again only when Redis has taken
its XADD and then refused the HSET that marks it done. With the types checked at step 2, that leaves
a broker refusing writes (`maxmemory` with no eviction), which health already reports. It repeats at
most once per pass, every 5 minutes, and only while that refusal lasts and the episode is the same:

- a new head or a new set of failures is a new digest;
- a green or closed PR sends nothing more.

That is the whole bound, and it is enough because a duplicate is harmless.

If the episode changes while an action is unfinished, the new episode's action replaces it. Its
entries describe the failure as it now stands. The count already includes the old action's
hand-off.

**Known limitation: a held owner delays an unfinished hand-off's master copy** (reviewer-codex, r7;
master's decision: no new mechanism). Sequence:

1. A hand-off's owner part is written and marked done.
2. Redis refuses master's copy, or its done-mark.
3. Before the next pass, the owner becomes `blocked` or `waiting`.

The held check (§4 step 3) is decided in Go before the script, so the passes write nothing while the
owner stays held. Master's copy is delayed, and delivery depends on the episode:

- if the original episode is still red when the owner leaves the held state, the first pass after
  that writes it, as the retry rule says, only late;
- if the episode ends first (the PR goes green or closes, or gets a new head or a new failing set),
  the copy is superseded under the episode-change rule above and never sent. A new episode makes its
  own action, with its own copy to master (reviewer-codex, r8). The owner already has its hand-off, and if the
cap is reached, the stop does not wait for a held owner (§4 step 2), so the person sees the stop
either way. The build task tests this sequence and checks that the copy is written once the owner is
no longer held.

The person's `ci reset` is the only other writer: a script that deletes the field.

`{p}:ci` is classified in `bus/keykinds.go`, as `TestEveryWrittenKeyKindIsClassified` requires.
It is **runtime** (§8 Q9): a backup does not restore it, so after a restore the loop restarts at
zero for the PRs still red. A restored count could belong to heads that have moved since.

## 5. The stop, to the person

The notice is sent once per PR, at the stop:

> myproject: PR #42 still fails CI after 3 hand-offs (`build`, `test-linux`): the loop stopped; the
> PR is yours to decide (fix it yourself, close it, or `agentbus ci reset 42` to give the author 3
> more tries).

It appears in For you, as the other dispatcher notices for the person do.

After the stop, the loop sends nothing more for that PR, whatever its checks do. It resumes only
when:

- the PR closes and its record is deleted;
- the person runs `agentbus ci reset <pr>` (Q5). That command deletes the PR's field with a small
  script and needs no lease, the same way `agentbus run reset` works (`bus.ResetRun`): only the
  person may run it, and an agent caller is refused. busmon's `/` command palette gets a class C row,
  `ci reset <pr>`, as `self-repair reset <agent>` has (`cmd/busmon/palette_c.go`): it shows the exact
  argv and runs it after y, as the person, so the person needs no shell.

## 6. What stays as it is

- **The dispatcher never runs repository code.** `gh` reads the forge only, as the pass does today.
- **The dispatcher types only the wake line.** The failure text goes on the bus, never into a pane.
- **No conflict loop.** A conflicting PR is already in the merge queue for master, `Conflicting`,
  to be rebased (§8 Q6). Conflicts are not CI failures; the real fix is the file-based task split,
  in a separate spec.
- **No exactly-once machinery for the base.** The base check adds one read per base and one
  `FencedCmdOnce`; at-least-once stands as §4 states it.
- **No review-comment loop.** Our reviews already reach the author on the bus. Comments a person
  leaves on the forge are deferred, as the study says.
- **No new process, cadence or token.**

## 7. Settings, tests, step

Settings, in `config/keys.go`, read by `dispatch`:

- `ci.loop`, on or off, default on (Q2).
- `ci.max_handoffs`, an int, default 3 (Q2); 0 means never hand off, so every red PR goes straight
  to the stop.

Tests, with a fake `gh` and the disposable broker:

- the rollup parsing for each status and conclusion above;
- a pending check sends nothing, even beside a failed one; once all are complete, one hand-off lists every failure;
- same head, the failing set grows while checks finish: one hand-off, and the count moves once;
- duplicate check names: the latest entry wins;
- a self-addressed verdict is not an author;
- a held owner holds a hand-off but not a stop;
- the known limitation: owner part done, master's copy refused, owner then `waiting`: no write while held, and master's copy written once (count unchanged) on the first pass after the owner leaves held, if the episode is still red; if the episode changes while the owner is held, the old copy is not sent and the new episode's action is decided;
- master being swapped holds the stop;
- the same episode sends once;
- a new head sends again and counts;
- the cap stops the loop, and stops it once only. After the stop, a new head or a new failing set returns `stopped-before` and writes nothing; an unfinished stop finishes for its own digest only (reviewer-codex, r6);
- a `blocked` owner is held, then sent;
- an unknown owner and a two-author head go to master;
- a closed PR deletes its record;
- `ci reset` re-opens the loop;
- the fenced script refuses a stale token and writes nothing;
- a swap in progress writes nothing, and the next pass sends;
- a hand-off writes the owner's entry, master's copy and the record. A pass stopped before the script writes nothing, and the next pass writes all of it. A pass stopped after it writes nothing more (`same`);
- a wrong-typed `{p}:ci`, cmd or notify key gives `bad-type` and writes nothing;
- a check name containing `|` and `,`: the digest still matches, so `same` holds and the count does not move (foureyes, r4);
- a resend after a refused done-mark (injected): the same text and episode again, and the count does not move;
- a script failure injected after the owner's part is done: the retry writes master's copy only, the decision is not taken again, and the count does not move;
- an episode that changes while its action is unfinished: the new action is decided, and the old one's missing part is not sent;
- the owner rule: one author over two reviewers is that author; no entry, or two authors, is master;
- no log field and no output of the command ever reaches an entry;
- the log hint: an Actions check run's entry names `gh run view --log-failed`; a `StatusContext`
  (a Jenkins status) or a non-Actions check run gives the URL alone (A);
- the base (B), the person's seven:
  - base red on `test-linux`, two PRs red on `test-linux`: no hand-off, counts unchanged, one
    directive to master;
  - the same base episode on the next pass: nothing more to master;
  - base green again, a PR still red: a normal hand-off, count +1;
  - a PR red on a check the base passes, beside a base red on another check: a normal hand-off;
  - base still running: the PRs wait;
  - base read fails: nothing sent, no count;
  - a base with no rollup: a normal hand-off;
- and two more for step 1b's order: a PR at the cap whose failure the red base shares is held, not
  stopped; an already stopped PR stays stopped whatever the base.

One build step, one PR: the read, the base check, the record, the loop, the stop, `ci reset` and
its palette row, an operator-notes fragment. The fragment says: a check that never finishes
(`EXPECTED` or `PENDING` forever, a CI server down) or a base that stays running makes a PR silent,
and silence is never "all green" (C); the hand-off gives a URL, which the agent must be able to
reach (A); a base with no CI on push has an empty rollup, so it is "not red" and the loop runs as
written (B). One condition: measure the time `gh pr list` takes with the rollup, plus the base
call, on one of the person's projects with real checks and with many open PRs (`ghLimit` is 1000),
against the 10 s per-call budget before it ships. If it does not fit, the rollup becomes its own
`gh` call in the same pass. That is still bounded, and it is decided at build time, not guessed now.

Next steps, each its own PR after this one: the "CI red" mark on an approved PR's row in the merge
queue (§8 Q8, the next small step); the GitLab and Bitbucket PR readers, on the roadmap (§8 Q7).
Longer term, not this spec: master's merge queue tests the PR rebased on the current base before
pushing, so the base stays green.

## 8. The person's answers (decided 2026-11-19, r10)

1. **Who is told:** as proposed. The author is woken, and master gets a copy with `wake=no`.
2. **Cap and default:** as proposed. 3 hand-offs per PR, and the loop is on by default. A repository
   with no CI stays silent, so the default costs nothing.
3. **Green does not reset the count:** as proposed. `ci reset` is the deliberate way to give more
   tries.
4. **`CANCELLED` and `ACTION_REQUIRED` are not failures:** as proposed. A PR whose only check is
   `ACTION_REQUIRED` stays silent, which is acceptable.
5. **`agentbus ci reset <pr>`:** yes, and also in busmon's `/` command palette, so the person needs
   no shell (§5).
6. **Conflicts stay with master, through the merge queue:** yes, for this step. Conflicts are not CI
   failures. The real fix is the file-based task split already requested, in a separate spec.
7. **GitLab and Bitbucket:** no loop until they have a PR reader. The readers go on the roadmap as a
   separate step, not as a "maybe": the project must run on any forge. With no forge, there is no
   status to read and nothing to do.
8. **"CI red" mark in the merge queue:** yes, as the next small step after this one.
9. **`{p}:ci` is runtime and not restored:** as proposed.
10. **New, decided by the person: a red base is not the author's failure.** The base is checked
    before a hand-off (§4, "The base").

The rest stands as written: at least once with a harmless duplicate, a stop that is terminal, types
preflighted, no exactly-once machinery.
