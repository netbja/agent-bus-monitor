# Verdict Ledger — Design Spec

- **Date**: 2026-06-30
- **Status**: Approved (brainstorm), ready for implementation plan
- **Scope**: Slice 1, CLI-only (`agentbus` + `bus` package). No busmon changes.

## Problem

The 4-eyes review guarantee is the central design invariant of this project, but
today it lives in prose. Concretely:

1. **No queryable verdict registry.** `bus.ResolveChallenge` does `HDEL` on the
   gate hash, so the moment a reviewer approves, the audit line
   (`ref → "<challenger>|<summary>"`) is **deleted**. The gate hash
   (`{p}:gate:{agent}`) answers only "what is still open", never "what was
   decided". There is no way to mechanically answer *"what is the 4-eyes state of
   PR #25?"* — it is reconstructed by eye from status/cmd prose.
2. **Two review paths that don't converge.** The protocol already has
   `challenge → reply → verdict` (correlated by `ref`), but it only engages if the
   author opens a `challenge`. When a peer instead pushes "review my commit" as a
   plain `cmd` (a `CmdDirective`, no `ref`, no gate), the reviewer's APPROVE goes
   back as free-text `cmd` and the auditable machinery is bypassed.

This spec adds a durable, queryable verdict ledger and makes recording a verdict
**subject-first** so it captures both paths.

What this spec does **not** address (acknowledged, out of scope): ack/TTL on
directives, agent identity leases, protocol versioning, and busmon surfacing.
Those are tracked as follow-ups.

## What already exists (do not rebuild)

- `bus.Cmd` already carries `type` (`directive|challenge|reply|verdict`) and a
  `ref` correlation field.
- CLI verbs `challenge` / `reply --ref` / `verdict --ref` already thread by `ref`.
- `{p}:gate:{agent}` is a registry of **open** challenges only.

The change is therefore mostly additive: persist verdicts in a new stream, add a
read command, and loosen the `verdict` write command to be subject-first.

## Design decisions (locked during brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Ledger scope | Verdicts only (not full review thread, not all cmd traffic) |
| 2 | What a verdict is | A **fact about a subject** — recordable even with no prior challenge; resolves a matching gate only as a bonus |
| 3 | What `verdicts` returns | A **computed roll-up status** (APPROVED/REJECTED/PENDING) + the supporting entries; exit code reflects state |
| 4 | Self-approval (reviewer == author) | **Recorded** for audit, but **never counted** toward APPROVED by the roll-up |
| a | Storage cap | `{p}:verdicts` stream capped at **10 000** (vs 1 000 elsewhere); override via `AGENT_BUS_VERDICT_MAX` |
| b | Exit codes | `0`=approved, `2`=pending, `3`=rejected; `1` stays reserved for usage/errors |
| c | Author argument | `author` positional is **required** on a verdict (needed to evaluate independence) |

## Data model & storage

### Stream

A new per-project stream `{p}:verdicts`, written with `XADD MAXLEN ~ <cap>`
(approximate trim), where the cap is `verdictMaxLen()` resolving
`AGENT_BUS_VERDICT_MAX` (positive int) else `defaultVerdictMax = 10000`. This
mirrors the existing `reportMaxLen()` env pattern in `bus/bus.go`.

Rationale for 10 000 vs the `streamMaxLen = 1000` used by status/report/notify/cmd:
this is the money-path audit trail, not a high-churn activity feed. Verdict volume
is low (a handful per subject), so 10 000 is effectively full history while still
bounded.

### Entry fields

| Field | Meaning |
|-------|---------|
| `subject` | The review subject key, e.g. `pr:25` or `commit:abc123` (required) |
| `author` | The agent whose work is under review (the old `<target>` positional) |
| `reviewer` | The agent issuing the verdict (`self` = `AGENT_BUS_AGENT`) |
| `decision` | `approve` or `reject` |
| `message` | Free-text rationale (optional; sanitized like reports) |
| `ref` | Optional correlation id linking to a `challenge` thread |

The entry timestamp is the Redis stream id (`<ms>-<seq>`); no separate time field.

### Bus API (in `bus/stream.go`)

- `func VerdictsKey(project string) string` → `{project}:verdicts`.
- `type Verdict struct { ID, Subject, Author, Reviewer, Decision, Message, Ref string }`
  with a `TS int64` derived from the id (ms), matching the `AgentSnapshot` idiom.
- `func (b *Bus) AppendVerdict(ctx, v Verdict) (id string, err error)` — validates
  `author` and `reviewer` via `ValidName`, requires non-empty `subject` and a
  `decision` of `approve|reject`, `XADD`s with the cap, returns the id.
- `func (b *Bus) Verdicts(ctx, subject string) ([]Verdict, error)` — `XRANGE` over
  the stream (oldest→newest); if `subject != ""`, filter to matching entries; else
  return all. Scan+filter is acceptable at this scale (no secondary index).

`message` is passed through `SanitizeReportMessage` so a verdict stays one bounded
line (consistent with the line-based consumers).

## Write path — `verdict` command

New usage (old `--ref`-driven form still works as a subset):

```
agentbus --project P verdict --pr N        <author> <approve|reject> [msg...]
agentbus --project P verdict --subject S   <author> <approve|reject> [msg...]
agentbus --project P verdict --ref R --pr N <author> approve [msg...]   # also resolves gate
```

Behavior, in order:

1. Resolve the subject: exactly one of `--pr N` (→ `subject = "pr:N"`) or
   `--subject S` must be present; otherwise usage error (exit 1). `N`/`S` are
   trimmed; empty is an error.
