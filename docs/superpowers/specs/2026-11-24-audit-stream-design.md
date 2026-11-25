# An append-only audit stream (audit)

Status: IDEA spec r2, for review (subject `audit-spec`). Not built. Base: origin/dev `af527c19`.
r2: reviewer-codex's reject of r1 (verdict `1795574727848-0`). `XADD` takes `MAXLEN` and `MINID`,
so any user allowed to append can also trim. An ACL that grants `XADD` cannot make the stream
append-only. r2 says the stream is a best-effort trail, with or without the ACL. A real boundary
needs one trusted writer, which is Q3's option C. `detail` is built from fixed fields, and
`health.Sensitive` is a best-effort net (§2, §3, §6).
Author: architect, from master's directive (cmd `1795559518593-0`, task `specs-from-person-docs`),
idea 1 of 4 from the person's note "ideas borrowed from PiBot". The note's reference is PiBot
`packages/storage/src/permissions/audit.ts` (Apache-2.0). It is used for the idea only. No code
is translated. No new dependency. The questions for the person are last (§6).

## 1. The need

Who approved, merged, pushed or forced what, and when? Today the answer is spread over the
verdict ledger, the swap ledger, the cmd stream, `{p}:ci`, the events stream and the forge.
Most of these are trimmed by length (`MAXLEN`), and `busmon --reset` clears the cmd and events
streams (`ResetKinds`, `bus/events.go:165`). So after a week, or after a reset, nobody can tell
who allowed a force push.

## 2. The idea

One stream per project, **`{p}:audit`**. It follows the `{p}:<kind>` naming, not the note's
`abm:audit`. Only one writer adds to it, `bus.Audit(ctx, AuditEntry)`, which only appends:

| Field | Content |
|---|---|
| `action` | One of a closed list (below). Any other value is refused. |
| `actor` | The agent or the person who acted. |
| `human`, `typed` | Copied from the triggering entry, when a person's entry caused the act. |
| `target` | What was acted on: a PR number, a branch, a task, an agent, or a version. |
| `ref` | The bus id that authorises the act: a verdict, an ask answer, or a cmd. |
| `outcome` | `ok`, `refused` or `failed`. |
| `detail` | At most 300 runes, with no control characters. The calling code composes it from fixed parts (a commit, a flag, a count, a reason code), never from free text it received. |

**The actions:** `approve` (a verdict), `answer` (an ask's answer), `merge` (the merge queue),
`push` and `deliver` (`agentbus deliver`), `force` (any verb run with `--force`), `ci-handoff`
(the dispatcher's CI step), `update` and `reinstall` (`agentbus update`, setup), `quota-pause`,
`pause`, `resume`, `suspend`, `retire`, and `retro-apply` (the retro spec, if it is built).

Each act's existing code path calls `bus.Audit` once, after the act, with its outcome. A call
the dispatcher makes goes into its existing fenced script, so it stays fenced and the audit
line is written in the same step. No new process watches the bus.

## 3. The rules

- **Redacted: the whole field, never a span.** Secrets are kept out by construction: no field
  carries free text (§2). `health.Sensitive` (`health/health.go:212`) is a second net, and its
  own comment says it is best effort: a secret in a shape it does not list passes. If it matches
  a field, that field is stored as `[redacted]`. A partial redaction leaves the rest of the secret in place (the
  lesson "a detection regex is not a redactor"). An audit line is still written, so that the
  act is not hidden.
- **Bounded by age, not length.** Every `XADD` trims with `MINID` at the server's `TIME` minus
  `audit.retention` (default 90 days, the note's figure). A burst of entries never pushes out a
  month-old approval.
- **It survives `busmon --reset`.** `audit` is not one of the `ResetKinds`. The suite's
  survive-or-reset test lists it as surviving. `busmon --delete` still removes it, with the
  project. Backups include it.
- **A trail, not a proof, with or without the broker ACL.** `XADD` itself trims: `MAXLEN 0` or
  a recent `MINID` on an append erases the history. So a user allowed to append can erase,
  whatever the ACL denies besides. In the ACL table (`bus/acl.go`), `XTRIM`, `XDEL` and `DEL` on
  `audit` go to `admin` only. That stops a careless trim, not a deliberate one. The spec claims
  no more than that, as the person-memory spec does for `human=yes`. Making the stream
  append-only against agents needs one trusted writer (§6 Q3, option C).

## 4. Reading it

- **`agentbus audit [--action a] [--actor x] [--since 7d] [--json]`** lists entries, newest first.
- **`agentbus audit export --since <t> > file.jsonl`** writes JSON Lines, one entry per line,
  already redacted. It is the note's optional export, and it runs only on request.
- **busmon:** an Audit list on the existing History page. It shows the last 200 entries, filtered
  by action. Enter shows the entry's `ref`. It is read-only.

## 5. Build and tests

**One step**, then a second PR that adds the call sites, one area per commit.

**Tests (the `bus` package, against the disposable Redis):**

- An unknown action is refused, and nothing is written.
- A token-shaped value in `detail` is stored as exactly `[redacted]`, and so is the whole field.
- `MINID` trimming drops an entry older than the retention and keeps a newer one. The test
  injects the time the trim reads.
- `busmon --reset`'s kinds leave `{p}:audit` whole.
- **Adversarial, under the ACL test harness:** an `agent` user's `XADD … MAXLEN 0` on
  `{p}:audit` is recorded as what it is. With option A or B it succeeds, and the test asserts
  that, so the stated limit stays true. With option C it is refused.
- `detail` call sites pass only fixed parts: a test feeds a token-shaped commit message to a
  call site and finds it absent from the entry.
- A fenced call without the lease writes neither the act nor the audit line.
- One test per call site checks that the act writes exactly one line, and that a refused act
  writes `outcome=refused`.

**Not doing:** no signing or hash chain (§6 Q3), no forwarding to an outside service, and no
audit of reads.

## 6. Questions for the person

**Q1. Retention.**
- **(A) recommended: 90 days**, the note's figure. It is a few thousand short entries.
- (B) 30 days.
- (C) forever, trimmed only by hand.

**Q2. Which acts go in the first build?**
- **(A) recommended: all of §2's list.** Each is one call after an act that already exists.
- (B) only approvals, merges, pushes and force. The rest comes later.

**Q3. How strong must it be?**
- **(A) recommended: a trail.** Best effort, stated as such. It answers "who did what" for an
  honest team, which is the need of §1.
- (B) a trail with a hash chain: each entry carries the previous entry's hash, and
  `agentbus audit verify` checks the chain. It detects an edit in the middle, but not a cut of
  the tail.
- (C) one trusted writer. Writers append to an inbox stream, `{p}:audit:in`. The dispatcher's
  ACL user, the only one with `XADD` on `{p}:audit`, copies each entry over in a fenced script
  and applies the retention itself. Agents then cannot trim it. Your own shell, as the same OS
  user, still could. It costs a new kind and a dispatcher step.
