# busmon coordination readability — slice 1

**Goal:** make busmon answer three operator questions without leaving the TUI —
*what was actually asked*, *what is really known about each agent*, and *what is
waiting on someone*.

**Scope:** `cmd/busmon/*` only. `bus/*` and `cmd/agentbus/*` belong to Codex
(see `docs/coordination-v1-contract.md`, Coordination v1). No bus schema is
invented here: every new field busmon renders is either already on the wire
today or named by that contract.

**Spec:** the operator brief of 2026-09-17 (three sections: readable messages,
understandable states, follow-up view) + Coordination v1 contract.

## Global constraints

- Redis Streams stays; no new framework, no rewrite.
- No change to `bus/`, `cmd/agentbus/`, agent panes, or any deployment.
- UI copy in English.
- Keep: keyboard navigation, OSC52 copy, mouse, scroll-pause, live resume,
  narrow-terminal layout.
- Never invent a deadline, an acknowledgement, or a completed state.
- Do not raise the 500-rune preview cap.
- `go build ./... && go vet ./... && go test ./... -count=1` green.

## Interfaces (busmon-local view models)

Codex's `bus.RequestInfo` does not exist in this worktree yet. busmon therefore
renders from **local view models** and keeps one thin adapter at the edge:

```go
// tracking.go
type request struct {
    Task, From, Target, Branch string
    State        string    // requested | accepted | blocked | done (board state)
    Thread       string
    Created, Expires, Accepted, Responded, Updated time.Time // zero = absent
    Delivery     string    // queued|uncertain|output_written|expired|missing|"" = unknown
    Availability string    // retained|missing|expired|"" = unknown
    Attempts     int
    DuplicatePossible bool
    BlockedReason string
}
func boardRequests(raw map[string]string, now time.Time) []request // JSON → view models
```

`boardRequests` parses the `{p}:board` hash JSON directly (HGETALL), so the
`request` object lands the moment Codex writes it — the contract's snake_case
names are the interface. When `bus.Board()` returns `Request`, the adapter
switches to the typed API and the renderer below is untouched.

Fidelity is a view model too, because the wire cannot express it yet:

```go
// detail.go
type fidelity struct{ Label, Caveat string } // e.g. "full text retained", "…"
func classify(e bus.Event) fidelity
```

## File structure

| File | Responsibility |
|---|---|
| `cmd/busmon/detail.go` | message detail overlay: metadata block, full body, fidelity, thread transcript |
| `cmd/busmon/tracking.go` | request view models + REQUESTS overlay + status-bar summary |
| `cmd/busmon/help.go` | legend overlay: every badge, colour and counter in words |
| `cmd/busmon/render.go` | existing renderers; chips reworked (declared state vs silence), health indicator |
| `cmd/busmon/main.go` | wiring: overlays, filter, agents-hash discovery, tail retry |
| `*_test.go` | pure renderer tests + `tcell.SimulationScreen` navigation/resize tests |

## Tasks

### Task 1 — the feed remembers events, not just strings
`feedLine` carries the whole `bus.Event` + entry time. No visible change; every
later task depends on it. Test: a feed line round-trips its event.

### Task 2 — message detail overlay (`Enter`)
Scrollable overlay showing author, recipient, absolute date + age, entry id,
thread id, then the full body with newlines preserved. `y` copies the body
verbatim (OSC52), `Y` copies the whole thread transcript, `Esc` closes.
Fidelity line, one of:
- `full text retained (N chars)` — report carrying `full`
- `preview only — nothing further was retained`
- `truncated at publish — only the 500-char preview exists` (preview ends in `…`)
- `completeness unverified — entry predates the completeness marker` (caveat)
- `no longer on the bus (trimmed)` — a referenced entry that XRANGE cannot find
Tests: one per branch, plus long/multiline/Unicode bodies.

### Task 3 — thread transcript in the detail
For a `cmd` event, fetch `Bus.Thread(threadID)` on open and list the retained
entries chronologically with role (`directive`/`challenge`/`reply`/`verdict`).
A thread whose root is no longer retained says so instead of hiding it.

### Task 4 — honest agent chips
Split the three facts the old chip conflated:
- **declared state** — always shown, coloured by state, never overwritten
- **age of that information** — `· 8s` fresh, `· no update 18m` when stale
- **subscription** — the 👂 lease badge, unrelated to state
`offline` as a label is removed; silence is reported as silence.
Tests: a `working` agent silent for 18m still reads `working`, plus `no update 18m`.

### Task 5 — discovery from the agents hash
Seed and refresh the agents map from `Bus.Agents()` each tick, so an agent that
last spoke before the `--limit` window still gets a chip. Test: an agent absent
from the backfill but present in the hash appears.

### Task 6 — monitor connection health
A `bus ok` / `bus unreachable Ns` indicator in the status bar, fed by the
ticker's own errors — the monitor's link, shown separately from agent liveness.
The tail loop retries from its last cursors instead of dying silently.
Tests: renderer branches; a tail error marks unhealthy and does not kill the feed.

### Task 7 — legend overlay (`?`)
Every badge, colour and counter explained in words, on demand, so the screen
stays uncluttered. Test: each rendered badge appears in the legend.

### Task 8 — REQUESTS overlay (`r`) + status summary
Tracked requests from the board: task, target, state, age, deadline **only when
`expires_at` is set**, delivery disposition in plain words, acceptance and
response times when explicitly recorded. Untracked legacy cmd threads are listed
separately and labelled as carrying no acceptance signal.
Status bar gains `N waiting · oldest 42m` when there is anything waiting.
Tests: deadline absent → no deadline column; `output_written` never renders as
received/accepted; `missing` never renders as done.

### Task 9 — filters (`/`)
Filter the feed by `@agent` or thread/subject; the title shows the active filter
and how to clear it; `Esc` returns to the full journal. Test: filtered feed
contains only matching lines and the title says so.

### Task 10 — SimulationScreen coverage
Navigation, overlay open/close, resize to 40 columns, long Unicode lines,
partial history, a silent-but-declared-working agent, Redis down.

### Task 11 — docs + reproducible demo
README pane/key reference updated; `scripts/busmon-demo.sh` seeds an isolated
throwaway project on a scratch Redis so before/after renders can be reproduced.