2. Validate `decision ∈ {approve, reject}` (unchanged from today).
3. Build the message: `decision` alone, or `decision + ": " + joined-rest`
   (unchanged from today).
4. `reviewer = self` (`AGENT_BUS_AGENT`, default `hermes`); `author = <positional>`.
5. **Append the ledger entry** (`AppendVerdict`). This always happens — it is the
   durable record, including self-approvals.
6. **Publish `CmdVerdict`** to `{p}:cmd` (`from=self, target=author, type=verdict,
   ref=<ref or "">, command=message`) so busmon ACTIVITY and the author see it live
   — unchanged from today.
7. **Bonus gate resolution**: if `--ref R` is given, attempt
   `ResolveChallenge(author, R)`. If no such challenge is open, this is **not** a
   fatal error anymore — log a notice to stderr and continue. Without `--ref`, no
   gate interaction at all.

**Backward-compatibility / behavior change** (additive and loosening — cannot break
armed subscribers, since the `:cmd`/`subscribe` wire format is untouched):

- `--ref` becomes **optional** (was effectively required to resolve a gate).
- `verdict` **no longer dies** when there is no open challenge. Previously
  `ResolveChallenge` failure aborted the command; now resolution is best-effort.
- A subject (`--pr`/`--subject`) is now **required**.

### Independence (self-approval) handling

The write path records every verdict, including `reviewer == author`. The
independence rule is enforced only at **read** time (roll-up), so the audit trail
preserves attempted self-approvals.

## Read path — `verdicts` command

```
agentbus --project P verdicts --pr N        # one subject: roll-up + detail
agentbus --project P verdicts --subject S   # one subject: roll-up + detail
agentbus --project P verdicts               # overview: recent verdicts, all subjects
```

### Roll-up rule (frozen, to remove ambiguity)

For a given subject, over all its entries sorted by stream id (chronological):

- `indepApprove` = the **latest** entry with `decision == approve` and
  `reviewer != author`.
- `lastReject`   = the **latest** entry with `decision == reject`.

Then:

1. **APPROVED** — iff `indepApprove` exists AND (`lastReject` does not exist OR
   `indepApprove` is strictly newer than `lastReject`). Reported as
   `APPROVED ✓ (<reviewer>, <relative-age>)`.
2. **REJECTED** — else iff `lastReject` exists. Reported as
   `REJECTED ✗ (<reviewer>, <age>)`.
3. **PENDING** — otherwise. Covers: no verdicts at all; or only self-approvals
   (annotated `PENDING (only self-approval)`).

Detail lines follow the header, oldest→newest, each:
`<time>  <decision>  <reviewer>  "<message>"  [ref=…]`, with a `(superseded)`
marker on entries older than the deciding entry, and an `(author=…, ignored)`
marker on self-approvals.

### Exit codes

| State | Exit |
|-------|------|
| APPROVED | 0 |
| PENDING | 2 |
| REJECTED | 3 |
| usage/connection error | 1 |

This lets an agent gate a merge: `agentbus verdicts --pr 25 || block-merge`.

The no-argument overview form lists the most recent **25** entries across all
subjects (matching busmon's default window) and exits 0; it computes no
per-subject roll-up.

## Non-goals (explicit)

- **busmon surfacing** — a per-subject 4-eyes indicator/panel is an obvious
  follow-up, intentionally excluded to keep this slice small and CLI-only.
- **Ledgering `reply`/`directive`** — out of scope; only verdicts are persisted.
- **ack/TTL on directives (#4), identity leases (#5), protocol versioning (#6)** —
  separate concerns, not touched here.

## Testing

Following the repo's existing `_test.go` style (miniredis-backed bus tests,
arg-parsing tests for the CLI):

**`bus` package**
- `AppendVerdict` then `Verdicts("")` round-trips all fields.
- `Verdicts(subject)` filters to the matching subject only.
- Roll-up rule (a small table-driven test over the four cases):
  - independent approve → APPROVED;
  - self-approval only → PENDING;
  - reject after approve → REJECTED (reject supersedes);
  - reject then later independent approve → APPROVED.
- Bonus gate resolution: a verdict with `--ref` on an open challenge resolves it;
  a verdict with `--ref` on no open challenge succeeds (best-effort, no error).
- A standalone verdict (no `--ref`, no prior challenge) still lands in the ledger.
- `AppendVerdict` rejects an invalid `author`/`reviewer`, empty `subject`, or a
  `decision` outside `{approve, reject}`.

**`agentbus` CLI**
- Arg parsing: `--pr` ↔ `pr:N`, `--subject`, `--ref` optional; exactly-one-subject
  enforcement; missing author/decision usage errors.
- Exit-code mapping for APPROVED / PENDING / REJECTED.

## Files touched

- `bus/stream.go` — `VerdictsKey`, `Verdict`, `AppendVerdict`, `Verdicts`,
  `verdictMaxLen()` (+ `defaultVerdictMax`).
- `cmd/agentbus/main.go` — rework the `verdict` case (subject-first, optional ref,
  best-effort resolve, ledger append); add a `verdicts` case; update the usage
  banner.
- New `cmd/agentbus/verdicts.go` (+ `verdicts_test.go`) — roll-up computation and
  rendering, kept out of `main.go` for testability (mirrors `agents.go`/`usage.go`).
- `bus/stream_test.go` (or a new `verdict_test.go`) — bus-level tests above.
- `README.md` / `CLAUDE.md` — document the new stream key, the two commands, and
  the roll-up/exit-code contract.
