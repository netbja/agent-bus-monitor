# The plan's lifecycle, and the Plan tab redone (plan-lifecycle)

- Status: spec r4, for review (subject `plan-lifecycle-spec`). Not built. r3 merged as #847.
  - r4, the person's answers to ask 1795818074022-0 (§C), folded in: the release recap wakes the
    drafter (§A4); `plan load` takes no path (§A4); master's sorting is recorded, undone from
    busmon, and never overrides the person's, and off-bus PRs come from the forge reader, not
    git history (§A3); `plan draft --from` takes a committed file (§A4); a headless approval counts only on a merged
    request, and a new approval must name its head (§A2); the mini timeline is a list (§B7, §B8).
  - r2, foureyes r1:
    - a task's work is one request, its slices `--more` steps (§A1, §A2);
    - `+` stays priority, and "next plan" is `n` (§A3, §B9);
    - the prerequisites of each step (Build steps), with step 1 sent only after both of its
      prerequisites have merged (reviewer-codex r2);
    - first-pass unplanned and the 11/11 example (§0, §A3).
  - r2, reviewer-codex r1:
    - the Next line is its own function, and `plan.NowNext` is unchanged (§B6);
    - the mock-ups' counts are 9/11, with the shared scale;
    - a manual done is ignored once work is linked (§A2).
  - r3, foureyes r2b: "merged" gets a source today's flow produces (`Request.Merged`, §A2), and
    a manual done is kept unless newer linked work shows the task unfinished (§A2).
- Date: 2026-11-27. Author: architect. Base: origin/dev `a919454e`.
- Source: the person's typed directive to master of 2026-11-27, 23:12, relayed as task
  `plan-lifecycle-spec`, after master's report 1795817303570-0 ("Revoir le plan"). The person's
  words: "aimed at the cause".
- One spec, two parts, one PR. Part A is the lifecycle. Part B is the Plan tab, which draws what
  Part A derives. The person's open questions are in §C, asked in one ask in French.
- It builds on onboarding (`2026-11-27-project-onboarding-design.md`, merged with its amendment):
  the draft, `TypedApproval`, `check`, the evidence bound to a head, and `plan.InHand`. It also
  builds on status-summary (`2026-11-27-status-summary-design.md`): request provenance
  `--plan <task>`, and `plan.NowNext`.

## 0. The cause

`agentbus plan show` reads "v0.34 8/11" while every task of v0.34 is finished. Three things make a
plan drift from what happened:

1. **Done is recorded by hand.** A merge task reaches done only when master runs `plan done`
   (`plan/derive.go:117`, `fact.Done`). The eight done rows all read "merged (recorded 6d ago)".
   The three still "upcoming" have specs merged (`2026-11-21-wip-limit-design.md`,
   `2026-11-14-quiet-update-design.md`) and builds merged since (`wip-limit step 3`, #803;
   `single-approval step 3b`, #806; `quiet-update step 4b`, #817). Nobody recorded them.
   v0.34 will not fix itself when Part A lands. Its requests carry no `--plan` provenance, so
   its three tasks reach done only once the person (or master) attaches their merged work
   (§A3). From the next plan on, requests carry the link.
2. **A task finds its work by name.** A task's board entry is its `board` field, or nothing
   (`derive.go:125`, `in.Board[t.Board]`). A request sent under another task name, as
   `wip-limit-step1` for `wip-limit-spec`, is invisible to it.
3. **Nothing renews the plan, and the alarm is blind.** The plan is a file master loads by hand
   (`plan load`). The `plan-drift` check reads merge commits of `origin/main` only
   (`internal/dispatchsvc/plandrift.go:118`, `:243`). It reads only GitHub's "Merge pull request"
   subject (`health/plandrift.go:33`). This project squash-merges into `dev`, so the check sees
   nothing. The "stale" label is a 7-day timer (`plan.StaleAfter`), not a fact.

Part A removes the three causes. A warning stays only as a safety net (§A5).

# Part A. The lifecycle

## A1. Every task finds its own work

A task's work is found in this order. The first rule that finds one wins:

1. **Provenance.** A request sent with `--plan <task>` (status-summary §4.3) records
   `provenance=plan:<task>` on its board entry. Every request for a plan task is sent that way,
   so the link is written once, by the act that starts the work. **A task's work is one
   request.** A task built in slices uses that request's `--more` steps (`MergedSteps`,
   `OpenSteps`, `bus/requests.go:44-48`): the request stays open until its last step, and
   `ClosedOnMerge` is written only then. So:
   - a second `request send --plan <task>` is refused while another request for that task is
     open: "<task> has request <id> open: add a step to it (request step), or close it first";
   - a `--plan` request sent after the task's request closed is a **rework**. It reopens the
     task, which is not done again until that request closes on merge.
