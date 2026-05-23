# Curated bus notifications — design

- **Date**: 2026-05-23
- **Status**: Approved (brainstorm), pending spec review
- **Repos touched**: `agent-bus-monitor` (this repo), `adv-trading-ai`, `~/.hermes`

## Problem

Today every Claude task completion fires a Signal notification: the `Stop` hook in
`adv-trading-ai/.claude/settings.local.json` calls `hermes-notify "...Claude task stop"` on every
stop, with **no content**. Separately, `~/.hermes/scripts/bus_watch_hdl.sh` forwards **any** bus
message it catches straight to Signal verbatim. The result is noisy, content-free pings.

The user wants the opposite: **few, relevant, content-rich notifications** ("soak running",
"bug corrigé") — and `hermes_laptop` as the single conductor that decides what reaches Signal and
phrases it for a human.

## Goals

- Kill the systematic per-task-stop Signal ping.
- Claudes emit *substance* to the bus only when something is genuinely noteworthy.
- `hermes_laptop` is the **single gate + translator** to Signal (via the VDR gateway).
- Notifications carry real content, attributed to the agent and project.

## Non-goals

- No change to the Signal delivery path itself (`hermes-notify` → VDR `:8644` gateway → Signal).
- No change to the inter-agent coordination channels (`hermes:cmd:*`, `status:*`) beyond adding
  the new report channel.
- No LLM judgment on the intentional path (kept verbatim) — see decisions below.

## Decisions (from brainstorm)

1. **Division of labour**: Claudes write the substance; `hermes_laptop` decides forward-or-not and
   translates. (Not: hermes judges a raw firehose; not: rigid priority tags.)
2. **Emission trigger**: agent judgment for mid-task events **plus** a `Stop`-hook safety net that
   publishes to the **bus** (not Signal). Hermes gates both.
3. **Channel**: a dedicated `hermes:report:{agent}` channel + an `agentbus report` subcommand —
   cleanly separated from `hermes:cmd:*` and `hermes:notify`.
4. **Hermes brain — hybrid**: intentional reports are relayed verbatim with a light template; only
   the auto (Stop-net) stream goes through the hermes agent (LLM) for judgment/summary.

## Architecture

### 1. Bus convention (`agent-bus-monitor` — the single source of truth)

- **Channel**: `hermes:report:{agent}` (per-agent, mirroring `status:{agent}` and
  `hermes:cmd:{agent}`).
- **Payload**: `kind|message`, where `kind ∈ {note, auto}`. Reuses the existing first-`|` split, so
  `message` may itself contain `|`. `note` = intentional agent report; `auto` = Stop-net summary.
- **`bus/bus.go`** (one-file convention change, by design):
  - `ReportChannel(agent string) string` → `"hermes:report:" + agent`
  - `Report(ctx, r, agent, kind, message string) error` → publishes `kind|message` to the channel.
  - `Parse`: add `case strings.HasPrefix(channel, "hermes:report:")` → returns
    `(agent=trimmed, kind="report", state=<note|auto>, message=<text>)`. (The report sub-kind rides
    in the existing `state` return slot.)
- **`cmd/agentbus`**: new subcommand
  `agentbus report <agent> [--auto] <message...>` — validates `<agent>` against `ValidAgents`,
  joins trailing words, publishes via `Report` with `kind` = `note` (default) or `auto` (`--auto`).
- **`cmd/busmon`**: ACTIVITY pane renders report lines (busmon already `PSUBSCRIBE`s `hermes:*`, so
  it receives them — just add a render case, e.g. `[report→agent] message` in a distinct colour).

### 2. Emission (`adv-trading-ai`)

- **Per-session identity**: each Claude session exports `AGENT_BUS_AGENT=claude1|claude2` (set per
  herdr pane / worktree). Both the manual reports and the Stop hook read it.
- **Mid-task (agent judgment)**: the Claude runs
  `agentbus report "$AGENT_BUS_AGENT" "bug X corrigé"` when something is genuinely worth surfacing.
  Guidance added to `adv-trading-ai/.claude/CLAUDE.md`: report milestones, fixes, blockers, soak
  start/stop — **not** routine task completion.
