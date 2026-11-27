# The plan's lifecycle, and the Plan tab redone (plan-lifecycle)

- Status: spec r2, for review (subject `plan-lifecycle-spec`). Not built.
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
  - **merged:** its newest request was closed on merge, its head delivered into the base. This is
    `Request.ClosedOnMerge`, which the dispatcher's merge-queue pass writes
    (`internal/dispatchsvc/closeonmerge.go:37`, through `mergequeue.Delivery.Delivered`:
    ancestor, squash patch-id or rebase patch-ids). Or a PR attached by hand that the merge queue
    saw merged;
  - **reviewed:** its review rolls up `APPROVED` (`bus.RollUp`). The deciding approval names the
    merged head (`Verdict.Head == ClosedOnMerge.Head`). An approval at another head is "approved,
    head changed" (onboarding §9.2). An older verdict with no head still counts, labelled
    "approval names no head": `agentbus deliver` already refused to push a head without one, so
    such verdicts predate it (Q5).

  For a task with a `check`, onboarding §9's evidence rule applies on top.
- **A `merge: false` task:** delivered and approved, as today. With a check, onboarding §9.
- **A person task:** as today, and onboarding §9.4 for one with a check.
- **`plan done`** becomes an exception for a task with no linked work (a step done outside the
  bus). busmon shows it as "recorded by master". It is refused on a task that has linked work:
  "<task> has linked work: its state comes from it". This uses the same guards as onboarding §9.3.
- **A manual done is ignored once work is linked.** Today derive returns `Done` for `fact.Done`
  before it reads anything else (`derive.go:117`). The new order is:
  1. derive calls `plan.Links` first;
  2. when the task has linked work (a request, an attachment, a `board` entry), `fact.Done` is
     ignored, and the state comes from that work;
  3. the detail pane says "recorded done ignored: linked work decides".

  So attaching an unapproved PR after a `plan done` makes the task "in review", never done. The
  fact stays in `{p}:plan:facts`, unchanged, and counts again only if the link goes. Test: `plan
  done` on a task with no work, then `plan attach` of an open, unapproved PR, gives "in review";
  then that PR merged and approved at its head gives done.

So "merged" is a fact the dispatcher already writes. No one records it any more.

## A3. Unplanned work

**What counts as unplanned:** work that merged into the base while the plan was active, and that
belongs to no task:
- **requests closed on merge** whose provenance is not `plan:<a task of the plan>` and that are
  not attached;
- **merges in the base** that no request names. The dispatcher reads them from the base's
  first-parent history since the plan's load. That is today's `plan-drift` reader, fixed: it reads
  the delivery base (`[delivery] base`, `dev` here) instead of `origin/main`, and it reads the
  squash subject `title (#N)` as well as the merge-commit one;
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

Who may run them is Q4.

