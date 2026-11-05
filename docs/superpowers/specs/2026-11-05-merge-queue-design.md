# Merge queue: what waits for the person's merge, derived, never remembered

Status: spec, for review (subject `merge-queue-spec`), with master's decisions in §8. Not built. The task comes from the person,
2026-11-05, through master: busmon's For you stopped showing the PRs waiting for the person's merge,
because nothing derived them. They showed only when master remembered to open an ask or link a PR to
a plan task.

## 1. The regression, and its root cause

The person merges every PR on this project. Today busmon learns that a PR waits for them in one of
two ways, and both depend on master remembering:

- **An ask.** It shows in For you, but master writes it by hand, and it is a question, not a list.
- **A plan link.** `plan.Derive` makes an approved merge task wait for the person only when the PR
  is linked (`plan link <task> <pr>`, or `deliver --task`, which links it). A delivery without
  `--task`, or one not tied to a plan task at all, waits invisibly.

`deliver` writes no structured record of what it delivered. The verdict check binds the delivery to
an approved head, and the forge then holds the PR. The only trace on the bus is master's report
sentence, `delivered <branch>@<head>: <url>`, which nothing parses. The facts that say "approved,
delivered, not merged" all exist: the ledger, the forge and git. What is missing is something that
joins them.

### Why the proposal's `pr:<n>` subjects do not reach today's PRs

The proposal derived the queue from ledger subjects named `pr:<n>`. This team reviews by **task
subject** (`request-priority`, `message-reply-key`, ...) with `verdict --head`, and `deliver` checks
`--subject`, else the plan task's review subject, else the branch name. A `pr:<n>` subject appears
only when someone reviews the PR itself. Keyed on `pr:<n>`, the queue would have been empty on
2026-11-05 too. The join below is by **head commit**, which every delivered PR has whatever its
subject is called.

## 2. The derived fact

**A merge-queue item is an open change request (PR, or MR on GitLab) whose head commit H is the
head of an independent approve that decides its subject.**

That means:
- `bus.RollUp` over the subject's verdicts is `APPROVED`;
- the deciding verdict's `Head` is H;
- the approve was given on the head being merged now.

### What does not count

- **A newer push.** The PR's head moved past the approved one, so the PR is not in the queue. It is
  the team's again: a new head needs a new verdict, merge-only heads included.
- **Approved but not delivered.** No open PR carries H. That is master's work (`deliver`), not the
  person's. It is not For you; reporting it is a later step (§8, decision 2).
- **A rejection after the approve.** The roll-up is `REJECTED`, so the item is out, even with the
  PR open.
- **Merged or closed.** The PR's state is not open, or (git fallback) H is an ancestor of the base
  branch's tip. The item leaves.

### Several subjects, several heads

The ledger is read whole, as `plan.ReadWithVerdicts` already does incrementally. For each subject
with an approved deciding head, that head is the subject's candidate. A PR's head H matches every
subject whose candidate is H. Usually there is one. Two subjects approving the same commit merge
into one item that lists both. An approve with no `Head` matches nothing: `deliver` already refuses
it.

## 3. Where the facts come from (§3a forge, §3b git fallback)

### 3a. The forge, when its CLI is there (the authoritative source)

- **GitHub:**
  `gh pr list --state open --json number,title,url,headRefName,headRefOid,baseRefName,isDraft --limit 1000`,
  in the project's working directory.
  - One call answers number, title, URL, branch, head SHA and base; `gh` pages through the API up to
    the limit itself.
  - **The list must be whole** (reviewer-codex): an approved PR outside a cut list would leave the
    queue silently. A result of exactly the limit (1000) is read as possibly cut: the pass is
    **incomplete**, which is unknown (§3d), and keeps the previous items. A failed page fails the
    call, which is unknown too. Never a partial list read as the whole.
  - A merged or closed PR is simply not listed.
  - A draft is listed but never queued: not ready.
