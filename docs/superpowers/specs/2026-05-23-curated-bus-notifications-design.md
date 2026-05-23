# Curated bus notifications — design

- **Date**: 2026-05-23
- **Status**: Approved (brainstorm + AI peer review incorporated), pending final spec review
- **Repos touched**: `agent-bus-monitor` (this repo), `adv-trading-ai`, `~/.hermes`
- **Peer review**: `2026-05-23-curated-bus-notifications-design-AI-Reviews.md` (DeepSeek, GPT, Gemini, Kimi)

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
- A chatty agent must not be able to re-spam Signal (throttle is part of the goal, not an add-on).

## Non-goals

- No change to the Signal delivery path itself (`hermes-notify` → VDR `:8644` gateway → Signal).
- No change to the inter-agent coordination channels (`hermes:cmd:*`, `status:*`) beyond adding
  the new report channel.
- No LLM judgment on the intentional path (kept verbatim).

## Decisions

From brainstorm (user choices):
1. **Division of labour**: Claudes write the substance; `hermes_laptop` decides forward-or-not and
   translates. (Not: hermes judges a raw firehose; not: rigid priority tags.)
2. **Emission trigger**: agent judgment for mid-task events **plus** a `Stop`-hook safety net that
   publishes to the **bus** (not Signal). Hermes gates both.
3. **Channel**: a dedicated `hermes:report:{agent}` channel + an `agentbus report` subcommand —
   cleanly separated from `hermes:cmd:*` and `hermes:notify`.
4. **Hermes brain — hybrid**: intentional reports relayed verbatim with a light template; only the
   auto (Stop-net) stream goes through the hermes agent (LLM) for judgment/summary.

From peer review (forks resolved by user):
5. **Payload stays `kind|message`** (NOT JSON). Consistent with the rest of the bus
   (`status`/`cmd` are `state|message`); the agent is already in the channel name, the project is
   derivable, the timestamp is added by hermes. `Parse` is isolated in `bus.go`, so a later JSON
   migration (if structured metadata is ever needed) is a contained change. **But** add input
   hardening (decision 7).
6. **Transport stays pub/sub** (NOT Redis Streams) for Phase 1. Consistent with the whole repo
   (pub/sub, zero application keys); `bus.go` already documents the pub/sub→Streams swap as a
   future one-file change. Move to Streams only when delivery guarantees (no loss while hermes is
   down, replay) become a real requirement — tracked as a Phase 3 milestone.
7. **Input hardening is mandatory, not optional**: sanitize control chars and bound length at the
   emission boundary (decision detail in Architecture §1). Rationale: `agentbus listen` prints
   `[channel] message\n` and `bus_watch.sh` reads line-by-line, so an embedded newline would
   corrupt the matcher — this is a correctness bug, not cosmetics.

## Architecture

### 1. Bus convention (`agent-bus-monitor` — the single source of truth)

- **Channel**: `hermes:report:{agent}` (per-agent, mirroring `status:{agent}` and
  `hermes:cmd:{agent}`).
- **Payload**: `kind|message`, `kind ∈ {note, auto}`. Reuses the existing first-`|` split, so
  `message` may itself contain `|`. `note` = intentional agent report; `auto` = Stop-net summary.
- **`bus/bus.go`** (one-file convention change, by design):
  - `ReportChannel(agent string) string` → `"hermes:report:" + agent`
  - `Report(ctx, r, agent, kind, message string) error` → publishes `kind|message`.
  - `Parse`: add `case strings.HasPrefix(channel, "hermes:report:")` →
    `(agent=trimmed, kind="report", state=<note|auto>, message=<text>)`.
- **`cmd/agentbus`**: new subcommand `agentbus report <agent> [--auto] <message...>`:
  - validates `<agent>` against `ValidAgents`;
  - validates `AGENT_BUS_AGENT` (when used as identity) against `^[a-zA-Z0-9_-]+$`;
  - **sanitizes** the message: strip/replace `\n`,`\r` and other control chars; **truncate** to
    ~120 chars with an `…` suffix;
  - joins trailing words, publishes via `Report` with `kind` = `note` (default) or `auto`.