- **Stop-net (safety)**: the `Stop` hook (`settings.local.json`) stops calling `hermes-notify` and
  instead runs `agentbus report "$AGENT_BUS_AGENT" --auto "<concise summary>"` (e.g. last commit
  subject). Publishes to the **bus** with `kind=auto`. No direct Signal ping anymore.

### 3. Hermes gate + translate (laptop, hybrid)

- **Report listener** for `hermes:report:*` (all agents). `bus_watch.sh` is generic and shared by
  the `claude1`/`claude2` watchers, so the report arm must **not** be added unconditionally — that
  would wake those sessions on reports not meant for them. Two options (decide at plan time): gate
  the new arm to the `hermes_laptop` invocation only, or give `hermes_laptop` a dedicated
  `hermes:report:*` listener separate from `bus_watch.sh`. Either way, `claude1`/`claude2` watchers
  stay unchanged. (The listener already subscribes to `hermes:*`, which covers the new channel.)
- **`bus_watch_hdl.sh`**: route by report kind:
  - `note` → **verbatim + light template** → `hermes-notify`
    (e.g. `hermes-notify "[claude1 @ adv-trading-ai] bug X corrigé"`).
  - `auto` → **hand to the hermes agent (LLM)** which decides forward-or-not and summarizes; only
    if deemed noteworthy → `hermes-notify`.
- **Remove** the current "forward any caught bus message to Signal" behaviour. From now on, **only**
  `hermes:report:*` feeds Signal. `hermes:cmd:hermes_laptop` and `hermes:notify` remain for
  coordination and no longer trigger user notifications.

### 4. Anti-feedback & safety

- Hermes pushes to Signal over HTTP (VDR gateway), never back onto the bus → no feedback loop.
- Reports live on `hermes:report:*`, distinct from `hermes:notify`, so hermes's own watcher does
  not self-echo.

## Phased rollout

- **Phase 1 — intentional path + de-noise** (no dependency on hermes's LLM interface):
  1. `bus.go` report convention + `agentbus report` + busmon render.
  2. `bus_watch.sh` report match + `bus_watch_hdl.sh` verbatim relay for `note`.
  3. Repoint the `Stop` hook off Signal; remove the blanket Signal-forward in `bus_watch_hdl.sh`.
  4. Add reporting guidance to adv-trading-ai `CLAUDE.md`; wire `AGENT_BUS_AGENT` per session.
  - Deliverable: noise gone, intentional `agentbus report` reaches Signal cleanly.
- **Phase 2 — auto safety net** (depends on the verification item below):
  1. Stop hook emits `--auto` summaries to the bus.
  2. `bus_watch_hdl.sh` routes `auto` through the hermes agent for judgment/summary.

## Open verification items (resolve during planning, not now)

- **Hermes agent invocation interface** on the laptop for the `auto` LLM step: how
  `bus_watch_hdl.sh` hands a message to the running hermes agent (`hermes_cli`) and gets back a
  forward/skip decision + summary. Determines Phase 2 feasibility/shape.
- **`AGENT_BUS_AGENT` wiring** per Claude session in herdr (env per pane/worktree).

## Files touched

| Location | Files |
|---|---|
| `agent-bus-monitor` | `bus/bus.go`, `cmd/agentbus/main.go`, `cmd/busmon/main.go`, README/CLAUDE.md |
| `adv-trading-ai` | `tools/bus_watch.sh`, `.claude/settings.local.json` (Stop hook), `.claude/CLAUDE.md` |
| `~/.hermes` | `scripts/bus_watch_hdl.sh` (+ Phase 2 agent invocation) |

## Testing

- `bus.go`: unit tests for `Parse` of `hermes:report:{agent}` payloads (`note`/`auto`, messages
  containing `|`) and `ReportChannel`. (First tests in the repo.)
- `agentbus report`: invalid-agent rejection; round-trip publish observed via `agentbus listen`.
- End-to-end Phase 1: `agentbus report claude1 "test"` → appears in busmon ACTIVITY → reaches
  Signal with the `[claude1 @ adv-trading-ai] test` template; confirm a `Stop` no longer pings
  Signal.