- **GitLab and Bitbucket, this round: the git fallback (§3b)** (master's decision on review,
  2026-11-05). No forge reader is built for them yet: a GitLab or Bitbucket project reads its queue
  from the remote branches, as GitHub without `gh` does, and its source is `git`. What the fallback
  cannot see (a draft, a title, the MR's number and URL) is simply not shown; an approved head on a
  remote branch is still an item, so nothing waiting is lost.
- **Later step: `glab mr list`** with its JSON output, the same fields under GitLab's names, and the
  same rule for a cut list. The exact flag is to be confirmed against glab's version when it is
  built: glab is not installed on this host, and the spec does not guess it. Bitbucket has no
  official CLI to rely on; it stays on the fallback until one is chosen.
- **The rules `deliver` already follows:** the same forge detection (`forge` package, `delivery.forge`
  setting), the same "CLI on PATH" check (`cliReady`), the same environment hardening as the github
  health check:
  - no askpass (`probeEnviron`);
  - `GH_PROMPT_DISABLED=1`;
  - `LC_ALL=C`;
  - a 10 s bound.
- **Credentials:** agentbus never reads a token. `gh` uses its own stored credentials, as it does for
  `deliver`.

### 3b. git, when there is no forge CLI or the forge is `local`

1. **The remote tips:** `git ls-remote --heads origin`, one call, no fetch.
2. **An open item:** a remote branch whose tip is exactly H, H being an approved deciding head.
3. **The base:** `ls-remote` gives the base branch's tip B (`delivery.base`, else `main`).
   `git fetch --no-write-fetch-head --no-tags --refmap= origin <B>` brings B's objects only. The
   empty `--refmap=` is what stops git's opportunistic update of `refs/remotes/origin/<B>`. Without
   it the fetch moves the remote-tracking branch and `origin/HEAD` (foureyes, tested on git 2.56).
   With it every ref stays byte-equal and the objects arrive. So the person's checkout is
   untouched. The forge-delivery decision lets only master and docs push; this is a read.
4. **Merged:** `git merge-base --is-ancestor H B` is true, and the item leaves.

**Local forge:** `deliver` pushes nothing, so a local delivery is approved head H on a local branch.
The rule has the remote fallback's shape (foureyes):
- **An item** is a **local branch whose tip is exactly H** (`git for-each-ref refs/heads`), H not an
  ancestor of the local base branch and the branch not the base itself.
- **It ends** when the branch is deleted or moves (a new commit needs a new verdict).
- **What is not an item:** an approved subject with no branch at its head, abandoned or never
  delivered. That is master's work (§2), never the person's list.
- **How the person merges:** with the command `deliver` printed
  (`git switch <base> && git merge --no-ff <H>`). The plan's `LinkRef` already records that ref.

**What the fallback cannot see:**
- **A squash merge**, or a rebase merge, leaves H outside the base forever. The branch's deletion
  ends the item (no remote tip at H). A branch kept after a squash is dropped once its diff against
  the base is empty, said as "contents in base" (§8, decision 3).
- **Titles and URLs:** git has none. The title is the plan task's title, when a plan link names H's
  branch, else the first commit's subject in `B..H`, as `deliver` builds it (`deliverTitle`). The URL
  is the forge's compare page (`linkPage`), or none.

### 3c. Which runs where, and how often

- **The computation:** a dispatch-service chore, `cmd/agentbus/mergequeue.go`, every 5 minutes and
  only while `HoldsDispatch`, as notify and history do.
  - Its working directory is the services' (the repository).
  - The forge or git calls run there, under one 20 s budget per pass.
  - The pure join, `mergequeue.Derive(verdicts, prs, tips, ancestry, planFacts)` in a new package
    `mergequeue/`, does no I/O.
- **The record:** the chore replaces `{p}:mergequeue`.
  - What it is: a fenced JSON string.
  - Lifetime: `PEXPIRE` 15 minutes.
  - Class: runtime (`bus/keykinds.go`), not in `projectKinds`, ignored by `HasKeysBesidesDispatch`.
  - Contents (reviewer-codex: an attempt is not an observation):
    - `attempted_at`, Redis `TIME` at the last pass, failed or not;
    - `items_at`, Redis `TIME` at the last **successful** pass, the one that read the items;
    - `source` (`gh` or `git`; `glab` with its later step);
    - `ok`, or the fixed phrase of what could not be read;
    - the items.

    A failed pass moves `attempted_at` and the reason only; `items_at` and the items stay as the
    last success left them.
- **This is a cache of a derivation, not a memory.** Nobody else writes it, nothing merges into it,
  and every pass recomputes it whole. A deleted key changes nothing: the next pass writes it back.
  busmon reads it on its `requestPoll` tick. It never runs git or `gh` itself.

### 3d. Unknown is never "nothing waits"

The pass is unknown, with its reason, when any of these holds:
- no Git repository;
- no `origin`;
- the forge CLI is present but fails (not logged in, network, timeout, non-zero exit);
- `ls-remote` or the fetch fails;
- the ledger cannot be read.

An unknown pass **keeps the previous items and their `items_at`**, and sets the reason. busmon shows
them "as of" `items_at`, never as an empty queue. Successive failures never make that time look
fresh: only a success moves it. A missing or expired key is unknown too. Only a successful pass can
say "nothing waits".

## 4. busmon

### For you

- **One row per item:**
  - text `merge PR 309: <title>`, or `merge <branch>` without a number;
  - the sender column `you`;
  - the age since the deciding approve.
- **Rank:** its own, `rankMerge`, right after `rankPlan`, so it sits with the plan's waits (§8,
  decision 1).