- **`cmd/busmon`**: ACTIVITY pane renders report lines (busmon already `PSUBSCRIBE`s `hermes:*`),
  e.g. `[report→agent] message` in a distinct colour.

### 2. Emission (`adv-trading-ai`)

- **Per-session identity**: each Claude session exports `AGENT_BUS_AGENT=claude1|claude2` (set per
  herdr pane / worktree). Both the manual reports and the Stop hook read it.
- **Mid-task (agent judgment)**: the Claude runs
  `agentbus report "$AGENT_BUS_AGENT" "bug X corrigé"` when something is genuinely worth surfacing.
  Guidance in `adv-trading-ai/.claude/CLAUDE.md`: report milestones, fixes, blockers, soak
  start/stop — **not** routine task completion; keep it to one short line.
- **Stop-net (safety)**: the `Stop` hook stops calling `hermes-notify` and instead runs
  `agentbus report "$AGENT_BUS_AGENT" --auto "<concise summary + context>"` — e.g. last commit
  subject + short hash (the hash gives the Phase-2 LLM something to anchor judgment on). Publishes
  to the **bus** with `kind=auto`. No direct Signal ping anymore.

### 3. Hermes gate + translate (laptop, hybrid)

- **Report listener** for `hermes:report:*` (all agents). `bus_watch.sh` is generic and shared by
  the `claude1`/`claude2` watchers, so the report arm must **not** be added unconditionally — that
  would wake those sessions on reports not meant for them. Two options (decide at plan time): gate
  the new arm to the `hermes_laptop` invocation only, or give `hermes_laptop` a dedicated
  `hermes:report:*` listener separate from `bus_watch.sh`. `claude1`/`claude2` watchers stay
  unchanged.
- **`bus_watch_hdl.sh`** routes by kind:
  - `note` → **verbatim + light template** → throttle/dedup gate → `hermes-notify`
    (e.g. `"[claude1 @ adv-trading-ai] bug X corrigé"`).
  - `auto` → Phase 2 path (LLM judgment, §Phased rollout).
- **Throttle + dedup** (Phase 1, applies to the `note`→Signal step): per-agent rate limit
  (e.g. max N Signal pushes / window) and drop of identical messages within a short TTL window
  (hash of `agent+message`). Prevents a chatty/looping agent from re-spamming Signal — directly
  serves the de-noise goal.
