# Feedback loop: what reviews say about our skills

Status: design, step 1 to build, step 2 a contract only. Plan v0.24, task `feedback-spec`.
Source idea: Bernard's note "boucle de feedback sur le bus" (October 2026), critiqued below.

## Why

Every review on this bus leaves a verdict in `{p}:verdicts`: who reviewed whom, approve or
reject, and why. Read together, those verdicts say where the team keeps stumbling (missing
tests, docs left behind, wording that claims too much), which is exactly what a role's skill
should teach. Today nobody reads them together. The loop has two steps:

1. **`agentbus feedback`**: read-only, deterministic statistics over what the bus retains. No
   model, no network, no cost, no write.
2. **Skill patches by proposal**: from a recurring finding, an agent may propose a patch to a
   skill as a normal pull request, only after the person says yes to an ask. Nothing ever edits
   a skill on its own.

Product direction: a small, simple, fast manager. Step 1 is one command and one pure package;
step 2 adds no code beyond what the bus already has (asks, directives, PRs, 4-eyes).

## What we keep from the source idea, and what we drop

| Source idea | Decision | Why |
|---|---|---|
| Use the bus traffic as the feedback signal, change skills (text), never model weights | **kept** | reversible, auditable, reviewed like any change |
| `agentbus feedback summarize` on demand | **kept**, as `agentbus feedback` | one verb, read-only |
| The person (or master) decides every change | **kept**, through an ask | the person's decisions are asks here (v0.14) |
| Read `:missions` and failed missions | dropped | mission state lives in hashes, not a stream, and this team rarely runs missions; reviews are where the signal is |
| Count `blocked`/`failed`/`needs human` reports, average duration per role | dropped | reports are free text; a duration read from them would be a guess. Time to approval is measured, from the ledger |
| Publish a `feedback_report` entry on the bus | dropped | a write, a new message type and noise in every agent's feed, for something the person reads once a week |
| `suggestions` in the report ("add a checklist to the coder skill") | moved to step 2 | writing a suggestion is a judgement: an agent's job, behind the person's yes, not a statistic |
| A busmon tab for feedback reports | not now | the CLI first; a page can read the same package later if the numbers prove useful |
| Cron or sentinel trigger | not now | on demand only; sentinel's daily review may run it later, read-only |
| Granularity per project, role or global | per project | one project is one ledger; `--project` as everywhere |

## Step 1: `agentbus feedback`

```
agentbus feedback [--since <dur>] [--json]
```

Read-only: it reads two keys and writes nothing, so the identity guard lets it run from any pane.
`--since` (default: everything retained) bounds the window by verdict time; `--json` prints the
same facts as one object. No other flag in v1.

### Data sources and their limits

| Source | What is read | Retention | Consequence |
|---|---|---|---|
| `{p}:verdicts` (stream) | every verdict: subject, author, reviewer, decision, message, ref, time (its stream id) | capped at `verdictMaxLen()` = 10 000 (`AGENT_BUS_VERDICT_MAX`), the oldest trimmed | the window starts at the oldest retained verdict; the output says that date, always |
| `{p}:cmd` (stream) | the `challenge` entries only: their ref, target (the reviewer) and time | capped at ~1 000 entries (`MAXLEN ~`), all message types together: on an active project, a few days | a time to verdict needs the challenge; when it was trimmed the figure is left out and counted ("12 of 40 measured"), never estimated |

Not read, on purpose: client transcripts (`~/.claude/projects/…`, Codex sessions: secrets,
size, and they belong to the clients), `{p}:report` text (free prose), environment and service
files, `docs/memory/journal/` (prose for people, not data). The "journals" of this loop are the
bus's own two streams above.

A verdict's `ref` is the challenge it answers (the challenge's `:cmd` entry carries the ref, not
the subject). That is the only join: a verdict without a ref, or whose challenge is no longer
retained, still counts in every figure but the times.

### What it computes

Self-verdicts (author = reviewer) are excluded everywhere, as the roll-up excludes them.

1. **Per reviewer**: verdicts, rejects, reject rate, and the median time from challenge to that
   reviewer's verdict (measured ones only, with their count).
2. **Per subject (a task)**: rounds, the review state now (`bus.RollUp`: approved, rejected,
   pending), rejects before the first approval, and the time to approval. *Rounds* = the largest
   number of verdicts one reviewer gave the subject (each re-challenge is one more verdict from
   each reviewer: redis-watch, rejected twice then approved by one reviewer, is 3 rounds).
   *Time to approval* = from the subject's first retained challenge to the verdict that made the
   roll-up APPROVED; absent when that challenge was trimmed or the subject is not approved.
3. **Recurring reject categories**: each reject's text (its `full` field when the ledger kept
   one, else the ≤500 preview) is matched against a small, fixed,
   versioned table of categories (below), several per reject allowed, `other` when none
   matches. Shown per category: count, the reviewers and authors involved, and the ids of the
   matching verdicts (never their text by default).
4. **Totals**: verdicts, subjects, approved, pending, and the window actually covered.

The category table lives in the package (`feedback/categories.go`), one line per category, a
few lower-case phrases each; a change to it is a reviewed change like any other. Starting set,
from this team's reviews in October 2026:

