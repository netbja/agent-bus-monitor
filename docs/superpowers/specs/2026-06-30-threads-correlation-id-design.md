# Threads / Correlation-ID — Design Spec

- **Date**: 2026-06-30
- **Status**: Approved (brainstorm), ready for implementation plan
- **Scope**: One slice, CLI-only (`agentbus` + `bus` package). No busmon changes.
- **Backlog item**: feedback frictions #2 (two review paths don't converge) and #3 (no directive↔response correlation). See [[verdict-ledger-and-feedback-backlog]].

## Problem

A directive and its response are not linked. A `cmd` directive lands at stream id
`1782588072942-0`; the reply comes back as a separate `cmd` entry with no pointer
to that id, so "directive X → response Y → resolution Z" can only be reconstructed
by reading prose and timestamps. The `challenge → reply → verdict` flow *is*
correlated (by `ref`), but that machinery only engages when an author opens a
`challenge`; a plain `cmd` directive cannot be threaded onto.

## What already exists (do not rebuild)

- The `:cmd` wire format already carries `ref` (correlation id) and `type`
  (`directive|challenge|reply|verdict`).
- A subscriber already receives both the entry `id` and `ref` in its JSON
  (`cmd/agentbus/subscribe.go` `cmdEvent`).
- `reply --ref` / `verdict --ref` already thread onto a ref.

