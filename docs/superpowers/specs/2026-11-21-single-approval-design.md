# One approval for a small change (single-approval)

Status: spec r3, for review (subject `single-approval`; r3, the person via master (cmd `1795282533075-0`, task `single-approval-r2`; §4.1 now states §4.8's exception, reviewer-codex): a project with one reviewer, or reviewers of one provider only, deadlocks under §4.1; §4.8 adds the person's `delivery.same_provider_ok = off | docs | small` and busmon's warning; r2 merged as #721: the trace is the deliverer's report; busmon counts current distinct approvers from a new `MergeItem.Approvers`, not the raw approvals; Q3 covers both limits too (reviewer-codex); §4.4 and Q8 on an earlier reject by the other reviewer (foureyes)). Not built. Base: origin/dev `2a7e0360`.
Author: architect, from the person's directive relayed by master (cmd `1795262117698-0`, task
`single-approval-spec`). This **loosens a safety gate**: §2 says what is lost, with the ledger's
numbers, before the design.

## 1. Today

`agentbus deliver` (`cmd/agentbus/deliver.go`) delivers a branch only when:

1. the subject's roll-up is `APPROVED` (`bus.RollUp`) and its deciding approve names the
   branch head `H`; and
2. **two** reviewers are `currentApprovers(vs, H)`: each one's *latest* verdict on the subject is
   an independent approve (reviewer ≠ author) naming `H` (`deliver.go:357`).

The one way past the second rule is `--single-approval <reason>`: the emergency. Its reason is
written in the PR body (`deliver.go:383`) and traced on the bus as the delivering role's report
(`Trace` → `b.Report(ctx, self, …)`, `deliver.go:124`; master or docs may deliver). Nothing checks the size or the paths of the change.

The merge queue (`mergequeue/derive.go`) lists a PR whose subject rolls up `APPROVED` on its head,
with its approvals (`bus.MergeItem.Approvals`), so busmon can already count them.

`bus.RollUp` lets an approve that comes **after** a reject win (`bus/verdicts.go:21`). Today the
two-approver rule hides this: a reviewer whose latest verdict is a reject is not an approver.
Under one approval it would not be hidden (§4.3).

## 2. What is lost: the ledger over 40 hours

