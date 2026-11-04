# Health check `review-queue` (plan task review-queue-idle)

Status: agreed with master on 2026-11-04 (engaged reviewers, 72 h fixed, wake master only); implemented on the same branch. Short spec: the design has one choice that is not
trivial, the meaning of "half-approved" (§2).

## 1. The hole

On 2026-11-04 the team sat idle about 13 h. A branch had one reviewer's approval on its head and
waited for the other, but that second review existed only as a line in a report: no open gate, no
message. So nothing woke anybody:

- `team-stall` reads the plan. A task in review is not `Upcoming`, so it said "plan has nothing
  ready" (ok).
- An open gate would not have helped either. `team-stall` counts it as open work ("work is open:
  1 gate", ok) even when its reviewer read the challenge long ago and went idle.
- `quiet-agent` judges only directives and escalations.

`review-queue` warns, and wakes master once per episode, when every non-dormant agent has idled for
`dispatch.stall_after` and a review is still owed. Master can always act on it: re-challenge,
nudge the reviewer, or record that one approval is enough.

## 2. What "a review is owed" means

Nothing on the bus says which reviewers a subject needs. The roll-up, and `deliver`, ask for one
independent approval. So the check reads only what the team itself started: a reviewer counts for
a subject once that reviewer has engaged with it, either by being challenged on it or by giving a
verdict on it. Two cases:

**(a) Owed: an open gate.** A gate is open on a non-dormant reviewer, its challenge has been
delivered (it is not among the reviewer's unread or pending entries, which `cmd-delivery` and the
dispatcher already cover), and its challenge entry is older than `stall_after`. The detail names
the reviewer, the gate's subject (or its ref) and the age. A gate whose challenge entry is no longer
retained is never owed: a consumer group skips an entry trimmed before it read it, so even a cursor
past it proves nothing. It is named "not judged", and the check reads unknown when nothing else is
owed (reviewer-codex).

**(b) Half-approved.** For a subject S whose roll-up is `APPROVED`, take the deciding approval D,
with its `Head` when it names one. S is half-approved when some other reviewer R (not D's reviewer
and not the author) engaged with S, and R's newest verdict on S is one of these:
- missing: R was challenged on S, but R gave no verdict. Challenged on S means a challenge entry
  to R under the `Ref` of a verdict on S: one ref shared by both reviewers' challenges. A challenge
  entry carries no subject, so a ref that only R's challenge carries reaches S through R's open
  gate, which is case (a) while it is open;
- a reject older than D;
- an approve of another head (both heads known and different).

An approve from R with no head, or on the same head, settles R.

The review is not in this check's hands when:
- D is older than `review_queue_window` (72 h, fixed, no setting): an old subject is history.
  Without the window, every branch merged after one reviewer would warn forever.
- the plan records S's task as done.
- S is `pr:<n>`, and the plan task linking that PR is done.

Why "engaged" and not "foureyes and reviewer-codex": a docs-only change needs only foureyes (the
release notes, the plan additions). A fixed list would warn on every such change. The cost is the
11-04 case if reviewer-codex had never been challenged on the subject at all. Then nothing records
that it owes a review, and the shared memory note's rule stays the guard: open every challenge up
front. On 11-04 itself, reviewer-codex had already given a verdict on an older head of
focus-notice, so case (b) catches it.

## 3. The judgement, the reader and the wake (the stall guard's pattern)

- **Judgement:** `health/reviewqueue.go`, pure.
  - `EvaluateReviewQueue(ReviewQueueInput) Outcome`: off when `stall_after` = 0; unknown on a
    failed read or an unseen pane, as `team-stall` judges them; ok while any agent works, is busy
    or idled less than `stall_after`, or while other work is open (the `TeamOpenWork` items
    except `Gates`); warn with the owed and half-approved subjects, sorted, cut to the detail's
    limit.
  - `ReviewQueueWakes(in, master) []StallWake`: one wake to master, mark `stall-review`, episode =
    a hash of the sorted owed keys. A gate is keyed by its ref; a half-approved subject by
    `S|head`. A new owed review is a new episode, and the same set wakes once.
- **Reader:** `cmd/agentbus/stallchecks.go`, `stallReader.reviewQueue`.
  - Agents, observations and open work as `teamStall` reads them.
  - Every agent's gates.
  - The ledger, read forward through `readVerdicts`, which already exists.
  - The retained cmd challenges, ref → target.
  - The plan, for done tasks.
- **Wake:** the existing `StallWake` hook, extended to `review-queue`, through `FencedCmdOnce`
  from `dispatch`. A swap of the target is skipped until the next pass, as for the other wakes.
- **Definition:** in `health.go`, Handler master, Interval 1 min, Order next to `team-stall`.
  Its warn is in hand by master, so it is not For you. It never fails, so it never wakes sentinel.
- **Tests:**
  - the evaluator: a table per case of §2, plus off, unknown and open work;
  - the wake's episode: stable across passes, new when a subject is added;
  - the reader with a fake bus, as `stallchecks_test.go` does;
  - one Redis test for the hook's once-per-episode.

  A line in CLAUDE.md, in the stall-guard paragraph.

## 4. Questions for master

1. Is "engaged reviewers" (§2) right, or should a fixed reviewer list come from a setting?
   A list would also catch the never-challenged case, but it would warn on docs-only subjects
   unless the list is per subject.
2. The 72 h window: fixed, or a setting?
3. Should the wake go to master only, or also to the reviewer that owes, as a directive? A
   reviewer pop that stops after each verdict needs the wake. But the dispatcher wakes only on an
   undelivered entry, and its challenge was delivered.
