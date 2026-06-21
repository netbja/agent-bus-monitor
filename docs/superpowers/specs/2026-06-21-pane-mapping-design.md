# Pane-mapping foundation — Design (Slice 3a)

**Date:** 2026-06-21
**Status:** Approved for implementation
**Scope:** `bus/` + `cmd/agentbus/` + a minimal `cmd/busmon/` badge. **No herdr/Signal runtime
dependency.** Branches off `main`.

First sub-slice of Slice 3 (master orchestration + herdr). Slice 3 decomposes into:

| Sub-slice | Theme | Status |
|-----------|-------|--------|
| **3a (this doc)** | Pane-mapping foundation — the bus↔herdr join key | designing |
| 3b | Master herdr-control skill (resync + unblock + Signal relay) | later |
| 3c | Usage broadcast (Model │ Ctx │ Weekly │ Session │ Reset) | later |

This sub-slice is **only the join-key data layer**: record which herdr pane each agent occupies, and
make it queryable. 3b/3c consume it (`agentbus agents --json` → pane per agent). herdr pane ids can
change (herdr's own SKILL.md: "don't treat them as durable; re-read from `pane list`"), so the pane
is re-registered on every heartbeat — reconciling a stored id against live `herdr pane list` is 3b's
job, not 3a's.

## Goals

- An agent running inside herdr records its `HERDR_PANE_ID` into the bus, automatically, as part of
  its normal `agentbus status` heartbeat — zero new agent workflow.
- The mapping is queryable per agent (`agentbus agents`, table + `--json`) and visible in busmon.

## Non-goals (later sub-slices)

- Usage line (3c); herdr injection / unblock / Signal relay (3b); reconciling a stale pane id against
  live `herdr pane list` (3b). 3a stores exactly what `HERDR_PANE_ID` holds.

---

## Component A — `AgentSnapshot.Pane` + `Bus.Status` writes it

The pane is **hash-only metadata** — the `{project}:status` *stream* fields stay `agent/state/message`
(pane never enters stream history). It rides in the per-agent `{project}:agents` hash snapshot.

```go
type AgentSnapshot struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	TS      int64  `json:"ts"`
	Pane    string `json:"pane,omitempty"` // HERDR_PANE_ID when the agent runs inside herdr
}
```

`Bus.Status` gains a trailing `pane string` parameter and writes the full snapshot (`state`,
`message`, `ts`, `pane`) in its **existing single best-effort `HSET`** — one writer, no
read-modify-write race. The `XADD` to the status stream is unchanged. Passing `""` (non-herdr
agents) omits the field via `omitempty`.

```go
func (b *Bus) Status(ctx context.Context, agent, state, message, pane string) (string, error)
```

**Ripple:** the signature change touches all 9 existing `Status` call sites — the `agentbus status`
handler (passes the real pane) and the `bus` test calls (pass `""`). Mechanical.

---

## Component B — `agentbus status` reads the env; `agentbus agents` surfaces the pane

- The `status` command handler reads `os.Getenv("HERDR_PANE_ID")` and passes it to `Bus.Status`.
  Empty outside herdr → nothing changes for non-herdr agents. No new command, no new flag.
- `agentbus agents` shows the pane in its aged table when present, e.g.:
  ```
  claude1   working   12s ago   ⧉w1:p1   (plan 10)
  claude2   idle      3m ago             · idle
  ```
  Built by extending `agentsTable` to insert a `  ⧉<pane>` segment when `snap.Pane != ""`.
  `agentbus agents --json` already carries `pane` via the struct — no extra work.

---

## Component C — busmon: a minimal "herdr-attached" badge

busmon derives agent state from the **stream** (authoritative, ages correctly); the pane lives only
in the hash. So busmon's 1s ticker — which already polls `PilotDriver`/`ArmedAgents`/`CmdLag`/
`OpenChallenges` — gains a `Bus.Agents` poll and copies each known agent's `Pane` into its
`agentState`. It does **not** synthesize chips for hash-only agents (same rule as the lag-only
guard); it only enriches agents busmon already tracks.

- `agentState` gains `pane string`.
- `agentLabel` renders a `[blue]⧉[-]` badge (after the `👂`/`⌛`/`🔒` badges) when `a.pane != ""` —
  no signature change (it already takes `*agentState`).

This tells the human which agents the master can drive via herdr. It is presentation polish on the
3a data layer; the 3b skill reads the hash directly and does not depend on it.

---

## Testing

- **`bus`** — `Status(…, "w1:p1")` round-trips `Pane` through `Bus.Agents` (set case); `Status(…, "")`
  → `Pane` empty/omitted (extends the existing `TestAgentsSnapshot`).
- **`agentbus`** — `agentsTable` renders `⧉<pane>` when a snapshot has a `Pane`, and omits it when
  empty (extends `TestAgentsTable`).
- **busmon** — `agentLabel` shows the `⧉` badge when `agentState.pane != ""` and not when empty
  (extends `TestAgentLabel`).
- Gate (each task + final): `go build ./... && go vet ./... && go test ./... -count=1`.

---

## Summary of changed surfaces

| File | Change |
|------|--------|
| `bus/stream.go` | `AgentSnapshot.Pane`; `Bus.Status(+pane)` writes it in the snapshot `HSET` |
| `bus/stream_test.go`, `bus/recent_test.go` | `Status(…)` call sites get a trailing `""`; pane round-trip test |
| `cmd/agentbus/main.go` | `status` handler reads `HERDR_PANE_ID`, passes it to `Status` |
| `cmd/agentbus/agents.go` | `agentsTable` shows `⧉<pane>` when present |
| `cmd/agentbus/agents_test.go` | pane-column test |
| `cmd/busmon/main.go` | `agentState.pane`; ticker polls `Bus.Agents` → copies `Pane` |
| `cmd/busmon/render.go` | `agentLabel` renders the `⧉` badge |
| `cmd/busmon/render_test.go` | `⧉` badge test |

## Things to get right

- **Pane is hash-only** — never add it to the status stream's XADD fields.
- **Single writer:** only `Bus.Status` writes the snapshot (incl. pane); no separate merge write.
- **No ghost chips in busmon:** the `Bus.Agents` poll only enriches already-tracked agents with a
  pane; it never synthesizes a chip from the hash alone.
- **English UI copy** (per the project preference): the badge is a glyph; any words stay English.