So this slice is **making `ref` ubiquitous + adding a reader**, not new wire
machinery. The `:cmd`/`subscribe` wire format is **untouched** — armed subscribers
cannot break (#6).

## Design decisions (locked during brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Thread-id model | **Natural stream-entry id.** A message's thread-id = its `ref` if set, else its own entry `id`. Flat threads: everyone references the root. |
| 2 | Thread lifetime | **Live read of `:cmd`** (recent ~1000), no new durable store. The verdict ledger remains the durable audit. |
| 3 | `cmd` write side | `cmd` **prints its entry id** and **accepts an optional `--ref`** (threadable follow-up directives). |
| 4 | `reply`/`verdict` | **Unchanged** — they already accept `--ref`; replying onto a directive means passing the directive's id as `--ref`. |
| 5 | `thread` reader | Plain chronological list (no roll-up). Reads `:cmd`, groups by `ref==T \|\| id==T`. |

A consequence of decision 1: the **same** reader unifies both review paths — a
thread rooted on a **directive id** and a thread rooted on a **challenge ref**
(`genRef`) read identically, since both live in the `ref` field. This answers
friction #2 at the view layer.

## Thread-id semantics

For any `:cmd` entry `e`, its thread-id is `e.Ref` if non-empty, else `e.ID`.
Threads are **flat**: a reply/verdict/follow-up references the **root**'s
thread-id, not an intermediate message's id. Concretely:

- A directive published by `cmd` has `ref=""`, so its thread-id is its own id `X`.
- `reply --ref X` / `verdict --ref X` / `cmd --ref X` all set `ref=X` → same
  thread.
- A challenge published by `challenge` has `ref=C` (a `genRef`), so its thread-id
  is `C`; its replies/verdict already carry `ref=C`.

To continue a thread, use the thread-id you received: for a message with a
non-empty `ref`, use that `ref`; for a root message (empty `ref`), use its `id`.
A subscriber has both fields, so it can always compute this.

## Write side — `cmd`

New usage: `agentbus cmd [--ref T] <target> <command...>`

1. `extractFlag(rest, "--ref")` → `ref` (default `""`, unchanged behavior).
2. `b.Cmd(ctx, self, target, bus.CmdDirective, ref, command)` — the only change
   from today is passing `ref` instead of a hardcoded `""`.
3. **Print the new entry id** on stdout as a single bare line (`fmt.Println(id)`),
   so it is trivially capturable (`ID=$(agentbus cmd claude2 …)`) and usable as
   the thread root. `cmd` previously printed nothing on success, so this is purely
   additive — no output format is being changed. (The `challenge` command keeps
   its existing `challenge <ref> opened on <target>` sentence; it is not touched.)

`reply` and `verdict` are **not modified**: they already accept `--ref` and pass
it through to `b.Cmd`. `b.Cmd` does not parse or validate the ref string, so a
stream-id-shaped ref (`1782588072942-0`) threads fine alongside the base36
`genRef` challenge refs.

## Read side — `thread`

New usage: `agentbus thread <T>`

### Bus API (`bus/stream.go`)

```
func (b *Bus) Thread(ctx context.Context, threadID string) ([]Event, error)
```

`XRANGE`s the `:cmd` stream (`"-"` → `"+"`, oldest→newest) and returns every
entry that belongs to thread `threadID`. An entry `e` belongs iff:

- `e.Ref == threadID`, **or**
- `e.ID == threadID` (the root), **or**
- `threadID` is all-digits (a bare `<ms>`) and `e.ID` has the prefix
  `threadID + "-"` (the same bare-ms tolerance `subscribe --since` offers).

Scan-and-filter over the capped (~1000) stream is acceptable at this scale,
mirroring `Verdicts`. Reads with `XRANGE` only — no consumer-group cursors
touched (like `Tail`/`Recent`).

### CLI rendering (`cmd/agentbus/thread.go`)

```
func threadReport(threadID string, evs []bus.Event, now time.Time) string
```

Pure function (no Redis). Renders a header plus one line per entry,
chronological:

```
thread <threadID>  (<N> entries)
  <age>  <type>     <from>→<target>  "<message>"  (root)
  <age>  <type>     <from>→<target>  "<message>"
```

- The entry whose `id == threadID` (the root) is marked `(root)`. If no entry is
  the root (e.g. `threadID` is a challenge ref, or the root aged out of the
  stream), no line is marked — the matched entries still list.
- `message` is quoted with `%q`, guarded so an empty message renders nothing
  (mirroring `verdictsReport`, not `verdictsOverview`'s earlier bug).
- `type` is the cmd type (`directive`/`challenge`/`reply`/`verdict`).
- An empty thread renders `thread <threadID>  (no entries)`.

`thread` exits `0` on success (including an empty thread — absence is not an
error); `die` (exit 1) stays reserved for usage/connection errors.

## Non-goals (explicit)

- **Threading `report`/`notify`** — those are broadcast/human-facing, not
  request↔response; excluded.
- **A durable thread store** — decided against; `thread` reads the live `:cmd`
  stream, the verdict ledger is the durable audit.
- **Ack/TTL on directives (#4)** — a directive lifecycle is a separate slice.
- **busmon surfacing** — a per-thread grouping in the TUI is an obvious
  follow-up, intentionally excluded.

## Testing

**`bus` package** (real-Redis `dialTest`):
- `Thread` groups a directive + its `reply`/`verdict` (published with
  `ref = directive.id`) and excludes an unrelated `cmd`; order is chronological.
- `Thread` groups a challenge-ref thread: a `challenge`/`reply`/`verdict` sharing
  `ref = C` all come back for `Thread("C")`.
- Bare-`<ms>` tolerance: `Thread("<ms>")` matches the entry whose id is
  `"<ms>-<seq>"`.
- An unknown thread-id returns an empty (non-nil) slice, not an error.

**`agentbus` CLI** (pure, no Redis):
- `threadReport`: root marked `(root)`; chronological order preserved; empty
  message renders no `""`; empty thread renders `(no entries)`.

**End-to-end smoke** (against the dev broker, in the plan):
- `ID=$(agentbus cmd claude2 review the migration)` prints an id; `reply --ref
  $ID hermes "on it"`; `agentbus thread $ID` shows the directive `(root)` then the
  reply.

## Files touched

- `bus/stream.go` — `Bus.Thread`.
- `bus/stream_test.go` — `Thread` tests.
- `cmd/agentbus/thread.go` (+ `thread_test.go`) — `threadReport` (pure), kept out
  of `main.go` for testability (mirrors `verdicts.go`/`agents.go`).
- `cmd/agentbus/main.go` — `cmd` accepts `--ref` and prints its id; new `thread`
  case; usage banner + doc-comment header.
- `README.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md` — document `cmd --ref` +
  the printed id, the `thread` command, and the thread-id = `ref || id` rule.
