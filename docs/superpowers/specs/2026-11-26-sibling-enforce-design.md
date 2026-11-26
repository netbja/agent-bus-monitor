# Sibling steps run in series, enforced on the board

Status: spec r1, for review (subject `sibling-enforce-spec`). Not built. Base: origin/dev `d3bc5568`.
Author: architect, from the merge-conflicts measure run early on 2026-11-26 (thread
`1795676948573-0`, asked by claude-ao for the person) and the person's typed answer to ask
`1795677070804-0` Q1: enforce it now, with a `parallel` exception for steps that share no file
(cited case: #823 and #824). Task from master: cmd `1795725770587-0`. Parent spec:
[merge-conflicts](2026-11-20-merge-conflicts-design.md) §3. No new dependency.

## 1. What broke, and why the plan cannot catch it

From 2026-11-21 to 2026-11-26 the rule "sibling steps run in series" was broken 5 times, and
12 PRs were redelivered after a conflict (the table is on the thread above). The 5 breaches:

| Build request (board) | Steps open at the same time | Conflict paid |
|---|---|---|
| `resource-limits-build` (coder2) | 2a #810, 2b #812, 3a #814, 3b #818 | #818 on its siblings' files, then #819 by knock-on |
| `retro-build` (coder) | step 2's guard #813 beside step 2 #815 | none between them |
| `single-approval-build` (coder2) | 3 #796 beside 3b #801 | none between them |
| `audit-build` (coder2) | 2b #799 beside 3 #802 | none |
| `board-drift-build` (coder) | 4 #757 beside 1 #758 | none |

Every breach is **inside one board request**. Master sends one request per build
(`<spec>-build`). The implementer then cuts the steps and puts each one in review with
`agentbus request review <task> --branch <b> --more`. Master never sent "step N+1" itself. The
master skill's wording ("send it once N is merged", `skills/agent-bus-master/SKILL.md:749`)
assumes a request per step, which is not how the team works.

So the merge-conflicts §3 shape, `plan.Validate` refusing two plan tasks of one group in flight,
would have caught **none** of the 5. The plan (`docs/plans/v0.34.json`) holds one task per build,
too coarse to see steps. The enforcement belongs where the steps exist: the board request and
its review transitions.

## 2. What the board already knows

- `request review --branch B --more` records `branch`, `commit`, `review_base`,
  `review_more=true` and bumps `review_n` (`bus/requests.go:465-473`). That is the open step.
- The merge pass (`internal/dispatchsvc/closeonmerge.go:81`) judges only requests in `review`
  with a branch and a `review_base`. On a `--more` step's merge, `requestMergedScript`
  (`bus/requestmerged.go:37-43`) appends the head to `merged_steps`, clears `branch`, `commit`,
  `review_more`, `review_base`, and sets the request back to `accepted`.
- `request accept` from `review` (a resume) keeps all those fields
  (`bus/requests.go:445-464` touches none of them). It is how an agent fixes a rejected step.
  It is also how it starts the next step while the previous one waits.

The board shows the gap today: `resource-limits-build` is `accepted` with
`branch=coder2/reslimits-2b`, `review_more=true`, review note "2a r2 + 2b r2 in review". The
second review overwrote the first step's branch, and the resume took the request out of the
merge pass's sight (it reads `review` only), so 2b's merge never stepped it.

## 3. The rule in code

**One open step per request.** A request has an open step when `review_more` is true and
`branch` is set, in any state but `done`. The step stays open until the merge pass records its
merge.

1. **`request review` refuses a second step.** In the transition script's `review` branch: if
   the request has an open step on branch S, the new review names a branch B ≠ S, and the
   request is not `parallel`, refuse. While a step is open, `--branch` is required: a review
   without it is refused (`a step is open on S: name the branch, --branch S`). Today the script
   keeps the old branch when none is given (`bus/requests.go:468`), and the CLI records the
   checkout's HEAD as the commit (`cmd/agentbus/requests.go:93-101`). A new step reviewed
   without `--branch` would then pass as S.
   Refusal text (one `redis.error_reply`, listed for `TestEveryScriptRefusalIsListed`):

   `step S of <task> is not merged yet: sibling steps run in series (merge-conflicts §3); take
   another task, or ask <from> for agentbus request parallel <task>`

   A review of the same branch S (a fix after a reject, a rebase) passes, as today. A review
   with no `--more` on a new branch while S is open is refused the same way: the last step
   still follows the one before it.
2. **The merge pass also sees a resumed step.** `closeOnMerge` judges a request in `review`
   *or* `accepted` with `review_more` and a branch and a `review_base`. `requestMergedScript`
   accepts the same two states. On an `accepted` request it does what it does for a step today,
   except that the state stays `accepted` (it is already there). The `review_n` fence is
   unchanged: a newer review voids the pass's read. This is what lets the rule lift by itself.
   Without it, a resumed step would stay "open" forever and every later step would be refused.
   It also clears today's stale `resource-limits-build` row at the first pass after the build
   (2b #812 is in `dev`).
3. **`request accept` is unchanged.** A resume stays allowed: the agent may fix its step, and
   may work on anything else meanwhile. Coding the next step locally is not refused (nothing
   can see it). Putting it in review is. The reviewers, and `deliver`, start from that review.

**The `parallel` exception (the person's Q1).** `agentbus request parallel <task> <reason...>`
sets `request.parallel = {by, reason, at}` (`at` from Redis `TIME`). `request parallel <task>
--off` clears it. Who may set it: the request's sender (`from`, master in practice) or the person
(`--as <person>`, as `request priority` does). The target is refused: the implementer does not
lift its own rule. A `done` request is refused. One script on the board, as `requestPrioritySrc`
(`bus/priority.go:120`). It writes no stream entry and one audit line (`action=parallel`). While
it is set, rule 1 does not apply to that request. Several steps may then be open, and the
request records only the newest branch, as today. Rule 2's merge pass then steps on whichever
branch is recorded. The others close by `request done` or by master, as today.

The reason is mandatory and one line (≤ 200 runes). Master sets it only after the check the
master skill already gives (`git diff --no-renames --name-only origin/<base>...origin/<branch>`
per step, no file in common). busmon's Board row and the Plan view's task row (through its
`board` field) show `parallel: <reason>`, so the person sees every exception.

## 4. What this does not catch, said plainly

- A step never put in review on the board: an implementer that challenges the reviewers
  directly. The agent-bus skill already says to put a delivered branch in review. The
  2026-12-05 count (merge-conflicts §3) reports any PR whose branch no request recorded.
- Two different build requests whose steps touch the same files: that is the "two settings
  PRs" and "a move runs alone" cases, not this rule. Hot-files step 2 (Q2, answered yes,
  specified next) removes the settings case. The move case stays in the skill.
- The plan: `plan.Validate` is unchanged. A plan task per step is not how plans are written, and
  two plan tasks of one group are often unrelated (merge-conflicts §3, foureyes r4).

## 5. Skills

- `skills/agent-bus/SKILL.md`, beside "Put a delivered branch in review" (`:373`): one open step
  per request. While step N is in review, take another task. `request review` refuses step N+1
  until N's merge steps the request. If two steps share no file, ask master for
  `request parallel`. The text does not tell the agent to work around it.
- `skills/agent-bus-master/SKILL.md:749`: the rule reads "per build request" instead of "send
  N+1 once N merged". `request parallel <task> <reason>` goes only after the file check. The
  board shows it.

## 6. Build: one step, one PR

`bus/requests.go` (rule 1 in the transition script, its refusal listed), `bus/requestmerged.go`
and `internal/dispatchsvc/closeonmerge.go` (rule 2), `bus/parallel.go` (new script and
`SetRequestParallel`), `cmd/agentbus/requests.go` (`parallel` subcommand and help),
busmon's board row and the plan row (one label), the two skills, and one operator-note fragment
`docs/operator-notes.d/sibling-enforce.md`. No setting: the rule has no off switch beyond
`parallel`, by the person's answer.

## 7. Tests

On the disposable broker (`AGENTBUS_TEST_REDIS_URL`):

- Review B with `--more`, then resume and review C → refused, text names B and the sender.
  Review B again after a resume → passes. With `parallel` set → C passes. After `--off` →
  refused again.
- Without `--more`: review B `--more`, resume, review C (last step) → refused. Review with no
  `--branch` while B is open → refused; with `--branch B` → passes.
- Merge pass: B merged while the request is `accepted` (resumed) → `merged_steps` gets B's
  head, `branch` and `review_more` cleared, state still `accepted`. Then review C passes. In
  `review` → as today. A newer `review_n` than read → nothing written.
- `request parallel` by the target → refused. By the sender → set, audit line written. By the
  person (`--as`) → set. On a `done` request → refused. Missing reason → refused.
- Mutants, each must fail a test: drop the B ≠ S check (a fix review is refused); drop the
  `accepted` state from the merge pass (the step stays open); let the target set `parallel`.
- `TestNoStructWriterOfTheBoard` and `TestEveryScriptRefusalIsListed` still pass.

## 8. Decisions this touches

- merge-conflicts §3 (2026-11-20): its enforcement shape for sibling steps moves from
  `plan.Validate` to the board, for the reason in §1. The rule itself is unchanged.
- board-drift §3.3-§3.4 (close on merge): extended to a resumed `--more` step (§3 rule 2). No
  new writer: the dispatcher's fenced script remains the only one.
- wip-limit §3.2-§3.3: a resume is still never refused. A request in `review` is still not
  counted. Unchanged.
- The person's Q1 (2026-11-26) named `plan.Validate` in the recommendation they accepted. §1
  shows that check would have caught none of the 5 breaches. The person's goal (enforced now,
  with a `parallel` exception master sets and the person sees) is kept; only the place changes.
  Flagged to master, not put back to the person as a new question.
