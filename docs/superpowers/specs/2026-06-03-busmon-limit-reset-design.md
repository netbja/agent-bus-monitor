# busmon: default activity window + `--reset` purge

**Date:** 2026-06-03
**Status:** Approved
**Scope:** `cmd/busmon`, `bus/stream.go`, docs

## Problem

`busmon` replays the **entire** retained stream history on launch — `Bus.Tail(ctx, "0", …)`
backfills up to ~1000 entries × 4 streams into the ACTIVITY feed before going live. The feed
floods, and there is no way to start fresh or clear the history.

## Goals

1. **Default window** — on launch, show only the last **25** merged ACTIVITY lines (across all four
   streams, chronological), then live-tail. Customizable; the old "replay everything" behavior stays
   reachable.
2. **`--reset`** — purge the project's four streams so the conversation history is cleared, gated by
   a confirmation so it can't wipe shared history by accident.

There is no "conversation" entity in the bus; the ACTIVITY feed is a merged chronological view of
`status`/`report`/`notify`/`cmd` entries. "Les 25 dernières conversations" ⇒ "the last 25 ACTIVITY
lines."

## Decisions (locked)

| Question | Decision |
|---|---|
| `--reset` semantics | **Destructive purge of Redis, gated by confirmation** (`[y/N]`, or `--reset --yes`) |
| Limit customization | `--limit N` (default 25) **+** `AGENT_BUS_BUSMON_LIMIT` env; `--limit 0` (or ≤0) = full history |
| Limit counting | Last N **merged** ACTIVITY lines across all four streams, chronological |
| Purge scope / primitive | All four streams via **`XTRIM MAXLEN 0`** — clears entries, **preserves** consumer groups + `armed`/`pilot`/`gate` keys |

## Where the code goes

Per `CLAUDE.md`, transport/protocol primitives live in `bus/stream.go`; the binary stays thin. The
bus gains three small, independently-testable methods; `cmd/busmon/main.go` wires them.

## `bus/stream.go` — additions

### `idLess(a, b string) bool` (unexported helper)
Compares two stream IDs `<ms>-<seq>` **numerically** (parse `ms`, then `seq`). Lexicographic
comparison is wrong: `"10-0" < "9-0"` as strings.

### `Recent(ctx, kinds []string, n int) ([]Event, map[string]string, error)`
- For each kind: `XRevRange(key, "+", "-", Count=n)` → up to n newest entries.
- Parse to `Event`, merge across kinds, sort ascending by `idLess`, keep the last `n` → the n most
  recent overall, in chronological order (ready to render top→bottom).
- Returns a **cursor map** `streamKey → newest ID` for every stream that had entries. Empty streams
  are omitted: the follow-on tail defaults a missing key to `"0"`, which replays nothing now but
  catches every future entry — avoiding the documented `"$"` gap (entries arriving between backfill
  and live-subscribe).

### `TailFrom(ctx, start map[string]string, kinds []string, fn func(Event)) error`
- Today's `Tail` loop, but each stream starts from `start[key]` (missing key → `"0"`).
- `Tail(ctx, lastID, kinds, fn)` is refactored into a one-line wrapper calling `TailFrom` with every
  key set to `lastID`. **No behavior change** for `agentbus listen` or the `--limit 0` path.

### `Purge(ctx, kinds []string) (int64, error)`
- `XTRIM <key> MAXLEN 0` per kind; returns total entries removed (for the summary line). Missing key
  → 0, no error. Consumer groups survive `XTRIM`; `armed:`/`pilot`/`gate:` are distinct keys and are
  left untouched — so cmd at-least-once delivery is unaffected.

## `cmd/busmon/main.go` — wiring

### Flags
- `--limit` int, default `25`
- `--reset` bool
- `--yes` bool (skip the reset confirmation)

### `resolveLimit(flagSet bool, flagVal int, env string) int` (pure function)
Precedence: explicit `--limit` (detected via `flag.Visit`) → `AGENT_BUS_BUSMON_LIMIT` → `25`. A
non-parsing env value falls through to 25. Extracted as a pure function so it is unit-tested without
a TUI.

### Reset flow (before building the TUI, terminal still in normal mode)
- If `--reset` and not `--yes`: print
  `Purger l'historique des 4 streams du projet '<p>' (status/report/notify/cmd) ? [y/N] `,
  read one line from stdin. Accept `y`/`Y`/`yes`/`oui`. Anything else — including EOF from a
  piped/non-TTY stdin — prints `Annulé.`, exits 0, and **does not launch**.
- Confirmed (or `--yes`): `Purge(ctx, kinds)`, print `Purgé: N entrées effacées.`, then launch. The
  feed comes up empty (history is gone) and live-tails from `"0"`.

### Backfill + tail (replaces `go b.Tail(ctx, "0", …)`)
The ~60-line event handler currently inline in the `Tail` callback is extracted to a named
`handle(e bus.Event)` (a justified readability win — it is now called from two places). Then:
- `L <= 0` → unchanged: `go b.Tail(ctx, "0", kinds, handle)` (full history).
- `L > 0` → `events, cursors, _ := b.Recent(ctx, kinds, L)`; loop `handle(e)` over the backfill
  (chronological); then `go b.TailFrom(ctx, cursors, kinds, handle)`.

The existing `feedCap` / `MaxLines = 500` still clamps the display, so a very large `--limit` is
naturally bounded.

### Error handling
Startup-fatal, consistent with the existing `Connect`/`Open` pattern: `Recent`/`Purge` errors print
to stderr and `os.Exit(1)`.

## Tests

- **bus** (real Redis via `dialTest`, skips when unavailable — matching the suite):
  - `Recent`: seed mixed streams → assert last-N merged order + cursor IDs.
  - `Purge`: seed → purge → assert all four streams empty **and** a pre-created cmd consumer group
    survives.
  - `TailFrom`: per-stream cursors don't replay already-seen entries.
  - `idLess`: unit test, incl. the `"10-0"` vs `"9-0"` case.
- **busmon**: table test for `resolveLimit` (flag vs env vs default vs `0`).

## Docs
- `README.md`: busmon flags (`--limit`, `--reset`, `--yes`, env var).
- `CLAUDE.md`: busmon section + "Things that bite" note (`--limit 0` = full history; `--reset` is
  `XTRIM`-not-`DEL`, so consumer groups/leases survive).

## Out of scope (YAGNI)
In-TUI reset keybinding; per-stream limits; purging `pilot`/`gate`/`armed`.
