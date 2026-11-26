# Sibling steps run in series, enforced on the board

Status: spec r2, for review (subject `sibling-enforce-spec`; r2, foureyes' reject of r1: a step the merge pass never judges stayed open for ever. The open step is now judged by `request review` itself with the dispatcher's `Delivered` test, and an abandoned step is closed by the sender or the person). Not built. Base: origin/dev `d3bc5568`.
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

**One open step per request.** A request has an **open step** S when `review_more` is true and
`branch` is set, in any state but `done`. That is all the board records. Whether S is merged is
not read from the board: it is judged when the rule is applied (rule 1), so no pass the
dispatcher does or skips can leave S open for ever (r2, foureyes: the merge pass skips a step
with no `review_base`, a tip ahead of the recorded commit, or a re-review whose base already
holds the head, `closeonmerge.go:81-115`).

1. **`request review` judges the open step itself, then refuses a second one.** When the review
   names a branch C, the request has an open step S ≠ C, and the request is not `parallel`, the
   CLI (in the agent's checkout, where it already runs git for `reviewFacts`,
   `cmd/agentbus/requests.go:93-101`) fetches the project base and asks whether S is delivered.
   It uses the dispatcher's own test, `mergequeue.Delivery.Delivered` (`mergequeue/delivered.go:76`:
   ancestor or same tree, squash patch-id, rebase patch-ids). It asks for two heads: S's recorded
   `commit` and S's remote tip if the branch still exists. Either one delivered means S is merged.
   That covers a tip pushed after the review and a branch deleted after its merge.
   - **S delivered:** the CLI passes `step_merged = S, head, base tip` with the review. The
     script, in the same call, checks the request still has open step S with the `review_n` the
     CLI read. It appends the head to `merged_steps` (as `requestMergedScript` does) and writes
     one audit line (`action=step-merged`, by the agent, with head and base tip). Then it
     records C's review as today. The board changes only if S is still the open step.
   - **S not delivered:** refused. One `redis.error_reply`, listed for
     `TestEveryScriptRefusalIsListed`:
     `step S of <task> is not merged yet: sibling steps run in series (merge-conflicts §3); take
     another task, or ask <from> for agentbus request parallel <task>`
   - **No base to judge against** (the project has no delivery base: the CLI already records
     nothing and refuses nothing then, `requests.go:95-97`) or a failed git read: refused with
     the git error. Nothing is written. A base nobody chose leaves no open step (`review_base`
     is not recorded), and the rule does not apply.
   - **`--branch` is required while a step is open.** Today the script keeps the old branch
     when none is given (`bus/requests.go:468`) and the CLI records the checkout's HEAD as the
     commit. A new step reviewed without `--branch` would then pass as S. Refusal:
     `a step is open on S: name the branch (--branch S for S itself)`.
   - A review of S itself (a fix after a reject, a rebase) passes, as today. A review without
     `--more` on a new branch while S is open is judged the same way: the last step still
     follows the one before it.

   The judgement runs in the agent's process: the bus cannot run git, and the dispatcher never
   runs repository code. The CLI is the honest path, not a boundary. The audit line names the
   head and the base tip it used, and the 2026-12-05 count reads them back.

2. **An abandoned step is closed by the sender or the person.** A step whose PR is closed
   unmerged would stay open. `agentbus request step <task> --abandon <reason...>`, by the
   request's sender (`from`) or the person (`--as <person>`), never the target, clears the open
   step (`branch`, `commit`, `review_more`, `review_base`). It appends
   `{branch, head, reason, by, at}` to `abandoned_steps`. State: a request in `review` goes back
   to `accepted`, otherwise unchanged. One audit line (`action=step-abandoned`). The target asks
   master for it. This is rare: in the window, the steps closed unmerged were redeliveries of
   the same step, which keep the same open step.

3. **`request accept` is unchanged.** A resume stays allowed. The agent may fix its step and
   work on anything else meanwhile. Coding the next step locally is not refused (nothing can
   see it). Putting it in review is. The reviewers and `deliver` start from that review.