- **Counts:** in the title and in the badge (`decisions`), and in the reminder.
- **No double row:** a plan task waiting for the person (`merge PR N`) whose PR or head is an item
  shows once, as the merge item, its plan task named on the page.
- **When the record is not current:** while the pass is unknown, the rows keep their text and add
  `(as of HH:MM: <reason>)` in muted. With no record ever read, one muted row says the queue cannot
  be read, and why.

### The item's page (Enter)

The rows are `fields.go`'s:
- **PR:** number, title, URL.
- **Branch:** its name, and its head commit shortened.
- **Approved by:** each reviewer, with its time.
- **What the reviewers checked:** each approve's message, verbatim (`DetailText`).
  - This is where the suites and heads master and the reviewers ran are written. No structured
    check record exists, and the spec invents none.
- **Subject:** with the plan task when one names it.
- **Source:** `gh` or `git`, and when it was read.

Keys:
- `y` copies the PR URL, or the merge command in local mode.
- `Y` copies the reviewers' messages.
- Both through the page's `plainWhat`/`threadWhat`, `OnPage` as today.

The item leaves busmon at the first record without it, after a merge, a close, a newer push or a
reject.

### The header

Nothing new: the For you badge already counts decisions.

## 5. CLI, health, notifications

### `agentbus merge-queue [--json] [--live]`

- **What it does:** read-only.
  - It prints the record: one line per item, `#309  request-priority  f98091b  approved by foureyes,
    reviewer-codex  <url>`, then the source and age.
  - Exit codes: 0 with items, 64 with none, 2 when unknown, with the reason.
- **`--live`:** computes now in the caller's checkout with the same code. It writes nothing, not
  even the record.
- **Its place in `main()`:** under the project-required exit, read-only for the identity guard.

### Health `merge-queue`

- **Place:** Person, with `Command: agentbus merge-queue`.
- **Interval:** 5 min, from the record.
- **ok:** no item.
- **warn:** one or more items, for you. The detail lists `#N <title>`, cut to the detail's limit.
- **unknown:** the reason.
- **Never fail,** so it never wakes sentinel.

### Notifications

- **The event:** a new event, "PR waiting to merge".
  - Its episode is `merge:<pr or branch>|<head>`, so a new approved head after a change is a new
    event and the same head is told once.
  - It goes in `notifyfacts.go`'s list, `quiet_exempt`-able like the others, after the baseline
    rule.
  - Its summary line carries no agent text: `PR 309 waiting to merge`. The title only in `full` to a
    private target.
- **Its class:** `decision`, like a plan task waiting for the person. `notify.events` (default
  `decision,stall,fail,blocked,prompt`) needs no change, and a person who turned `decision` off gets
  none.

## 6. Who writes what, and the skills and rules

- **What is written:** the chore's fenced record, nothing else. The person and master write nothing
  new.
- **What the PR link becomes:** `plan link` and `deliver --task` stay, as plan facts (a PR on a plan
  task), but the queue no longer depends on them.
- **The `agent-bus-master` skill:**
  - A PR waiting to merge is **derived**: master does not open an ask to list PRs or to say "please
    merge".
  - An ask stays for **decisions** (merge now or wait, a conflict to choose, a release).
  - After `deliver`, master checks `agentbus merge-queue` names the PR. If it does not, the queue
    says why (unknown, a newer push, a draft), and master fixes that, not the list.