2. **An attachment** made by hand (§A3, `plan attach`): a request or a PR that the person or
   master tied to the task.
3. **The `board` field** (today's rule), then a board entry named exactly as the task id. These
   keep old plans and old requests working.

The task's PR comes from its requests:
- while open, from the merge queue's item for the request's branch (`MergeItem.Number`, read by
  branch);
- once merged, from the merge record (§A2), which gains the field `pr` (additive), written by the
  dispatcher when it closes the request on merge.

`plan link` (master) stays as the manual exception, and busmon shows such a link as "linked by
hand". `plan.Links(task, in)` is the one function that applies these rules. Derive, busmon, `plan
show` and `summary` call it.

## A2. Done is derived, never recorded

The plan file holds **intent only**: titles, order (the file's order), `after`, `owner`, `group`,
`priority`, `check` (the acceptance, onboarding §7), `spec` and `command` (§A6). A task's state
comes from broker events, by these rules (`plan/derive.go`):

- **A merge task** is done when all of these hold:
  - **nothing open:** no request linked to the task is open (§A1: one at a time; a rework
    reopens it);
  - **merged:** its newest request's head is delivered into the base. There are two records of
    that, and either counts:
    - `Request.ClosedOnMerge`, written today by the merge-queue pass for a request still in state
      `review` (`internal/dispatchsvc/closeonmerge.go:37`, `:81`);
    - **`Request.Merged {head, base, pr, at}`**, new and additive, for a request already `done`.
      This team closes its requests with `request done` before the merge, so by then nothing is
      left in `review`. On the live board, 376 entries are done, none carries `closed_on_merge`,
      and 146 have a recorded `commit` (foureyes, r2b). The same pass, in the same fenced script
      family (`requestMergedScript`), writes `Merged` on a `done` request whose recorded
      `Commit` (`request done --commit`) is delivered into the base by
      `mergequeue.Delivery.Delivered` (ancestor, squash patch-id or rebase patch-ids). It takes
      `pr` from the merge-queue item seen for the request's branch, when there was one. A `done`
      request with no recorded commit gets no `Merged`. Such a task relies on its manual done
      (below), or on an attachment.

    A PR attached by hand that the merge queue saw merged counts too.
  - **Backfill, once:** when Part A lands, the pass runs over the `done` requests of the active
    plan's life, newest first, bounded to 500. It writes `Merged` where the commit is delivered.
    After that it judges each request when it becomes `done`, and again on each base move until
    its commit is delivered or 30 days have passed;
  - **reviewed:** its review rolls up `APPROVED` (`bus.RollUp`). The deciding approval names the
    merged head (`Verdict.Head` equals the merged record's `Head`). An approval at another head is "approved,
    head changed" (onboarding §9.2). An older verdict with no head (Q5):
    - counts **only when the request is already merged** (`ClosedOnMerge` or `Merged`), labelled
      "approval names no head" in the detail pane and the task's history;
    - does not count on a request not merged. The task waits for a review that names its head;
    - is never rewritten: the label is computed at read time, and no record is migrated.

    **A new approval must name its head.** `agentbus verdict … approve` with no `--head` is
    refused: "an approval names the head it read: --head $(git rev-parse HEAD)". A reject may
    still omit it. The skills' verdict lines already pass `--head`; the build checks them.

  For a task with a `check`, onboarding §9's evidence rule applies on top.
- **A `merge: false` task:** delivered and approved, as today. With a check, onboarding §9.
- **A person task:** as today, and onboarding §9.4 for one with a check.
- **`plan done`** becomes an exception for a task with no linked work (a step done outside the
  bus). busmon shows it as "recorded by master". It is refused on a task that has linked work:
  "<task> has linked work: its state comes from it". This uses the same guards as onboarding §9.3.
- **A manual done is kept, unless newer linked work shows the task unfinished.** Today derive
  returns `Done` for `fact.Done` before it reads anything else (`derive.go:117`). The new rule:
  - `fact.Done` stands, as today, with its label "recorded by master";
  - it is **overridden** only when the task's linked work shows the task is not finished: a
    linked request still **open**, or a request sent, or an attachment made, **after** the fact
    (`fact.Done` time against the request's `CreatedAt` and the attachment's time). That is a
    rework, and the task's state then comes from that work;
  - the detail pane says "recorded done overridden: newer work <id> is open".

  So the past stays as it is. v0.34's seven tasks recorded "merged (recorded 6d ago)" stay done,
  and gain `Merged` from the backfill where their commit is recorded. An unapproved PR attached
  after a `plan done` is newer than the fact, so the task is "in review", never done.

  Tests:
  - **today's shape:** a request `done` with no `closed_on_merge`, a delivered `commit`, and a
    `fact.Done` stays done, and the backfill writes `Merged`;
  - the same task with a newer open rework request is not done;
  - `Merged` is written for a `done` request whose commit is delivered by squash, and not for one
    whose commit is not delivered;
  - `plan done`, then `plan attach` of an open, unapproved PR, gives "in review". Once that PR is
    merged and approved at its head, the task is done;
  - a fixture of v0.34's board and facts keeps 8/11 before any attachment.

So "merged" is a fact the dispatcher writes, for requests in review as today and for requests
already done (`Merged`). No one records it by hand any more.

## A3. Unplanned work

**What counts as unplanned:** work that merged into the base while the plan was active, and that
belongs to no task:
- **requests closed on merge** whose provenance is not `plan:<a task of the plan>` and that are
  not attached;
- **off-bus PRs** (the person's answer to the follow-up, ask 1795852308652-0): PRs merged into
  the delivery base (`[delivery] base`, `dev` here) since the plan's load that no request
  references, tagged "off-bus" (a hotfix, the person's own). No git history is parsed. They come
  from the **forge reader the merge-queue pass already runs** for ci-loop
  (`internal/dispatchsvc/mergequeuechore.go:361`, `mergequeue/forge.go`):
  - `mergequeue.ReadGitHubMerged(ctx, dir, env, base, since)` is new, beside `ReadGitHub`, and
    run the same way: the same hardened env, `gh` from env's PATH, no token read, the call
    bounded by `mergeQueueCall`, free text through `cleanText`. It runs `gh pr list --state
    merged --base <base> --search "merged:>=<since>" --json
    number,title,url,headRefName,headRefOid,mergeCommit,mergedAt --limit 1000`. A list of exactly
    the limit is `ErrIncomplete`;
  - it runs in the same pass, after `ReadGitHub`, only when the forge is GitHub with `gh`
    (`case f.Kind == forge.GitHub && c.hasGH(env)`) and a plan is loaded. GitLab and Bitbucket
    get their reader with ci-loop's roadmap step (ci-loop §8 Q7), behind the same function
    shape;
  - **referenced by a request** means any of: the PR number in a request's `Merged.pr` or a
    merge-queue item seen for its branch; the PR's `headRefName` equal to a request's branch; its
    `headRefOid` or merge commit equal to a request's recorded `Commit`. A PR referenced is never
    off-bus;
  - **a failed or cut read** keeps the previous off-bus items, never a partial list read as
    whole, as the merge queue does;
  - **no forge reader** (a local forge, GitLab or Bitbucket today, or `gh` missing): the board
    only, and the section's header carries one line: "off-bus PRs not read: no forge reader for
    <forge>";
- excluding PRs master marked `plan ignore` (notes, a release, a hotfix).

**Where it is kept:** the dispatcher's merge-queue pass writes `{p}:plan:unplanned`, a hash keyed
`pr:<n>` or `req:<task>`, with title, head, merged time and source. It is a fenced write, and the
kind `plan:unplanned` is in `aclDispatcherOnly`. A new load drops it, as it drops
`plan:ignore`.

**The Plan tab's UNPLANNED section** lists them, newest first (§B, the mock-ups). Each item takes
three actions:
- **`a` attach** to a task of the plan, chosen from a picker. It runs `agentbus plan attach
  <task> <item>`, which writes the fact `attached` on the task. A merge task attached to a merged,
  approved PR is then done by §A2.
- **`n` add to the next plan.** It runs `agentbus plan next add <item>`, which appends to
  `{p}:plan:next`, a list (record) that the release draft reads (§A4). The current plan's file
  does not change: a file changes only through a draft and the person's yes.
- **`x` not plan work.** It runs `plan ignore <pr>`, as today.

**Who sorts (Q4):** the person in busmon, and master by the CLI.
- Every sort is a record: `who`, `at` (Redis `TIME`) and `reason` (required from master's CLI,
  optional in busmon), kept on the item in `{p}:plan:unplanned` once sorted, so the detail pane
  shows "attached by master 2h ago: <reason>".
- **Undo from busmon:** `z` (busmon's undo key, as in Activity, `keys.go:269`) on a sorted item (`d` shows sorted items, as it shows done rows)
  reverses it: `plan attach --undo`, `plan next drop`, `plan ignore --undo`. The undo is recorded
  the same way.
- **Master never overrides the person:** a sort by master on an item the person sorted, or
  undid, is refused: "the person sorted <item>: only busmon changes it". The person may change
  master's.
- **`n` feeds only the next draft**, never the active plan; its adoption stays the typed yes (§A4).

**The first pass lists a backlog.** Every request merged during the active plan with no
provenance (all of today's), and matching no task by `board` or id, is unplanned. That is the
intent: it is how `wip-limit-step1` and the work behind v0.34's three open tasks surface, so the
person attaches it. It is not a bug.

## A4. At each release, a draft of the next plan

- **When:** a minor release tag `vX.Y.0` closes plan `vX.Y` (busmon already maps them,
  `releases.go:109`, `planOfTag`). The release checklist gains one step for docs: it publishes
  the **release recap** (the tasks shipped, and the work done off the plan) as a report to the
  architect, the drafter (Q1), which wakes it to "draft v(X.Y+1)". Docs never edits the plan.
- **What the draft holds:**
  - the tasks of the current plan not done, carried with their ids (onboarding §8.6), and the
    recap's off-plan work in the question's context;
  - the first items of `NEXT-DIRECTIONS.md`, when the file exists;
  - the `{p}:plan:next` items, each turned into a task;
  - the unplanned items not yet sorted, listed in the question's context, not as tasks. The
    person sorts them with `a`, `+` or `x`.
- **How:** the same flow as `agentbus init`. The drafter commits the file, runs `plan draft`
  (onboarding §8.1), and the person's typed `yes` adopts it (`TypedApproval`, onboarding §8.4).
  On that yes master runs the load, and only that load works (onboarding §8.5). Nothing else is
  new.
- **`plan load` becomes internal** (Q2). It takes no path: `plan load` (or `plan load <qid>`,
  the draft's id) loads only the recorded draft the person approved. The
  onboarding rule "a plan with `checks: required` is loaded only as a draft" widens to every plan.
  `plan load <path>@<commit>`, the old form, is refused with a migration error: "plans are
  adopted through a draft and the person's yes: agentbus plan draft <path>@<commit>, then
  plan load". There is no bypass flag. The recovery path for a file written by hand is `plan
  draft` itself (onboarding §8.1): the same banner, diff and typed yes.
- **`plan draft --from <file>`** is an alias of `plan draft <file>@HEAD` (follow-up, ask
  1795852308652-0). It is refused when the file has uncommitted changes: "<path> has uncommitted
  changes: commit it on any branch, then run plan draft again". Any branch is accepted, and the
  commit need not be pushed. The draft records the path, the full commit SHA and the file's
  blob hash (`git rev-parse <commit>:<path>`). `plan load` re-reads that blob at that commit and
  refuses when it differs, on top of onboarding §8.5's checks. No copy of the bytes goes to
  Redis. The active v0.34 stays loaded until v0.35's
  draft is adopted.

## A5. The stale warning, only as a safety net

The health check `plan-stale` (warn, never fail) replaces `plan-drift` and the 7-day label (Q3).
`plan-drift` is removed with its help and goldens. It has no registry key in `config/keys.go`, so
there is none to deprecate; `agentbus health` names a removed check once, "plan-drift: replaced by
plan-stale", for one release.
It warns in exactly two cases:
- **a plan is loaded with no open task**: every task done, or none waiting, in hand or upcoming,
  for longer than `plan.stale_after` (default 24 h, in `config/keys.go`), and no draft recorded;
- **a release was tagged without a next draft**: a tag `vX.Y.0` newer than the plan's load that
  closes the loaded plan, with no draft recorded `plan.stale_after` after the tag (the
  person's "N days": the setting, 24 h by default, is how many). The tags are read from the
  checkout, as `releases.go` reads them.

It wakes master once per episode (as `plan-drift` does today, `PlanDriftWakes`), with a fixed line:
"plan v0.34 has no open task: ask the drafter for the next plan". The unplanned work of §A3 is no
longer an alarm. It is a section the person sorts.

## A6. A task for the person shows the command to run (idea i18)

- **`command`** is an additive plan field: one line of at most 200 characters, valid only on a
  `person` task. It holds the exact command, for example `agentbus migrate solana`. The detail
  pane (§B4), the task page, `plan show` and `summary`'s Plan line show it as "Run: …", in
  monospace.
- **A wait on the person that the plan did not write** gets a command derived from its state:
  - **a merge waiting:** `gh pr merge <n>` on GitHub (the project's `[delivery]` forge), with the
    project's merge method when one is set. Other forges get the PR's URL;
  - **an ask waiting:** "answer in busmon: For you, ask <id>", and the equivalent `agentbus answer
    <id> <question> --choice <label>`;
  - **anything else:** no command, and no "talk to master" either.

  `plan.PersonCommand(row, forge)` is the one function.
- i18 is set to `started` by this spec's build, and to `done` with the build of §A6.

# Part B. The Plan tab redone

The tab draws what Part A derives. It reuses the fine-rendering bars (`internal/finedraw`:
`ThinBar`, `Bar`) and `plan/derive.go`'s rows. It reuses busmon's existing roles, so no new colour
is introduced.

## B1. A group's state comes from its tasks

`plan.GroupState(rows)` is the one rule, checked in this order:

1. every task is done: **done**;
2. any task waits for the person: **for you**;
3. any task is blocked: **blocked**;
4. any task is in hand (`InHand`), or some are done and some are not: **in progress**;
5. otherwise: **todo**.

So a group with 2 of 3 done is "in progress", never "not started". Today's `planGroupState`
(`cmd/busmon/plan_r2.go:255`) is replaced by this rule.

## B2. Rows

- **A leading coloured glyph**, and no status column:
  - `○` todo, muted;
  - `◐` in progress or in review, accent;
  - `●` done, OK;
  - `⊘` blocked, error;
  - `!` waiting for the person, attention.

  An approved task waiting for the person's merge is `!`.
- **The title**, then the owner role and the PR, dim, right after it: `coder #737`. The state's
  words are in the detail pane only.
- **Blocking dependencies** go under the row, dim: `↳ waits on wip-limit-spec, api-auth`. Only
  the ones not done are listed.
- Done rows stay folded (`DONE 9  d shows`), as today.

## B3. Progress at one shared scale

- **The header:** `v0.34 ━━━━━━━━╌╌ 10/11 · 1 in progress · 1 for you`. Done cells are `━` in the
  OK role and the rest `╌` muted, drawn by `finedraw.ThinBar`. "In progress" counts `InHand` rows,
  and "for you" counts rows waiting on the person.
- **Group bars** use one scale: every group gets the same cells per task. A 3-task group's bar is
  shorter than an 11-task one, so the bars compare at a glance. The scale is
  `floor(barWidth / largest group's task count)`, at least 1 cell per task. A group too big for
  the width is clipped with `…`.

## B4. The detail pane

The Releases column goes. At 120 columns and wider, the right pane shows the selected task:

- the glyph, id and title;
- **State:** the derived state in words, with its reason ("approved at 1ca5d19, waits for your
  merge");
- **Owner** and **PR** (a link);
- **Spec:** the task's `spec` field, an additive path to a spec file, as a link;
- **Check:** the acceptance check, and its evidence: "exit 0 at <sha> (reviewer)", or "not run
  yet";
- **Deps:** the `after` list, each with its glyph;
- **Last:** the newest event about the task, with its age (request, verdict, merge, ask);
- **Run:** the command (§A6), when the task waits on the person.

## B5. Releases move to the header

- The frame's right side holds a compact **selector**, `v0.34 ▾`.
- **`V`** (today's key, `keys.go:290`, now an overlay) opens the release list:
  - the next release, with its decided ideas;
  - the current plan;
  - past releases, newest first, grouped by month ("Nov 2026: v0.33, v0.32, v0.31").
- Choosing a past release shows its plan file, read-only, from the checkout (`releases.go`'s
  reader, unchanged). Esc returns to the active plan.

## B6. The Next line

The tab's last line names the next task that can start, and what waits on the person:
`Next: <task> → <owner> · for you: merge #737`.
- **The next task** is `plan.NextFree(rows)`: the first `Upcoming` row in file order whose
  `WaitingOn` is empty, so none of its `after` tasks is still open. This is the person's "top
  unblocked task". Priority is not used: the file's order is the plan's order (§A2).
- With none free, the line reads `Next: none free (<first upcoming> waits on <its first open
  dep>)`. The mock-ups show that case.
- **`plan.NowNext` is unchanged** (status-summary §3.2: today's `planInHand`, the first
  upcoming row in file order, deps not read). It keeps feeding the plan header's "in progress"
  and the idle view. `NextFree` is a second function for a second question. A test fixes the
  difference: with the first upcoming row waiting on an open task, `NowNext` returns it, and
  `NextFree` returns the next free row, or none.
- After the task comes the first thing waiting on the person, with its command when it fits.

## B7. Responsive tiers

| Width | Layout |
|---|---|
| under 120 (80 is the golden) | the list alone. `Enter` opens the task's page, which holds the detail pane's fields. The Next line is shown. |
| 120 to 159 | the list (about 48 %) and the detail pane |
| 160 and wider | the same, plus a **mini timeline** of the selected task in the detail pane (Q6) |

The **mini timeline** (Q6) is the selected task's events as a list, **newest at the bottom**,
one per line, each with its relative age. It follows the selection. From the facts derive
already reads, in this order of kinds:
- `◦` requested, `●` accepted, `◇` PR opened;
- `◐` review asked, then `✓` or `✗` per verdict, with the reviewer;
- `!` waiting on the person, in the warning color, with how long ("20h");
- CI red or green (`MergeItem.CIRed`);
- `■` merged, `●` done.

When the events do not fit the pane's height, the oldest go first, behind a line "… N
earlier". The plan's progress stays the header bar; progress per day belongs to History, not
here.

## B8. Mock-ups (goldens at 80x24, 120x40, 160x45)

80 columns: the list alone.

```
╭─ Plan ───────────────────────────────────────────────────────────── v0.34 ▾ ─╮
│ v0.34 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╌╌╌╌╌╌╌╌╌╌╌╌  9/11 │
│       1 in progress · 1 for you                                              │
│                                                                              │
│ Broker ACL      ━━━━━━━━━━━━━━━━━━━━  4/4  done                              │
│ Specs           ━━━━━━━━━━━━━━━╌╌╌╌╌  3/4  in progress                       │
│  ◐ wip-limit-spec  Spec: a work-in-progress limit       architect #736       │
│ Builds          ━━━━━━━━━━╌╌╌╌╌       2/3  for you                           │
│  ! single-approval-build  Build the single approval     coder #737           │
│                                                                              │
│ UNPLANNED  2 merged, not in the plan                                         │
│  ◇ #835 busmon: the Kanban's column rules (coder)                            │
│  ◇ #837 busmon: mark all read in Activity (coder)                            │
│                                                                              │
│ DONE  9   d shows                                                            │
│                                                                              │
│ Next: none free (quiet-update-spec waits on wip-limit-spec) · for you: #737  │
╰──────────────────────────────────────────────────────────────────────────────╯
 ↑↓  enter task  V releases  a attach  n next plan  x not plan  + - priority
```

120 columns: the list and the detail pane.

```
╭─ Plan ───────────────────────────────────────────────────────────────────────────────────────────────────── v0.34 ▾ ─╮
│ v0.34 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╌╌╌╌╌╌╌  9/11      │ ! single-approval-build                                   │
│       1 in progress · 1 for you                          │   Build the single approval                               │
│                                                          │                                                           │
│ Broker ACL   ━━━━━━━━━━━━━━━━━━━━  4/4  done             │ State   approved at 1ca5d19, waits for your merge         │
│ Specs        ━━━━━━━━━━━━━━━╌╌╌╌╌  3/4  in progress      │ Owner   coder          PR #737                            │
│  ◐ wip-limit-spec  Spec: a WIP limit  architect #736     │ Spec    2026-11-21-single-approval-design.md              │
│  ○ quiet-update-spec  Spec: quiet update  architect      │ Check   go test ./cmd/agentbus -run Single                │
│    ↳ waits on wip-limit-spec                             │         exit 0 at 1ca5d19 (reviewer-codex)                │
│ Builds       ━━━━━━━━━━╌╌╌╌╌       2/3  for you          │ Deps    none                                              │
│  ! single-approval-build  Build single…  coder #737      │ Last    approve, reviewer-codex, 2h ago                   │
│                                                          │                                                           │
│ UNPLANNED  2 merged, not in the plan                     │ Run     gh pr merge 737                                   │
│  ◇ #835 busmon: Kanban column rules                      │                                                           │
│  ◇ #837 busmon: mark all read in Activity                │                                                           │
│                                                          │                                                           │
│ DONE  9   d shows                                        │                                                           │
│                                                          │                                                           │
│ Next: none free (quiet-update-spec waits                 │                                                           │
│       on wip-limit-spec) · for you: merge #737           │                                                           │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  V releases  a attach  n next plan  x not plan work  + - priority  d done
```

160 columns: the detail pane gains the timeline.

```
╭─ Plan ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── v0.34 ▾ ─╮
│ v0.34 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╌╌╌╌╌╌╌  9/11      │ ! single-approval-build                                                                           │
│       1 in progress · 1 for you                          │   Build the single approval                                                                       │
│                                                          │                                                                                                   │
│ Broker ACL   ━━━━━━━━━━━━━━━━━━━━  4/4  done             │ State   approved at 1ca5d19, waits for your merge                                                 │
│ Specs        ━━━━━━━━━━━━━━━╌╌╌╌╌  3/4  in progress      │ Owner   coder          PR #737                                                                    │
│  ◐ wip-limit-spec  Spec: a WIP limit  architect #736     │ Spec    2026-11-21-single-approval-design.md                                                      │
│  ○ quiet-update-spec  Spec: quiet update  architect      │ Check   go test ./cmd/agentbus -run Single                                                        │
│    ↳ waits on wip-limit-spec                             │         exit 0 at 1ca5d19 (reviewer-codex)                                                        │
│ Builds       ━━━━━━━━━━╌╌╌╌╌       2/3  for you          │ Deps    none                                                                                      │
│  ! single-approval-build  Build single…  coder #737      │ Last    approve, reviewer-codex, 2h ago                                                           │
│                                                          │                                                                                                   │
│ UNPLANNED  2 merged, not in the plan                     │ Run     gh pr merge 737                                                                           │
│  ◇ #835 busmon: Kanban column rules                      │                                                                                                   │
│  ◇ #837 busmon: mark all read in Activity                │ Timeline  (this task)                                                                             │
│                                                          │ … 3 earlier                                                                                       │
│ DONE  9   d shows                                        │ ✓ foureyes approve              1d                                                                │
│                                                          │ ✓ reviewer-codex approve        20h                                                               │
│ Next: none free (quiet-update-spec waits                 │ ● CI green                      20h                                                               │
│       on wip-limit-spec) · for you: merge #737           │ ! waits on you: merge #737       20h                                                              │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  V releases  a attach  n next plan  x not plan work  + - priority  d done
```

## B9. Keys

- `a`, `n` and `x` act on an UNPLANNED item, and do nothing on a task row. `z` undoes the
  selected sorted item's sort (§A3); it is bound in the Plan panel only, as Activity's `z` is in
  Activity only.
- `+` and `-` keep their meaning: the person's priority on a task row (`ActPriorityUp`,
  `keys.go:279`). onboarding §12 relies on them too.
- `V` opens the releases, and `d` shows the done rows, as today.
- `a`, `n` and `x` are bound in other panels and on pages (`a` for Agents and Ideas, `n p` for a
  page's next and previous), not in the Plan panel's bindings. The key test checks them in the
  Plan panel.

# Build steps, tests

Each step is one PR, in this order. **Prerequisites from other specs**, which master must see
merged before sending the step:
- **Step 1** is sent only after both of these have merged, and is never sent in part:
  - status-summary step 4 (`request send --plan` provenance), which rule 1 of §A1 reads;
  - onboarding step (a) (the evidence rule, `ResultHead`), for tasks with a `check`.

  There is no interim state in which provenance-linked tasks are invisible.
- **Step 3** needs onboarding step (b): `plan draft`, `TypedApproval` and the draft load.
- **Step 6** needs fine-rendering's `finedraw.ThinBar` (merged) and status-summary step 1
  (`plan.NowNext`).

The steps:

1. **Links and derived done** (§A1, §A2):
   - the work: `plan.Links`; `MergeClose.PR`; `Request.Merged` and its pass, with the backfill;
     derive's merge rule; the manual-done rule; the `plan done` refusal; `plan attach`;
   - tests:
     - a task finds its requests by provenance, by attachment, by `board`, and by id;
     - **two requests for one task:** a second `--plan` request is refused while the first is
       open; with the first merged and a rework sent and still open, the task is not done; with
       the rework closed on merge, it is done;
     - a merge task with a request closed on merge and approved at that head is done with no fact;
     - approved at another head, it is "approved, head changed";
     - a verdict with no head counts, with its label, on a merged request, and does not count on
       a request not merged;
     - `agentbus verdict … approve` with no `--head` is refused; a reject with none is recorded;
     - `plan done` is refused with linked work;
     - an 11-task fixture whose requests are `--plan`-linked and merged shows 11/11 with no fact.
2. **Unplanned** (§A3):
   - the work: `mergequeue.ReadGitHubMerged` and its call in the merge-queue pass;
     `{p}:plan:unplanned`, fenced, dispatcher-only; `plan next add`; the sort records and `z`;
   - tests:
     - with a faked `gh` (the merge-queue chore's fake): a PR merged into `dev` that no request
       references is unplanned, tagged "off-bus"; one whose branch, head or number a request
       names is not;
     - a cut list (exactly the limit) and a failed read keep the previous off-bus items;
     - a local forge, or no `gh`: no forge call, the board only, and the header's notice line;
     - no `git log` is run for this section (the fake git records its calls);
     - a `--plan` request is not;
     - an `--own` request is;
     - `plan ignore` removes an item;
     - a sort records who, when and reason; master's CLI sort without `--reason` is refused;
     - `z` undoes a sort, and the undo is recorded;
     - master's sort of an item the person sorted is refused; the person's change of master's
       sort is accepted;
     - a load drops the hash;
     - the ACL refuses the agent user a write to `plan:unplanned`.
3. **Release draft and internal load** (§A4):
   - the work: the checklist step; `plan load` refusing without an approved draft; the
     drafter's skill section;
   - tests:
     - `plan load` with no draft is refused;
     - `plan load <path>@<commit>` is refused with the migration error, approved draft or not;
     - `plan draft --from <file>` with uncommitted changes is refused, naming the path; on an
       unpushed branch commit it records path, SHA and blob hash; a load whose blob differs is
       refused;
     - docs' release recap reaches the architect as a report that wakes it;
     - an approved draft loads;
     - the draft carries the remaining tasks, the `next` items and NEXT-DIRECTIONS items.
4. **`plan-stale`** (§A5), with `plan-drift` retired (Q3):
   - tests:
     - the two cases warn, once per episode;
     - a draft recorded silences both;
     - the release case follows `plan.stale_after` (a fixture at 3 days);
     - `agentbus health` shows "plan-drift: replaced by plan-stale" and no plan-drift check;
     - no other case warns.
5. **The person's command** (§A6):
   - tests:
     - a `command` on a non-person task is refused by `Validate`;
     - the derived commands for a merge (GitHub, another forge) and for an ask.
6. **The Plan tab** (§B):
   - the work: `GroupState`, the rows, the shared scale, the detail pane, the releases overlay,
     the Next line, the tiers, the timeline, the keys;
   - tests:
     - goldens at 80x24, 120x40 and 160x45, covering every glyph, a `↳ waits on` line, the
       unplanned section, the selector, the Next line and the timeline;
     - `GroupState` for each case, with 2/3 done reading "in progress";
     - the shared scale for a 3-task and an 11-task group;
     - the overlay grouped by month;
     - the timeline: newest last, "… N earlier" when it overflows, the wait on the person in the
       warning color with its duration, and it changes with the selection.

# What this does not do

- No new store for done: "merged" is the dispatcher's existing record.
- No automatic attach or add-to-plan. The person, or master, sorts unplanned work (Q4).
- No plan change without the person's typed yes.
- No timeline across tasks. The mini timeline is one task's (Q6); progress per day is History's.

# §C. Questions for the person (one ask, in French)

1. **Q1 Who drafts the next plan at a release:** the architect, who wrote the draft flow and owns
   `NEXT-DIRECTIONS.md` (recommended); or docs, who prepares the release.
2. **Q2 `plan load` by hand:** removed, so every plan comes through a draft and your typed yes
   (recommended); or kept as master's emergency path, shown in busmon as "loaded without a draft".
3. **Q3 The `plan-drift` alert:** retired, with the Unplanned section and `plan-stale` replacing
   it (recommended); or kept, fixed to read `dev` and squash merges.
4. **Q4 Sorting unplanned work (attach, next plan, not plan work):** you in busmon, and master
   (recommended); or you only.
5. **Q5 Older approvals that name no head:** count them, labelled "approval names no head", so
   the past tasks are done (recommended); or require a head, so those tasks need a fresh review.
6. **Q6 The mini timeline at 160 columns:** the selected task's events (recommended); or the whole
   plan's progress over time (done per day).

## §C.1 The person's answers (ask 1795818074022-0, typed, 2026-11-28)

All six took the recommended option. What they added is folded in above:
1. **Q1, the architect drafts.** Docs publishes the release recap and wakes the architect; docs
   never edits the plan; the safety net fires when no draft exists some days after a release
   (§A4, §A5).
2. **Q2, `plan load` only loads the approved draft**, by its id, with no bypass flag, and a
   migration error for the old form. The recovery is `plan draft` on the file (§A4).
3. **Q3, `plan-drift` removed**, with its help and goldens, and no git-history heuristics. On the
   follow-up, §A3's second source is the forge reader, not `dev`'s history (below).
4. **Q4, the person in busmon and master by the CLI.** Master's sorting is recorded (who, when,
   reason) and reversible from busmon; master never re-sorts what the person sorted; "next plan"
   feeds only the draft (§A3). The ask's option said `+` for next plan. The key is `n`, because
   `+` stays priority (§B9).
5. **Q5, a headless approval counts only on a merged request**, labelled. On one not merged a
   re-review naming the head is required, every new approval names its head, and no old record
   is rewritten (§A2).
6. **Q6, the selected task's events**, newest at the bottom, relative times, the wait on the
   person in the warning color with its duration (§B7, §B8).

Two follow-ups, ask 1795852308652-0:
- **Does the off-plan section still read `dev`'s history?** Answered: no. The board's requests
  stay; the second source is the forge reader ci-loop's pass uses, for merged PRs no request
  references, tagged "off-bus"; with no forge reader, the board only and a notice (§A3).
- **Must `plan draft --from <file>` take a committed file?** Answered: yes. An alias of `plan
  draft <file>@HEAD`, refused on uncommitted changes, any branch, unpushed accepted; the draft
  records path, commit and blob hash, and the load re-reads the blob (§A4). No Redis copy.
