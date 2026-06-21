# Bus Protocol Robustness — Design (Slice 1)

**Date:** 2026-06-21
**Status:** Approved for implementation
**Scope:** `bus/` package + `cmd/agentbus/` CLI + `docs/`. **`cmd/busmon/` is untouched.**

This is the first of three planned slices distilled from real multi-agent usage friction:

| Slice | Theme | Status |
|-------|-------|--------|
| **1 (this doc)** | Bus protocol robustness — the 5 friction points | designing |
| 2 | busmon UI: width-fit + master-agent status bar | later |
| 3 | Master orchestration + herdr integration (usage broadcast, unblock/resync) | later |

The architectural through-line: **Slice 1's agent-state hash (`{project}:agents`) becomes the
join key** that Slices 2 and 3 build on (an agent will later register its `HERDR_PANE_ID` and
usage line there). Slice 1 keeps that hash minimal; the forward fields are deferred (YAGNI).

---

## Goals (the 5 friction points)

1. **subscribe replays stale backlog** — each consuming session hand-rolls a `floor + XREVRANGE`
   reconciliation to tell a live command from an archive replay. Fix: a persistable cursor +
   `--since` floor, with **skip-backlog (floor = now) as the default**.
2. **No queryable current state per agent** — knowing whether `claude2` is idle/working/blocked
   requires scanning the `cmd`/`status` stream. Fix: an authoritative per-agent state hash +
   an `agentbus agents` command.
3. **`report` truncated at 120 runes** — forced important content into `cmd`. Fix: keep the
   channel, raise the cap to a configurable default (500).
4. **Free-text command parsing is fragile** — agents `awk` the bracketed wire format. Fix:
   **JSON-by-default** subscribe output — one object per fire.
5. **Subagents lack bus access** — `MEMORY.md`/quickref only reach top-level sessions. Fix: a
   copy-paste `CLAUDE.md` drop-in block in the guide.

## Non-goals / out of scope

- **busmon changes** — it already derives idle/offline correctly from the stream tail; the new
  hash is purely for the `agentbus agents` CLI query and future slices.
- **`HERDR_PANE_ID` / usage fields** on the agent-state hash — deferred to Slice 3.
- **Retiring the hermes→Signal relay** — the relay is an *external* hermes systemd consumer of
  `{project}:report`, not part of this repo. It is being retired (can return later); that is a
  hermes-side action. Slice 1 leaves `report`, `ReportAuto`, and `--auto` intact. A future slice
  may formally deprecate the `ReportAuto` path once the relay is gone. Recorded here as roadmap
  context only.

---

## Component A — `subscribe`: cursor + `--since` floor + JSON-by-default (fixes #1, #4)

The cursor, the command payload, and the rearm sentinel collapse into **one JSON object per
fire**. The caller does exactly one `json.Parse`, acts on the payload, persists `id`, and re-arms
iff `rearm == true`. The old two-line `[bracket]` + `__AGENTBUS__` text format is removed.

### Output schema (one object per fire)

```jsonc
// a command arrived — exit 0
{"event":"cmd","rearm":true,"id":"1782053749061-3","type":"directive","from":"hermes","target":"claude4-hl","ref":"58.2-spine","body":"rebase onto main"}
// idle window elapsed — exit 64
{"event":"heartbeat","rearm":true}
// transient glitch — exit 75
{"event":"error","rearm":true,"msg":"…"}
// misconfigured — exit 1
{"event":"fatal","rearm":false,"msg":"invalid agent …"}
```

Implementation struct (marshaled with `encoding/json`, so bodies with quotes/specials are escaped
correctly):

```go
type subEvent struct {
    Event  string `json:"event"`
    Rearm  *bool  `json:"rearm,omitempty"`   // *bool: nil omits the field (used by --loop)
    ID     string `json:"id,omitempty"`
    Type   string `json:"type,omitempty"`
    From   string `json:"from,omitempty"`
    Target string `json:"target,omitempty"`
    Ref    string `json:"ref,omitempty"`
    Body   string `json:"body,omitempty"`
    Msg    string `json:"msg,omitempty"`
}
```

- `Rearm` is a `*bool` so `fatal` (`rearm:false`) is **not** dropped by `omitempty`, while `--loop`
  cmd entries (which carry no rearm semantics) omit the field entirely (`nil`).
- Exit codes are **unchanged**: cmd 0, heartbeat 64, error 75, fatal 1. The wake-on-exit contract
  (re-arm iff `rearm==true`, which maps to exit codes ≠ 1) is preserved.
- The deprecated `__HEARTBEAT__` line is **dropped** (we are making a clean breaking cutover).
- **`--loop`** (headless) emits one `{"event":"cmd","id":…,…}` object per delivered entry, with no
  `rearm` field, continuously — same as today minus the bracket format.