- **Decision log** (Phase 1): append-only `~/.hermes/logs/reports.log` records every report and
  every gate decision (forward/skip + reason). Cheap observability ("what did claude2 do
  overnight?", tuning the gate). No SQLite/dashboard yet.
- **Remove** the current "forward any caught bus message to Signal" behaviour. From now on, **only**
  `hermes:report:*` feeds Signal; `hermes:cmd:hermes_laptop` and `hermes:notify` remain for
  coordination and no longer trigger user notifications.

### 4. Anti-feedback & safety

- Hermes pushes to Signal over HTTP (VDR gateway), never back onto the bus → no feedback loop.
- Reports live on `hermes:report:*`, distinct from `hermes:notify`, so hermes's own watcher does
  not self-echo.
- `hermes_laptop` is a deliberate single point of decision (user's choice). Mitigations: the
  persistent watcher loop, the append-only log, and the Phase-2 fail-open fallback below. A no-loss
  upgrade path (Redis Streams) is the Phase 3 milestone if this becomes load-bearing.

## Phased rollout

- **Phase 1 — intentional path + de-noise** (no dependency on hermes's LLM interface):
  1. `bus.go` report convention + `agentbus report` (with sanitize/truncate/validation) + busmon
     render.
  2. report listener + `bus_watch_hdl.sh` verbatim relay for `note`, with throttle/dedup + log.
  3. Repoint the `Stop` hook off Signal; remove the blanket Signal-forward in `bus_watch_hdl.sh`.
  4. Reporting guidance in adv-trading-ai `CLAUDE.md`; wire `AGENT_BUS_AGENT` per session.
  - Deliverable: noise gone; intentional `agentbus report` reaches Signal cleanly and rate-limited.
- **Phase 2 — auto safety net** (depends on the verification item below):
  1. Stop hook emits `--auto` summaries (with commit/session context) to the bus.
  2. **Keyword fast-path before the LLM**: critical keywords (`error|panic|rollback|failed|fix|
     bloquant|soak`) → forward immediately; obviously banal (`refactor tests`, `lint`) → skip;
     **only ambiguous** cases call the LLM. Cuts latency and cost; keeps intelligence where it
     helps.
  3. **Non-blocking**: the `auto` LLM call must not stall the listen loop / the `note` path
     (background the call or use a separate consumer).
  4. **Fallback** when the LLM is unreachable/slow: **fail-open** — forward the raw summary to
     Signal prefixed `[auto-non-filtré]` (better to over-notify on the safety net than silently
     drop a real problem); log the fallback.

## Open verification items (resolve during planning)

- **Hermes agent invocation interface** for the `auto` LLM step. Proposed target (from DeepSeek):
  a `hermes` subcommand, e.g. `hermes summarize --auto "<message>"`, returning JSON
  `{"forward": bool, "summary": "..."}`, called with a ~5s timeout; on timeout/error apply the
  fail-open fallback. **To verify**: whether `hermes_cli` exposes (or can cheaply expose) such a
  one-shot judge entry point on the laptop.
- **`AGENT_BUS_AGENT` wiring** per Claude session in herdr (env per pane/worktree).

## Deferred — roadmap, not now (from peer review)

YAGNI for Phase 1-2; documented so the convention can grow without surprise:

- **Structured payload (JSON)** — adopt only if severity/topic/context/`event_id` become needed;
  `Parse` isolation keeps the migration contained.
- **`event_id` / ACK / guaranteed delivery / replay** — pairs with the Redis Streams milestone
  (Phase 3). Phase-1 dedup is met by a message hash, not a full id scheme.
- **Redis Streams transport** — Phase 3, when no-loss/replay is required.
- **Go daemon replacing `bus_watch.sh`** — once throttle/async/dedup/timeout accumulate, a small Go
  consumer reusing this repo's `bus` package is the natural home (where the removed `busbridge`
  logic could live). Shell is fine for Phase 1.
- **`--silent` kind** (bus-visible, not forwarded), **severity/topic fields**, **metrics**
  (`report_total`, `report_forwarded`, `llm_gate_latency`), **SQLite persistence + timeline /
  daily cross-agent summaries** — later phases.
- **HMAC / shared-key auth** — rejected for the loopback-only single-user bus; kept only the cheap
  `AGENT_BUS_AGENT` regex validation.

## Files touched

| Location | Files |
|---|---|
| `agent-bus-monitor` | `bus/bus.go`, `cmd/agentbus/main.go`, `cmd/busmon/main.go`, README/CLAUDE.md |
| `adv-trading-ai` | `tools/bus_watch.sh`, `.claude/settings.local.json` (Stop hook), `.claude/CLAUDE.md` |
| `~/.hermes` | `scripts/bus_watch_hdl.sh` (+ Phase 2 agent invocation) |

## Testing

- `bus.go`: unit tests for `Parse` of `hermes:report:{agent}` (`note`/`auto`, messages containing
  `|`) and `ReportChannel`. (First tests in the repo.)
- `agentbus report`: invalid-agent rejection; `AGENT_BUS_AGENT` regex rejection; message with
  `\n`/`\r` is sanitized and length is bounded; round-trip publish observed via `agentbus listen`.
- Throttle/dedup: a burst of identical/rapid `note`s yields a single (or rate-limited) Signal push.
- End-to-end Phase 1: `agentbus report claude1 "test"` → busmon ACTIVITY → Signal as
  `[claude1 @ adv-trading-ai] test`; confirm a `Stop` no longer pings Signal.