4. **The merge pass is unchanged.** It still steps a request in `review` (board-drift §3.4).
   Rule 1 no longer depends on it. A resumed step that the pass never saw is judged at the next
   review, so today's stale `resource-limits-build` row (accepted, `review_more` on
   `reslimits-2b`, merged as #812) passes at coder2's next step review.

**The `parallel` exception (the person's Q1).** `agentbus request parallel <task> <reason...>`
sets `request.parallel = {by, reason, at}` (`at` from Redis `TIME`). `request parallel <task>
--off` clears it. Who may set it: the request's sender (`from`, master in practice) or the person
(`--as <person>`, as `request priority` does). The target is refused: the implementer does not
lift its own rule. A `done` request is refused. One script on the board, as `requestPrioritySrc`
(`bus/priority.go:120`). It writes no stream entry and one audit line (`action=parallel`). While
it is set, rule 1 does not apply to that request. Several steps may then be open, and the
request records only the newest branch, as today. When `parallel` is cleared, the newest one is
the open step that rule 1 judges.

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

`bus/requests.go` (rule 1's script part: the `step_merged` close, the two refusals, listed),
`cmd/agentbus/requests.go` (rule 1's judgement with `mergequeue.Delivery`, the `parallel` and
`step --abandon` subcommands and help), `bus/parallel.go` (the `parallel` and abandon scripts),
busmon's board row and the plan row (one label), the two skills, and one operator-note fragment
`docs/operator-notes.d/sibling-enforce.md`. `requestmerged.go` and `closeonmerge.go` are not
touched. No setting: the rule has no off switch beyond `parallel`, by the person's answer.

## 7. Tests

On the disposable broker (`AGENTBUS_TEST_REDIS_URL`), with a scratch git repository as the base
(the `reviewFacts` tests' pattern):

- Review B `--more`, resume, review C while B is not in the base → refused, text names B and
  the sender. Review B again → passes.
- B squash-merged into the base, then review C → passes: `merged_steps` holds B's head, one
  `step-merged` audit line, C recorded. The same after B's branch is deleted (recorded commit
  judged). The same when B's tip was pushed after its review (the tip judged). The same when
  the request is `accepted` (resumed) or in `review`.
- foureyes' r1 paths, each judged at review: no `review_base` (no base: nothing open), a tip
  ahead of the commit, a re-review after the merge whose `review_base` holds the head (the
  pass skips it; rule 1 does not).
- A `review_n` changed between the CLI's read and the script → refused, nothing written.
- Review with no `--branch` while B is open → refused. With `--branch B` → passes.
- `parallel` set → C passes without a judgement. `--off` → C's next review judged again.
- `request step --abandon`: by the target → refused. By the sender → open step cleared,
  `abandoned_steps` and audit written, `review` → `accepted`. By the person (`--as`) → same.
  Missing reason → refused.
- `request parallel`: by the target → refused. By the sender, or the person → set, audit line
  written. On a `done` request → refused. Missing reason → refused.
- Mutants, each must fail a test: drop the S ≠ C check (a fix review is refused); judge only the
  recorded commit (the tip-ahead case is refused); skip the `review_n` check; let the target set
  `parallel` or abandon.
- `TestNoStructWriterOfTheBoard` and `TestEveryScriptRefusalIsListed` still pass.

## 8. Decisions this touches

- merge-conflicts §3 (2026-11-20): its enforcement shape for sibling steps moves from
  `plan.Validate` to the board, for the reason in §1. The rule itself is unchanged.
- board-drift §3.3-§3.4 (close on merge): unchanged. Rule 1 closes a merged step itself, at
  the next review, through the same board script that records it. It is an agent's own
  transition, not an automated write, so no fencing is involved.
- wip-limit §3.2-§3.3: a resume is still never refused. A request in `review` is still not
  counted. Unchanged.
- The person's Q1 (2026-11-26) named `plan.Validate` in the recommendation they accepted. §1
  shows that check would have caught none of the 5 breaches. The person's goal (enforced now,
  with a `parallel` exception master sets and the person sees) is kept; only the place changes.
  Flagged to master, not put back to the person as a new question.