- **Tool rules:** none new. The command is read-only, and the chore runs in the service.
  `gh pr list` is not an agent's tool here: the service runs it, not a role.

## 7. Migration and compatibility

- **No data to migrate.** The first pass derives the queue from what already exists. On this host,
  that is every open PR whose head the ledger approved. The notifications baseline rule means a
  target is not flooded with the backlog at the first pass.
- **An older busmon** shows no merge items; the asks and plan links it reads still work.
- **The verdict ledger cap** (10 000 entries) bounds how far back an approve is found. An approve
  trimmed out of the ledger cannot match, and its PR shows as not approved. The record says so as a
  count, `N open PRs not on an approved head`, on the page and in the health detail. It is never
  hidden. The count covers (foureyes) every open, non-draft PR whose head is not the deciding
  approved head of any subject:
  - a newer push;
  - no review yet;
  - an approve trimmed out of the ledger;
  - a late approve on an older head, which becomes the decider, so the delivered PR leaves the
    queue. `deliver` would refuse that head too, so this is consistent, but never silent.

## 8. Decisions (master, 2026-11-05, the person being away)

The six questions this spec left open, as master decided them:

1. **Rank:** a rank of its own, `rankMerge`, right after `rankPlan`.
2. **Approved, not delivered:** a later step, not in this change.
3. **Squash merges under the git fallback:** an item whose branch has no difference from the base
   (`git diff --quiet B H` after the objects-only fetch) is dropped. The page and the CLI say
   "contents in base" for it, not "merged": the same empty diff also clears a branch whose changes
   were reverted (foureyes).
4. **Several forges:** `origin` only, as `deliver` does.
5. **Drafts:** never queued.
6. **The plan:** `plan.Derive` reads the queue, so an approved merge task whose head is an item waits
   for the person with no PR link at all. No automatic done: "done needs a recorded `plan done`"
   stays the plan spec's rule.

## 9. Test plan

Test-first, one step each:

1. **`mergequeue.Derive`, pure:**
   - an open PR on the approved deciding head is queued;
   - a newer push is not;
   - a reject after the approve is not;
   - a draft is not;
   - two subjects on one head make one item;
   - an approve with no head matches nothing;
   - a merged item (ancestry true) leaves;
   - `local`: a local branch whose tip is the approved head, not in the base, is an item; an
     approved head with no branch at it is not; a deleted or moved branch ends it;
   - an untrimmed count when approves are missing.
2. **The forge reader** with a fake `gh` on PATH, as the installer tests do (`glab` with its later step):
   - the JSON parsed, titles through `SafeText`;
   - the hardened environment asserted (no askpass, `GH_PROMPT_DISABLED`);
   - every failure is unknown with its fixed phrase;
   - a list of exactly the limit is incomplete. The test: 1001 open PRs, the only approved one
     outside the first 1000. That pass is unknown and keeps the previous items, never an empty or
     partial queue;
   - a failing page is unknown too;
   - the 10 s bound.
3. **The git fallback** on a temporary repository with a bare `origin`:
   - the remote tips;
   - the objects-only fetch with `--refmap=` writes no ref (`git for-each-ref` before and after,
     byte equal, `refs/remotes/origin/*` and `origin/HEAD` included, after a new commit pushed to
     the bare origin) and still brings the objects (`git cat-file -e`);
   - ancestry;
   - the squash case (§8, decision 3): a kept branch whose diff against the base is empty is
     dropped, said "contents in base".
4. **The chore:**
   - fenced (a lost lease writes nothing);
   - an unknown pass keeps the previous items and their `items_at`;
   - two successive failures move `attempted_at` only: the items' age stays the last success's;
   - the `PEXPIRE`;
   - not in `projectKinds`;
   - classified runtime (`TestEveryWrittenKeyKindIsClassified`).
5. **busmon:**
   - the For you rows (rank, count, badge, the dedupe with a plan task, the `as of` mark, no record);
   - the page and its `y`/`Y`;
   - goldens.
6. **CLI:** output, `--json`, exit codes, `--live` writes nothing, the table tests.
7. **The plan** (§8, decision 6): an approved merge task whose head is an item waits for the person
   with no PR link; nothing records it done.
8. **Health and notifications:** the check's places, and the `merge` event with its episode and
   baseline.
9. **The full suite and the Redis suite** before each challenge.