| Category | Matches, for example |
|---|---|
| `tests` | test, mutant, survivor, coverage, assert |
| `docs` | doc, readme, claude.md, comment, guide, changelog |
| `wording` | wording, message, help text, says, claims |
| `scope` | out of scope, scope, unrelated, beyond |
| `security` | secret, credential, token, password, inject, traversal |
| `concurrency` | race, lock, concurrent, overlap, deadlock |
| `edge-case` | empty, missing, nil, zero, boundary, offline |

A keyword table is crude, and the output says so (`categories: keyword heuristic, table v1`).
It is deterministic, costs nothing and is easy to correct; if it proves too noisy, the next step
is an optional `--tag <category>` on `agentbus verdict` (the reviewer names it), not a model.

### Output

Text by default, in the CLI's usual plain columns, widest first:

```
feedback: agent-bus-monitor, verdicts since 2026-10-20 (oldest retained), 214 verdicts, 63 subjects
categories: keyword heuristic, table v1

reviewers        verdicts  rejects  rate   median to verdict
reviewer-codex        98       21   21%    6m (71 of 98 measured)
foureyes             116       12   10%    9m (80 of 116 measured)

subjects                 rounds  state      rejects  to approval
redis-watch                   3  approved         2  1h12m
installer-default             4  approved         3  1h05m
busmon-plan-start-ux          1  approved         0  14m
…

recurring rejects (rejects 33, matched 29)
tests          11  reviewer-codex 7, foureyes 4 · authors coder 11 · 1793250…-0, …
wording         8  reviewer-codex 8 · authors coder 8 · …
other           4  …
```

`--json`: one object with the same sections (`window`, `reviewers`, `subjects`, `categories`,
`totals`, `table_version`). Like the other read commands' JSON it is additive by key and not
versioned (only `subscribe` is). A long list is capped in text (20 subjects, most rounds first,
then `+ N more`) and whole in JSON.

### Shape of the code

- Package `feedback/`, pure: `Compute(verdicts []bus.Verdict, challenges []Challenge, since, now
  time.Time) Report`, plus the category table. No Redis, no clock, no network.
- `cmd/agentbus/feedbackcmd.go`: reads the ledger (`XRANGE`, bounded by the cap) and the
  `challenge` entries of `:cmd`, calls `Compute`, prints. Its row in `commands.go`, so the
  command and flag table tests cover it. Above nothing special: it needs a project, like
  `verdicts`.

## Step 2: skill patches by proposal (a contract, not built now)

1. **Who notices.** Master (or the person) runs `agentbus feedback`, say weekly, or when a
   subject needed many rounds. A category with at least 3 rejects against one author role in the
   window is a candidate.
2. **The person decides.** Master opens an ask (`agentbus ask`): "N rejects in `tests` against
   coder since <date> (verdict ids). Propose a patch to coder's skill?" with options yes / not
   now / never for this category.
3. **An agent proposes.** On yes, master sends a directive to an agent (the docs agent, or coder)
   to write the smallest patch to **our** skill (`skills/<name>/SKILL.md`, or the role prompt in
   `roles/`): a checklist line, an example, a rule. A skill from outside (`mattpocock/skills`) is
   never patched; the patch adds a line to our own role skill instead.
4. **The normal path.** The patch is a branch and a PR like any change: 4-eyes, then the person
   merges. Agents pick it up through the usual skill change notice (`agentbus roles skill-users`).
5. **Never.** No process edits a skill, a prompt or `roles.toml` without that PR; `agentbus
   feedback` itself writes nothing, opens no ask and sends nothing.

Step 2 needs no new code: asks, directives, PRs and reviews exist. If it is used, a later change
may let `agentbus feedback --candidates` print the step-1 candidates in the ask's words.

## Security

- **Read-only, local.** Two `XRANGE` reads on the project's broker; no write, no network, no
  model call. The package imports no network library (a test asserts it).
- **Text that leaves the ledger.** Verdict messages are agent-written and could quote anything an
  agent saw, a secret included. By default the output carries counts and verdict ids, never a
  message. A later `--examples` (not in v1) would print at most 120 characters per verdict,
  each passed through the same credential-shape filter health uses (`health.SafeText`), and
  dropped rather than printed when it fails.
- **No new sources.** No transcript, no environment or service file, no `.env`, no credential.

## Out of scope

Step 2 as code; a busmon page; a bus message or any write; a model, embeddings or any network
call; costs (by rule); missions, reports and transcripts as sources; cross-project totals (run it
per project); scheduling (sentinel may run it later, read-only).

## Tests

- `feedback` unit tests over hand-built verdicts and challenges: self-verdicts excluded from every
  figure; reject rate; rounds = most verdicts by one reviewer; rejects before the first approval;
  time to approval from the first retained challenge, absent when it was trimmed, absent when not
  approved; the median to verdict and its "n of m"; `--since` bounds by verdict time; the window
  starts at the oldest retained verdict; the roll-up agrees with `bus.RollUp`.
- Categories: each table row matched by its phrases, case-insensitive; several per reject;
  `other`; approves never categorised; the table version printed.
- Output: text columns and the 20-subject cap with `+ N more`; JSON keys; no verdict message in
  either output (a fixture with a fake credential in its message proves it).
- No network: a test lists the package's imports and refuses `net`, `net/http`.
- Read-only: the command's reader is an interface with read methods only; one integration test on
  the disposable test broker (`AGENTBUS_TEST_REDIS_URL`, never the live one) writes verdicts and
  challenges, runs the command, and checks that the keyspace is unchanged afterwards.
- Mutants on the rules above before review, as usual.
