# Protocol Version Marker — Design Spec

- **Date**: 2026-07-01
- **Status**: Approved (brainstorm), ready for implementation plan
- **Scope**: One small slice, CLI-only (`agentbus` + `bus` package). No busmon changes.
- **Backlog item**: feedback friction #6 (fragile, unversioned binary coupling). See [[verdict-ledger-and-feedback-backlog]].

## Problem

The bus has an **implicit, unversioned wire contract**. Nothing anywhere declares
"I speak protocol vN", so when the *format* of what crosses a process boundary
changes, nothing can detect the mismatch — it breaks silently.

The concrete incident: the rearm-mechanism cutover from a `__AGENTBUS__` sentinel
string to JSON-per-fire. The consumer of `agentbus subscribe`'s output **lives
outside this repo** (the Claude session that armed `subscribe` as a background
task and parses its stdout to decide whether to re-arm). When the emitted format
changed, an already-armed subscriber — or any external parser of the old format —
silently failed: no wake of the terminal session, exactly the failure mode the
`subscribe` design exists to avoid. Hence the standing note that "every future
rebuild must be coordinated between all agents".

The `subscribe` JSON output (`subEvent`) is the single contract that is
**cross-process, out-of-repo, and survives a rebuild** (a subscriber can be
running while the binary is replaced). It is the only surface worth versioning.

## What already exists (do not rebuild)

- `cmd/agentbus/subscribe.go` emits one JSON `subEvent` per fire via `emit()`.
- There is **no** version/protocol marker anywhere in the codebase (confirmed by
  grep).
- Our own parsers are already forward-tolerant: `ParseEntry` reads stream fields
  by key (ignores unknown), and the `usage`/`verdict` `json.Unmarshal` paths
  ignore unknown keys and zero-value missing ones. This posture is to be
  **preserved**, not changed.

## Design decisions (locked during brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Scope | Version the `subscribe` JSON contract only, **plus** a trivial `agentbus version` command. |
| 2 | Version source | A single `const ProtocolVersion = 1` in `bus/bus.go`. |
| 3 | Where `v` is set | Centrally in `emit()` (one chokepoint), so every emitted variant carries it — no constructor can forget. |
| 4 | Consumer-on-unknown-`v` | Documented recommendation: **fail loud** on a higher-than-known `v`, not best-effort. (Documentation only — the consumer is out of repo.) |

## The version marker

- `bus/bus.go` gains `const ProtocolVersion = 1`, next to the other transport-
  neutral constants (`ReportNote`/`ReportAuto`, `ValidStates`). It is the single
  source of truth for the bus protocol version.
- `subEvent` (in `cmd/agentbus/subscribe.go`) gains a first field
  `V int \`json:"v"\`` (no `omitempty` — it must always appear). With `v` first,
  output reads `{"v":1,"event":"cmd",…}`.
- `emit()` sets it once for every line: `ev.V = bus.ProtocolVersion` before
  `json.Marshal`. The constructors (`cmdEvent`, the inline `heartbeat`/`error`/
  `fatal` literals) do **not** set `V` — the single chokepoint guarantees
  `cmd`, `heartbeat`, `error`, and `fatal` events all carry it.

Adding `v` now is **not a breaking change**: it is an extra key that any tolerant
consumer ignores. This slice does not bump anything — it declares the current
format as **v1**, the baseline.

## The `version` command

`agentbus version` reads a compile-time constant, so it must work with **no
project and no broker**. It is therefore handled **early** in `main` — before the
`project required` check and before `bus.Connect` — printing and exiting `0`
immediately. Output, exactly:

```
agentbus protocol v1
```

rendered as `fmt.Printf("agentbus protocol v%d\n", bus.ProtocolVersion)`. The
early branch keys off `args[0] == "version"` (after `--host`/`--project` flag
extraction, so flag order does not matter). It is added to the top-level usage
banner and the doc-comment header.

## The compatibility contract (documented in CLAUDE.md / README)

This is the substance of the slice — a rule, mostly enforced by discipline:

- `v` is the bus protocol version of the **emitting** binary.
- **Within the same `v`**, changes are **additive only** (new optional fields).
  A consumer **must ignore unknown fields**.
- `v` is **bumped only on a breaking change** to the `subscribe` contract: a field
  removed, renamed, or repurposed, or a change in the semantics of an existing
  field.
- **Recommended consumer behavior** on a `v` higher than the one it understands:
  **fail loud** — stop and surface the mismatch — rather than silently
  mis-handling the payload. The `v` field is what finally gives an out-of-repo
  consumer the means to do this; the sentinel→JSON-class incident becomes an
  explicit signal instead of a silent failure.

## Non-goals (explicit)

- **No `v` on individual stream entries** (`status`/`report`/`notify`/`cmd`/
  `verdicts`). Those are read by `ParseEntry` by key and are additive in practice;
  versioning them is out of scope for the cross-process pain this slice targets.
- **No `v` on `agents --json` / `usage` / `verdicts` output.** Those are CLI reads,
  not the armed cross-process contract.
- **No change to our own parsers.** They are already tolerant; the slice preserves
  that, it does not add new parsing logic.
- **No capability handshake / negotiation.** A single integer is right for a small
  trusted fleet; negotiation would be over-engineering.

## Testing

**`agentbus` CLI** (the subscribe tests already drive `runSubscribe` with a
captured output writer):
- Every emitted `subEvent` variant carries `"v":1`: assert it for a delivered
  `cmd` fire, a `heartbeat` (idle-timeout), an `error`, and a `fatal`
  (invalid-agent) event. Decode each emitted line and check the `v` field equals
  `bus.ProtocolVersion`.
- A regression guard that `emit` stamps `v` even when the `subEvent` passed in has
  `V == 0` (proves the central chokepoint, not the constructor, is authoritative).

**End-to-end smoke** (in the plan):
- `agentbus version` prints `agentbus protocol v1`.
- `agentbus subscribe …` output lines begin with `{"v":1,…}`.

## Files touched

- `bus/bus.go` — `const ProtocolVersion = 1`.
- `cmd/agentbus/subscribe.go` — `subEvent.V` field + set it in `emit()`.
- `cmd/agentbus/subscribe_test.go` — assert every variant carries `v:1` + the
  emit-stamps-v regression guard.
- `cmd/agentbus/main.go` — `version` case + usage banner + doc-comment header.
- `README.md`, `CLAUDE.md` — the compatibility contract (§ above).
- `docs/AGENT-BUS-GUIDE.md` — `agentbus version` cheat-sheet line.