- **`listen`** (debug tail) is **unchanged** — it stays human-readable.

### Input — `--since <cursor>` floor

- `--since <id>` → deliver only entries with stream-ID **strictly greater than** `<id>` (the value
  echoed in a prior fire's `id`). Accepts a full id (`<ms>-<seq>`) or a bare `<ms>` (treated as
  `<ms>-0`).
- **Omitted → floor = now** (the chosen default): a fresh subscribe skips all backlog. "Now" is the
  Redis **server** clock (via `TIME`, no client skew), formatted `<ms>-0`.
- `--since 0` → no floor → full at-least-once replay (escape hatch / today's behavior).

### Mechanism (`bus.WatchCmd`)

`WatchCmd` gains a `floor string` parameter:

```go
func (b *Bus) WatchCmd(ctx context.Context, agent, consumer, floor string, fn func(Event) bool) error
```

- **Group creation:** if `floor != "" && floor != "0"`, create the consumer group **at** `floor`
  (`XGROUP CREATE … MKSTREAM <floor>`), so a *fresh* group catches everything after a persisted
  cursor. An existing group yields `BUSYGROUP` and keeps its server-side cursor (unchanged).
  `floor == "0"` creates at `0` (replay all). `floor == ""` creates at `$` (today's behavior).
- **Delivery filter:** every read entry is still `XACK`ed (drains the PEL, advances the cursor) —
  but `fn` is called only when `e.Target == agent` **and** `e.ID > floor`. Pre-floor archive
  entries are ACKed-but-not-delivered, so stale backlog never reaches the agent and the group
  cursor still moves past it. Comparison reuses the existing `idLess` helper in `stream.go`.

New supporting `bus` API:

```go
// ServerFloor returns the current Redis server time as a stream-id floor "<ms>-0".
func (b *Bus) ServerFloor(ctx context.Context) (string, error)
```

### Caller-side files

- `bus/stream.go` — `WatchCmd` signature + floor logic; `ServerFloor`.
- `cmd/agentbus/main.go` — `extractFlag(rest, "--since")`; compute floor (server-now when absent);
  pass into `runSubscribe`. The `--loop` and positional `[idle_secs]` parsing is unchanged.
- `cmd/agentbus/subscribe.go` — replace `printCmd` + `rearmSentinel` text with `subEvent` JSON
  emission; thread `floor` and the delivered entry's `id` through `runSubscribe`/`WatchCmd`.

### The reconciliation pattern disappears

A well-behaved consumer now does: persist the last `id`; on each arm pass `--since <persisted id>`;
on a `cmd` fire, act on `body` and store the new `id`; re-arm iff `rearm`. No `XREVRANGE`, no
manual floor math. A first-ever arm with no persisted cursor and no `--since` simply starts at now.

---

## Component B — Agent-state hash + `agentbus agents` (fixes #2)

An authoritative, O(1)-readable snapshot of every agent's current state, written by `status`.

### Storage

- Key: `{project}:agents` (a Redis hash). New helper `AgentsKey(project string) string`.
- Field = agent name; value = JSON:

```go
type AgentSnapshot struct {
    State   string `json:"state"`
    Message string `json:"message,omitempty"`
    TS      int64  `json:"ts"` // ms, from the status entry's stream id
}
```

### Write path

`Bus.Status` additionally `HSET {project}:agents <agent> <json>` after a successful `XADD`,
deriving `TS` from the returned entry id's ms (`splitID`). **The hash write is a best-effort cache
update:** the stream remains the source of truth, so `Status` returns the `XADD` id and the `XADD`
error only; a failed `HSET` is ignored (the cache goes briefly stale rather than failing a status
publish that already landed on the stream). This keeps `status` robust and the change additive.

`report` does **not** write the hash — it is liveness-only and stays a busmon concern. (An agent
that only ever `report`s, never `status`es, won't appear in `agents`; per the guide, `status` is
the state heartbeat.)

### Read path

- New `bus` API: `func (b *Bus) Agents(ctx context.Context) (map[string]AgentSnapshot, error)` —
  `HGETALL` + unmarshal; unparseable fields are skipped.
- New CLI command `agentbus agents`:
  - default: one aged line per agent, sorted by name. Aging reuses busmon's thresholds
    (`idleAfter = 2m`, `staleAfter = 10m`, redefined locally in `cmd/agentbus`):
    ```
    claude1   working  (plan 10 shipped)        12s ago
    claude2   idle                              3m ago    · idle
    claude4   blocked  (waiting on verdict)     8m ago
    hermes    done                              11m ago   · offline
    ```
  - `--json`: prints the raw `map[string]AgentSnapshot` for scripts.
  - Entries are never auto-deleted (Redis hashes have no per-field TTL); age makes staleness
    obvious. No `--prune` in Slice 1 (YAGNI).

### Files

- `bus/stream.go` — `AgentsKey`, `AgentSnapshot`, `Bus.Agents`, `Status` hash write.
- `cmd/agentbus/main.go` — `agents` case + usage line + the `--double-dash` usage string.

---

## Component C — `report`: configurable cap (fixes #3)

- Keep `report`, `--auto`/`ReportAuto`, and newline-stripping (the line-based `listen`/`subscribe`
  protocols still need single-line reports). The cap's only remaining rationale is keeping the
  busmon/listen feed one line.
- Replace the hardcoded `const maxReportLen = 120` in `bus/bus.go` with a resolver:

```go
// reportMaxLen resolves the report rune cap: AGENT_BUS_REPORT_MAX if set and > 0, else 500.
func reportMaxLen() int { … } // reads env per call (cheap; keeps it test-settable)
```

  `SanitizeReportMessage` calls `reportMaxLen()` for its truncation bound. Reading the env per call
  (rather than a package-var init) keeps it settable from tests.
- Default **500 runes**; override via `AGENT_BUS_REPORT_MAX`. Non-numeric or ≤0 falls back to 500.

### Files

- `bus/bus.go` — `reportMaxLen` resolver; `SanitizeReportMessage` uses it.

---

## Component D — Bus quickref drop-in for subagents (fixes #5)

`docs/AGENT-BUS-GUIDE.md` already is the full agent-facing card. Two additions:

1. **A "Drop this into your project's `CLAUDE.md`" block** near the top — a small copy-paste section
   so consuming projects (trading, …) give every subagent bus access from the one file that reaches
   them. Shape:

   ```markdown
   ## Agent Bus (coordination)
   - Binary: `agentbus` (built from github.com/netbja/agent-bus-monitor; `go install ./...`).
   - Identity/namespace:
     `export AGENT_BUS_PROJECT=<project>` and `export AGENT_BUS_AGENT=<your-agent-name>`.
   - Receive directives (arm as a background task; its exit wakes you):
     `agentbus subscribe "$AGENT_BUS_AGENT" --since "<last-cursor>"`  # JSON per fire; persist `id`
   - Publish state: `agentbus status "$AGENT_BUS_AGENT" working "<msg>"`.
   - Full reference: docs/AGENT-BUS-GUIDE.md.
   ```

2. **Rewrite §2 subscribe lines + §3 sentinel table to the JSON contract** — this doubles as the
   migration doc for the breaking change. The §3 table becomes the four `event` shapes above with
   their exit codes, and a note that the caller persists `id` and passes it back as `--since`.

### Files

- `docs/AGENT-BUS-GUIDE.md` — the drop-in block + §2/§3 rewrite.

---

## Cross-cutting

### Tests (TDD)

- `cmd/agentbus/subscribe_test.go` — assert the four `subEvent` JSON shapes (parse + field
  values), exit codes, and `--since` floor filtering (pre-floor entries ACKed-not-delivered;
  post-floor delivered; `--since 0` replays).
- `bus` tests — `Bus.Agents` round-trip via `Status`; `AgentSnapshot` marshal/unmarshal; the
  configurable report cap (set `AGENT_BUS_REPORT_MAX`, assert truncation; default 500).
- Gate: `go build ./... && go vet ./... && go test ./... -count=1` stays green.

### Breaking-change migration

The JSON cutover breaks any currently-armed agent that parses the old `[bracket]` / `__AGENTBUS__`
lines. Shipping Slice 1 requires updating consuming projects' arming logic at the same time. The
new contract is documented in `docs/AGENT-BUS-GUIDE.md` (Component D) so the migration is
self-describing.

### Roadmap note

- The hermes→Signal relay is slated for retirement (external; can return later). When it is gone, a
  future slice may deprecate `ReportAuto`/`--auto`. Out of scope here.

---

## Summary of changed surfaces

| File | Change |
|------|--------|
| `bus/stream.go` | `WatchCmd(+floor)`, `ServerFloor`, `AgentsKey`, `AgentSnapshot`, `Bus.Agents`, `Status` hash write |
| `bus/bus.go` | `reportMaxLen` resolver; `SanitizeReportMessage` uses it |
| `cmd/agentbus/main.go` | `--since` parse + server-now floor; new `agents` command; usage string |
| `cmd/agentbus/subscribe.go` | `subEvent` JSON output (single object per fire); thread floor + cursor |
| `cmd/agentbus/subscribe_test.go` | JSON-shape + `--since` floor tests |
| `bus/*_test.go` | `Agents` round-trip + report-cap tests |
| `docs/AGENT-BUS-GUIDE.md` | `CLAUDE.md` drop-in block; §2/§3 JSON-contract rewrite |
| `cmd/busmon/*` | **none** |