**The first pass lists a backlog.** Every request merged during the active plan with no
provenance (all of today's), and matching no task by `board` or id, is unplanned. That is the
intent: it is how `wip-limit-step1` and the work behind v0.34's three open tasks surface, so the
person attaches it. It is not a bug.

## A4. At each release, a draft of the next plan

- **When:** a minor release tag `vX.Y.0` closes plan `vX.Y` (busmon already maps them,
  `releases.go:109`, `planOfTag`). The release checklist gains one step: master directs the
  plan's drafter (Q1) to "draft v(X.Y+1)".
- **What the draft holds:**
  - the tasks of the current plan not done, carried with their ids (onboarding §8.6);
  - the first items of `NEXT-DIRECTIONS.md`, when the file exists;
  - the `{p}:plan:next` items, each turned into a task;
  - the unplanned items not yet sorted, listed in the question's context, not as tasks. The
    person sorts them with `a`, `+` or `x`.
- **How:** the same flow as `agentbus init`. The drafter commits the file, runs `plan draft`
  (onboarding §8.1), and the person's typed `yes` adopts it (`TypedApproval`, onboarding §8.4).
  On that yes master runs the load, and only that load works (onboarding §8.5). Nothing else is
  new.
- **`plan load` becomes internal** (Q2). It loads only the recorded, approved draft. The
  onboarding rule "a plan with `checks: required` is loaded only as a draft" widens to every plan.
  `plan load <path>@<commit>` with no approved draft is refused: "plans are adopted through a
  draft and the person's yes (agentbus plan draft)". The active v0.34 stays loaded until v0.35's
  draft is adopted.

## A5. The stale warning, only as a safety net

The health check `plan-stale` (warn, never fail) replaces `plan-drift` and the 7-day label (Q3).
It warns in exactly two cases:
- **a plan is loaded with no open task**: every task done, or none waiting, in hand or upcoming,
  for longer than `plan.stale_after` (default 24 h, in `config/keys.go`), and no draft recorded;
- **a release was tagged without a next draft**: a tag `vX.Y.0` newer than the plan's load that
  closes the loaded plan, with no draft recorded 24 h after the tag. The tags are read from the
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

The **mini timeline** is the task's events on one line, oldest left. Each event is a mark, from the
facts derive already reads:
- `◦` requested, `●` accepted;
- `◐` review;
- `✓` or `✗` for each verdict;
- `!` waiting on the person;
- `■` merged.

The spacing is proportional to time, over the task's life, with labels under the main marks.

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
│  ◇ #837 busmon: mark all read in Activity                │ Timeline  (this task, 3 days)                                                                     │
│                                                          │   ◦──●────●──◐───✓─✓────!                                                                         │
│ DONE  9   d shows                                        │   req acc  rev    approve  you                                                                    │
│                                                          │                                                                                                   │
│ Next: none free (quiet-update-spec waits                 │                                                                                                   │
│       on wip-limit-spec) · for you: merge #737           │                                                                                                   │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  V releases  a attach  n next plan  x not plan work  + - priority  d done
```

## B9. Keys

- `a`, `n` and `x` act on an UNPLANNED item, and do nothing on a task row.
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
   - the work: `plan.Links`; `MergeClose.PR`; derive's merge rule; the `plan done` refusal;
     `plan attach`;
   - tests:
     - a task finds its requests by provenance, by attachment, by `board`, and by id;
     - **two requests for one task:** a second `--plan` request is refused while the first is
       open; with the first merged and a rework sent and still open, the task is not done; with
       the rework closed on merge, it is done;
     - a merge task with a request closed on merge and approved at that head is done with no fact;
     - approved at another head, it is "approved, head changed";
     - a verdict with no head counts, with its label;
     - `plan done` is refused with linked work;
     - v0.34's fixture (requests `--plan`-linked, merged) shows 11/11.
2. **Unplanned** (§A3):
   - the work: the fixed reader (delivery base, squash subjects); `{p}:plan:unplanned`, fenced,
     dispatcher-only; `plan next add`;
   - tests:
     - a squash-merged PR on `dev` with no request is unplanned;
     - a `--plan` request is not;
     - an `--own` request is;
     - `plan ignore` removes an item;
     - a load drops the hash;
     - the ACL refuses the agent user a write to `plan:unplanned`.
3. **Release draft and internal load** (§A4):
   - the work: the checklist step; `plan load` refusing without an approved draft; the
     drafter's skill section;
   - tests:
     - `plan load` with no draft is refused;
     - an approved draft loads;
     - the draft carries the remaining tasks, the `next` items and NEXT-DIRECTIONS items.
4. **`plan-stale`** (§A5), with `plan-drift` retired (Q3):
   - tests:
     - the two cases warn, once per episode;
     - a draft recorded silences both;
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
     - the overlay grouped by month.

# What this does not do

- No new store for done: "merged" is the dispatcher's existing record.
- No automatic attach or add-to-plan. The person, or master, sorts unplanned work (Q4).
- No plan change without the person's typed yes.
- No timeline across tasks. The mini timeline is one task's (Q6).

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