Source: `agentbus history --kind verdict --since 2d` (ids `1795116736392-0` to
`1795261562816-0`). For each subject and head, both reviewers' verdicts; for the size, `git diff
--no-renames --numstat <merge-base with origin/dev> <head>`, classified with §3.2's globs. This is
architect's measure, on the live bus.

| | heads both reviewers judged | one approved, the other rejected the same head |
|---|---|---|
| all | 142 | 43 (30 %) |
| small by §3.2 (≤150 lines and ≤5 files, or docs-only at any size; no never-small path) | 54 | **19 (35 %)** |
| of which docs-only | 43 | 17 (40 %) |
| small if docs must also fit the limits (Q2) | 25 | 8 (32 %) |

In the 19 small ones, **foureyes approved and reviewer-codex rejected 16 times**; the reverse
happened 3 times. Examples:

- the v0.31.0 release notes: five heads in a row, each with a false claim that codex found;
- the language-setting spec, merge-conflicts r4 (the `--no-renames` hole), conflict-owner r1;
- broker-acl-4b-spec (twice);
- `runtest-timeout-flake` (31 lines): an unreadable `/proc` state codex found.

Outside "small": `broker-acl-inventory` (`6584e4ac`), approved by foureyes, rejected by codex
(CLI entry points misclassified as privileged roles). It touches the ACL, so it stays
never-small (§3.2). `busmon-delete-safety` (`3538d7bb`) was rejected by **both**, so one
approval would have stopped it too.

So the number that matters is not the size: **docs-only changes are where the reviewers disagree
most**, and most often it is codex catching a false claim that foureyes passed. If either
reviewer's approval counted alone, all 19 would have been deliverable with the defect the other
reviewer found (§4.1 narrows it to 3).

## 3. The rule

### 3.1 Settings (per project, Shift+C)

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `delivery.single_approval` | `off` \| `small` | `off` | `small`: a change §3.2 calls small needs one approval (§4) |
| `delivery.small_max_lines` | Int | 150 | added + deleted lines, over the merge-base diff |
| `delivery.small_max_files` | Int | 5 | changed paths, a rename counted as two |
| `delivery.never_small` | List | `""` | globs **added** to the defaults of §3.2; never removes one |
| `delivery.same_provider_ok` | `off` \| `docs` \| `small` | `off` | which small changes a same-provider approval may deliver alone (§4.8); **the person's only** |

All five are `Project` keys (a value per project, `[project.<name>]`). They are read by
`agentbus deliver` and shown in busmon. Who may write the three that widen the gate
(`single_approval`, `small_max_lines`, `small_max_files`) is Q3. `never_small` only narrows it.
`same_provider_ok` is `PersonOnly` whatever Q3 answers (§4.8).

### 3.2 Small, decided by code

`deliver` computes it from what it is about to deliver:

```sh
mb=$(git merge-base <baseRef> <H>)
git diff --no-renames --numstat "$mb" "<H>"
```

- **lines** = the sum of added + deleted; **files** = the number of rows. A binary row (`-`)
  makes the change not small.
- **small** when no path matches a never-small glob, and either:
  - lines ≤ `small_max_lines` and files ≤ `small_max_files`; or
  - every path is documentation: `docs/**` or `*.md`, at any size (the person's "spec/docs-only").

**Never small, whatever the size** (defaults in code; the setting only adds):

| Class | Globs |
|---|---|
| protocol | `bus/**` |
| broker, ACL | `docker-compose.yml`, `internal/brokerrt/**`, `**/*.acl`, `setup/redis.go`, `setup/rotate.go` |
| setup, update, rollback | `setup.sh`, `setup/**`, `scripts/bootstrap`, `update/**`, `cmd/agentbus/updatecmd.go`, `backup/**` |
| secrets | `health/health.go` (`Sensitive`, `Redact`), `internal/secretpath/**`, `trust/**` |
| CI | `.github/workflows/**`, `.gitlab-ci.yml`, `bitbucket-pipelines.yml` |
| skills | `skills/**` (a path cannot tell a safety rule from other text; Q4) |
| the gate itself | `cmd/agentbus/deliver.go`, the classifier's file, `mergequeue/**`, and `config/keys.go` |

The last row matters most. Without it, a "small" PR could raise `small_max_lines`, empty the
defaults or weaken the classifier, and then be delivered on one approval. `config/keys.go` is
there because the four keys' defaults live in it; that makes every settings PR need two
approvals (Q5).

## 4. Delivering on one approval

### 4.1 Which reviewer counts

**A reviewer of a different provider from the author** (the ledger records both:
`Verdict.AuthorProvider`, `ReviewerProvider`, `bus/stream.go:188`). Today that is
reviewer-codex for every Claude author. With no such approver, two are needed: always when
`delivery.same_provider_ok` is `off` (the default), and otherwise for every change outside its
class. The one exception is §4.8: with `single_approval = small` and `same_provider_ok` set, a
same-provider approval alone delivers a small change of that class (`docs`: docs-only; `small`:
any small change). A provider left empty
on either side (an agent with no known client) is never "another provider".

Why not "either" or "foureyes first": §2's 16 of 19. A same-provider approval alone is the case
that let the defects through. The rule names no agent, so it holds when roles change. Q1 asks it.

### 4.2 The exact head

As today: the approve must name `H`, and it must be its reviewer's latest verdict on the
subject. Any later push is a new `H` with no approve, so it needs a new approval.

### 4.3 An open objection blocks

`deliver` refuses one approval when **any** reviewer's latest verdict on the subject is a reject
(`reject` or `changes`), on any head. That closes §1's `RollUp` gap: codex rejects `H`, foureyes
approves `H` after it, and the roll-up says `APPROVED`. With two approvals that cannot pass;
with one, without this rule, it would.

### 4.4 The other reviewer

Under `small`, master (or docs, for its own PR) challenges only the counting reviewer. If the other one was challenged
too, its gate stays open and its review goes on. A **later** reject makes the subject roll up
`REJECTED`, so the PR leaves the merge queue (`mergequeue.candidates` keeps only `APPROVED`). That
happens before the person merges, because For you lists only the queue.

**An earlier objection still blocks** (foureyes). §4.3 reads every reviewer's latest verdict. If
the other reviewer rejected an earlier head and was not challenged on the fix, its latest verdict
is still that reject. The fix then needs that reviewer's approve too: two approvals in practice.
So `small` saves a review only where the other reviewer never objected. Recommended: keep it (an
objection is answered by the one who raised it). The alternative is Q8.

### 4.5 What `deliver` records

- The PR body: `**single approval (small: 42 lines, 2 files; approved by reviewer-codex)**`.
- The bus: the same line, traced like the emergency (`Trace`).
- `--dry-run` prints the classification: small or not, lines, files, and the first
  never-small path that matched.

busmon's merge row and page show **"1 approval"** when the item's head has one current approver.
`MergeItem.Approvals` cannot tell that (reviewer-codex): `mergequeue.candidates` keeps every
independent approve naming the head, a reviewer's stale or repeated ones included. Example: A
approves H, then H2; B approves H. The roll-up decides on H, and its current approvers are B
alone. But `Approvals` holds A's and B's, so its length is 2. So:

- `currentApprovers` moves from `cmd/agentbus/deliver.go` to `bus` (`bus.CurrentApprovers`), so
  `deliver` and the merge queue count the same way;
- `MergeItem` gets `Approvers []string`: the head's current distinct approvers. It is additive, and
  `Approvals` stays.

The reason is in the PR body and the trace, and the page shows the trace's line.

### 4.6 Above the threshold

Two approvals, as today. Or the person decides: master opens an ask, and on the person's yes
delivers with `--single-approval "<the person's yes, ask <id>>"`, the existing emergency. Should
`deliver` check that ask (answered, by the person, yes)? Recommended: not now (Q6).

### 4.7 The refusal message

```
deliver: <subject>@<H> has 1 approval; one is enough only for a small change by a reviewer of
another provider: not small (163 lines > 150), or: bus/stream.go is never small. Two approvals, or
the person's yes (--single-approval).
```

Under §4.8, a same-provider approval refused for its class says so: `… approved by foureyes, of
the author's provider: delivery.same_provider_ok is docs, and this change is not docs-only.`

### 4.8 A project with one reviewer, or one provider (r3)

**The gap** (the person). `morning-check` has foureyes alone, of the author's provider. Under
§4.1 one approval never counts there, and two approvals cannot be had: the deadlock this spec
set out to remove.

**The setting**, per project, the person's only:

| `delivery.same_provider_ok` | A same-provider approval counts alone for |
|---|---|
| `off` (default) | nothing: §4.1 as written |
| `docs` | a small change (§3.2) whose every path is documentation (`docs/**`, `*.md`) |
| `small` | any small change (§3.2): docs, and code such as Ansible roles or scripts, within `small_max_lines` and `small_max_files` |

**How it composes with `delivery.single_approval`.** `single_approval` says whether one approval
can ever be enough, and for what size; `same_provider_ok` only widens *whose* approval may be
that one. So:
- with `single_approval = off`, `same_provider_ok` has no effect. Shift+C shows it with "no
  effect: delivery.single_approval is off". A project like `morning-check` sets both
  (`single_approval = small`, `same_provider_ok = docs` or `small`);
- with `single_approval = small`, one approval delivers when the change is small (§3.2) and the
  approver is of another provider (§4.1), or of the same provider and the change is in
  `same_provider_ok`'s class.

Every other rule stays:
- **never-small paths always need two approvals**, whatever the value (§3.2). In a one-reviewer
  project such a change is delivered only on the person's yes (§4.6, `--single-approval`);
- the approver is independent (reviewer ≠ author), on the exact head (§4.2), and no reviewer's
  latest verdict on the subject is a reject (§4.3);
- a different-provider approval, where one exists, is still preferred: `deliver` names it when
  both kinds are present.

**Person-only.** The key is `PersonOnly` and `Project`: read from `config.toml`'s
`[project.<name>]` table only, never from an env variable, and `agentbus config set` refuses it,
as it does `backup.after_*` (`config/keys.go:77`, `config/write.go:60`). Today every `PersonOnly`
key is machine-wide; a `PersonOnly` key with a per-project value is new, and its test says so.
busmon shows it, not editable, like the other `PersonOnly` rows.

**What `deliver` records**, as §4.5, with the provider named:
- the PR body: `**single approval, same provider (small docs: 42 lines, 2 files; approved by foureyes)**`;
- the bus: the same line, traced (`Trace`);
- `--dry-run` prints which rule would apply: another provider, same provider (`docs` or
  `small`), or none.

**busmon's warning: no reviewer of another provider.** Over the project's verdict ledger of the
last 7 days, when there is at least one independent verdict and none whose `ReviewerProvider` is
set and differs from its `AuthorProvider`, busmon shows one line on the Shift+C delivery rows and
on a merge item's page:

> `no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)`

The value shown is the project's current one; with `docs` or `small` the line still shows, as
information, without "needs". A project with no verdict in 7 days shows nothing. The ledger is
the source because it records the providers of the agents that actually reviewed
(`verdictProviders`, `cmd/agentbus/verdicts.go:142`); the roles file says nothing about who
reviews.

## 5. Steps (in series)

1. **The classifier** (pure, no setting yet): `deliver.classify(numstat, globs, max)` → small,
   docs-only (for §4.8), lines, files, matched path; `--dry-run` prints it. Tests:
   - a 150-line, 5-file change is small, and 151 lines or 6 files is not;
   - a 2 000-line `docs/` change is small;
   - `bus/x.go` with 1 line, `skills/a/SKILL.md`, a binary file and `config/keys.go` are not;
   - a rename counts two files;
   - a never-small glob added by the setting applies, and an empty setting keeps the defaults.
2. **The settings and the rule** in `deliver` (§3.1, §4.1–§4.5). Tests:
   - `off` keeps today's behaviour;
   - `small` with a different-provider approve on `H` delivers, records the line and traces it;
   - a same-provider approve alone is refused;
   - a reject by the other reviewer on any head is refused, even with a later approve;
   - a push after the approve is refused;
   - not small is refused with §4.7's message;
   - the emergency flag still works;
   - a reviewer who approved H and then H2 does not count for H;
   - §4.8: with `same_provider_ok = off`, a same-provider approve alone is refused; with `docs`,
     it delivers a small docs-only change and refuses a small code change; with `small`, it
     delivers both; with `single_approval = off`, any `same_provider_ok` changes nothing; a
     never-small path is refused under every value; an empty provider on either side is not
     "another provider"; the PR body and the trace say "single approval, same provider";
   - `same_provider_ok` read from `[project.<name>]` in `config.toml` only: an env variable is
     not read, `agentbus config set` refuses it, and another project's value does not leak.
3. **The merge queue and busmon**: `bus.CurrentApprovers` used by `mergequeue` for
   `MergeItem.Approvers`; the Shift+C rows; "1 approval" on the merge row and page; §4.8's
   warning. Tests: §4.5's example shows "1 approval", and two current approvers show none; a
   ledger with only same-provider verdicts in 7 days shows the warning, one different-provider
   verdict hides it, and an empty ledger shows nothing.

## 6. Questions for the person

1. **Which approval counts alone?** Recommended: only a reviewer of another provider than the
   author (today reviewer-codex for Claude authors). In §2, foureyes alone would have let 16 of
   19 defects through on small changes.
2. **Docs-only at any size?** It is where reviewers disagree most (17 of 43 small docs-only heads).
   Recommended: docs-only counts as small only within the same line and file limits, not at any
   size. The alternative is your text as written.
3. **Who may set `delivery.single_approval`, `small_max_lines` and `small_max_files`?** An agent
   could run `agentbus config set` itself and loosen its own gate: switch it on, or raise a
   limit so a larger change counts as small. Recommended: you only, for all three, from busmon's
   Shift+C or `config.toml`, the way `backup.after_*` is `PersonOnly`. Agents' `config set` is
   refused. `never_small` only narrows the gate, so anyone may add to it.
4. **Skills: all of `skills/**` never small, or only some?** A path cannot tell a safety rule
   from other skill text. Recommended: all of it.
5. **`config/keys.go` never small?** Needed so a small PR cannot raise its own limits; the cost
   is that every settings PR needs two approvals. Recommended: yes. The alternative is to move
   the four keys' defaults to a file of their own and list only that file.
6. **Above the threshold, should `deliver` check your yes on an ask?** Recommended: not now. The
   emergency flag with the ask's id in its reason, traced on the bus, is enough.
7. **Defaults: 150 lines and 5 files?** In §2, 11 of the 54 small heads were code; the rest were
   docs. Recommended: yes.
8. **An earlier reject by the other reviewer (§4.4).** Should it still block one approval on a
   later head? Recommended: yes, until that reviewer approves. The alternative: only a reject
   on the current head blocks, so a fix can go out on the counting reviewer alone.
