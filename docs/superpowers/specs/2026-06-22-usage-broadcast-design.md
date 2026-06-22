# Usage broadcast — Design (Slice 3c)

**Date:** 2026-06-22
**Status:** Approved for implementation
**Scope:** `bus` + `cmd/agentbus` + `cmd/busmon` (minimal) + a status-line snippet (docs) + a
master-skill addition. Branches off `main`. Final sub-slice of Slice 3.

| Sub-slice | Theme | Status |
|-----------|-------|--------|
| 3a | Pane mapping | MERGED (PR #10) |
| 3b | Master herdr-control skill | MERGED (PR #11) |
| **3c (this doc)** | Usage broadcast | designing |

## Goal

Surface each agent's remaining budget (`Model / Ctx / Weekly / Session / Reset`) so the master can
give the team a regular status. **The data is emitted as structured fields by each agent's own
status-line script — never scraped from rendered terminal text** (the unreliable approach we
rejected: the status-line format is user-configurable and changes). The bus carries structured
fields, so restyling a status line never moves the bus numbers.

## Design decisions (settled in brainstorming)

- **Source:** the agent's status-line script (which already computes these numbers) tees them to the
  bus — not a scrape.
- **Distribution: notify + pull.** The master posts a one-line team summary to `{p}:notify` (seen in
  busmon / by the human) and agents read `agentbus usage` on demand. **Not** pushed via `cmd` to each
  agent — that would wake/interrupt every agent's `subscribe` on each update.
- **Schema:** store the **display strings** the script already computed (no numeric parsing — avoids
  coupling to units/format). Sorting/alerting on numbers is a future addition (YAGNI).

## Non-goals

- Numeric usage math / "who's lowest" alerting. Inbound Signal. Any scrape of rendered text.

---

## Component A — `{project}:usage` hash + `bus` API

A separate latest-wins hash (separate from `{p}:agents` — a different writer and cadence). Mirrors
the 3a `AgentsKey`/`AgentSnapshot`/`Agents` pattern.

```go
func UsageKey(project string) string { return project + ":usage" }

type UsageSnapshot struct {
	Model   string `json:"model,omitempty"`
	Ctx     string `json:"ctx,omitempty"`
	Weekly  string `json:"weekly,omitempty"`
	Session string `json:"session,omitempty"`
	Reset   string `json:"reset,omitempty"`
	TS      int64  `json:"ts"`
}

// SetUsage writes (overwrites) an agent's usage snapshot. agent must be a valid name.
func (b *Bus) SetUsage(ctx context.Context, agent string, snap UsageSnapshot) error

// Usage returns agent → latest usage snapshot. Unparseable fields are skipped.
func (b *Bus) Usage(ctx context.Context) (map[string]UsageSnapshot, error)
```

`SetUsage` validates the agent, marshals, and `HSET`s `UsageKey`. `Usage` is `HGETALL` + unmarshal.

---

## Component B — `agentbus usage`

- **Write:** `agentbus usage <agent> '<json>'` — parse the JSON into a `UsageSnapshot` (unknown
  fields ignored; the 5 fields above are the contract), stamp `TS = time.Now().UnixMilli()`, then
  `Bus.SetUsage`. Bad JSON or invalid agent → `die`; a transient Redis write error → `die` too (the
  status-line snippet swallows non-zero so it never breaks the line — see Component D).
- **Read:** `agentbus usage` (no agent) → an aged table, sorted by name (a pure `usageTable`
  formatter, like `agentsTable`); `agentbus usage --json` prints the raw map.

**Files:** `cmd/agentbus/usage.go` (`usageTable`), `cmd/agentbus/main.go` (`usage` case + usage
string + doc comment), `cmd/agentbus/usage_test.go`.

---

## Component C — busmon minimal display

busmon's existing 1s ticker (which already polls `Agents` for the pane) gains a `Bus.Usage` poll and
stores a compact badge per agent. The chip shows the most decision-relevant budget bits
(session · reset) when present.

- `agentState` gains `usage string`.
- A pure `usageBadge(snap bus.UsageSnapshot) string` joins the **non-empty** of `Session`/`Reset`
  with `·` — `99%·36m` (both), `99%` (session only), `36m` (reset only), `""` (neither). The ticker
  sets `a.usage = usageBadge(snaps[n])`; `agentLabel` appends `[gray][<usage>][-]` when
  `a.usage != ""`. No ghost chips (enrich tracked agents only, same rule as the pane poll).

**Files:** `cmd/busmon/render.go` (`usageBadge`; `agentLabel` render), `cmd/busmon/main.go`
(`agentState.usage`; ticker poll), `cmd/busmon/render_test.go` (`usageBadge` + agentLabel test).

---

## Component D — status-line tee snippet (docs)

A copy-paste block in `docs/AGENT-BUS-GUIDE.md`: a **throttled** tee the user adds to their
status-line script (it already has the values). Throttle with a timestamp file so the frequent
status-line refresh doesn't hammer Redis; swallow errors so the line never breaks:

```bash
# in your statusLine script, after you've computed MODEL/CTX/WEEKLY/SESSION/RESET:
ts=/tmp/abus-usage-$AGENT_BUS_AGENT
if [ -z "$(find "$ts" -newermt '-20 seconds' 2>/dev/null)" ]; then
  agentbus usage "$AGENT_BUS_AGENT" \
    "{\"model\":\"$MODEL\",\"ctx\":\"$CTX\",\"weekly\":\"$WEEKLY\",\"session\":\"$SESSION\",\"reset\":\"$RESET\"}" \
    >/dev/null 2>&1 || true
  touch "$ts"
fi
```

Requires `AGENT_BUS_PROJECT`/`AGENT_BUS_AGENT` in the status-line script's env.

---

## Component E — master-skill addition

`skills/agent-bus-master/SKILL.md` gains a **"Broadcast team budget"** step: read everyone's usage,
format a one-line summary, post it for the human/team; agents pull on demand.

```bash
agentbus usage                                  # the team budget table (or --json)
agentbus notify "budget — claude1 99%/36m · claude2 41%/2h · …"   # periodic one-line summary
```
Agents that want their own/peer budget run `agentbus usage` themselves (pull) — never pushed via cmd.

---

## Testing

- **`bus`** — `SetUsage`/`Usage` round-trip (a snapshot in, the same fields out; `TS` set).
- **`agentbus`** — `usageTable` renders the fields + age, sorted, and omits empty fields cleanly.
- **busmon** — `usageBadge` (session·reset present; empty → `""`); `agentLabel` shows `[<usage>]`
  when set and not when empty.
- **Snippet + skill** — prose; the `agentbus usage` commands are checked against the real CLI.
- Gate (each task + final): `go build ./... && go vet ./... && go test ./... -count=1`.

---

## Summary of changed surfaces

| File | Change |
|------|--------|
| `bus/stream.go` | `UsageKey`, `UsageSnapshot`, `Bus.SetUsage`, `Bus.Usage` |
| `bus/stream_test.go` | `SetUsage`/`Usage` round-trip test |
| `cmd/agentbus/usage.go` | **new** — `usageTable` |
| `cmd/agentbus/main.go` | `usage` command (write + read) + usage string + doc comment |
| `cmd/agentbus/usage_test.go` | **new** — `usageTable` test |
| `cmd/busmon/render.go` | `usageBadge`; `agentLabel` renders `[<usage>]` |
| `cmd/busmon/main.go` | `agentState.usage`; ticker `Bus.Usage` poll |
| `cmd/busmon/render_test.go` | `usageBadge` + agentLabel-usage test |
| `docs/AGENT-BUS-GUIDE.md` | the status-line tee snippet + `agentbus usage` cheat line |
| `skills/agent-bus-master/SKILL.md` | the "Broadcast team budget" step |

## Things to get right

- **Structured, not scraped:** the bus stores named fields; nothing parses a rendered status line.
- **Separate hash:** `{p}:usage` is distinct from `{p}:agents` (different writer/cadence; no
  read-modify-write contention).
- **The tee must never break the status line:** the snippet throttles and swallows errors (`|| true`).
- **English UI/CLI copy** (project preference).
- **No ghost chips in busmon:** the `Bus.Usage` poll enriches already-tracked agents only.
