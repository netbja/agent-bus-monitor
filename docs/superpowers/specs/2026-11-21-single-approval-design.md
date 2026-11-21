# One approval for a small change (single-approval)

Status: spec r1, for review (subject `single-approval`). Not built. Base: origin/dev `570c16be`.
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
written in the PR body (`deliver.go:383`) and traced on the bus as master's report (`Trace`,
`deliver.go:124`). Nothing checks the size or the paths of the change.

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

All four are `Project` keys (a value per project, `[project.<name>]`). They are read by
`agentbus deliver` and shown in busmon. Who may write them is Q3.

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
reviewer-codex for every Claude author. With no such approver, small or not, two are needed.

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

Under `small`, master challenges only the counting reviewer. If the other one was challenged
too, its gate stays open and its review goes on. A **later** reject makes the subject roll up
`REJECTED`, so the PR leaves the merge queue (`mergequeue.candidates` keeps only `APPROVED`). That
happens before the person merges, because For you lists only the queue.

### 4.5 What `deliver` records

- The PR body: `**single approval (small: 42 lines, 2 files; approved by reviewer-codex)**`.
- The bus: the same line, traced like the emergency (`Trace`).
- `--dry-run` prints the classification: small or not, lines, files, and the first
  never-small path that matched.

busmon's merge row and page show **"1 approval"** when `len(MergeItem.Approvals) == 1`. No new
field: the reason is in the PR body and the trace, and the page shows the trace's line.

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

## 5. Steps (in series)

1. **The classifier** (pure, no setting yet): `deliver.classify(numstat, globs, max)` → small,
   lines, files, matched path; `--dry-run` prints it. Tests:
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
   - the emergency flag still works.
3. **busmon**: the Shift+C rows, and "1 approval" on the merge row and page.

## 6. Questions for the person

1. **Which approval counts alone?** Recommended: only a reviewer of another provider than the
   author (today reviewer-codex for Claude authors). In §2, foureyes alone would have let 16 of
   19 defects through on small changes.
2. **Docs-only at any size?** It is where reviewers disagree most (17 of 43 small docs-only heads).
   Recommended: docs-only counts as small only within the same line and file limits, not at any
   size. The alternative is your text as written.
3. **Who may set `delivery.single_approval`?** An agent could run `agentbus config set` itself
   and loosen its own gate. Recommended: you only, from busmon's Shift+C or `config.toml`, the
   way `backup.after_*` is `PersonOnly`. Agents' `config set` is refused.
4. **Skills: all of `skills/**` never small, or only some?** A path cannot tell a safety rule
   from other skill text. Recommended: all of it.
5. **`config/keys.go` never small?** Needed so a small PR cannot raise its own limits; the cost
   is that every settings PR needs two approvals. Recommended: yes. The alternative is to move
   the four keys' defaults to a file of their own and list only that file.
6. **Above the threshold, should `deliver` check your yes on an ask?** Recommended: not now. The
   emergency flag with the ask's id in its reason, traced on the bus, is enough.
7. **Defaults: 150 lines and 5 files?** In §2, 11 of the 54 small heads were code; the rest were
   docs. Recommended: yes.
