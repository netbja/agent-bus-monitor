# Master Bootstrap — Design Spec

**Date:** 2026-07-16
**Status:** Approved (brainstorming), pending implementation plan
**Scope:** Brique 1 of a larger vision — the provisioning + spawn layer (S1+S2+S3),
plus a Haiku **steward** (daily project review + master-context nudge), cron-woken, and an
opt-in **index preflight** (codegraph / code-index) so agents boot with warm tooling.
S4 (conversational recall) and S5 (deeper knowledge tooling) are separate specs.

## Problem

The Agent Bus works, but the *master* has no way to **bring a team up** or **grow
it**. Today the human opens one herdr tab per agent by hand, plus a busmon tab, and
the existing `agent-bus-master` skill only coordinates peers that **already exist** —
it never *creates* them. We want to start a project with a master in the bus that can
pop the base agents (architect on Fable, coder on Opus, 4-eyes on Opus), each launched
with the right model and the right skills, from one keypress.

## Goals

- **One keypress from zero to a working team**: master + coder + 4-eyes + steward + busmon
  come up at boot, each armed on the bus with its role model and skills.
- **The master can pop extras on demand** (architect, a second coder, a test-runner)
  using the *same* spawn recipe as the boot path — one mechanism, not two.
- **Right competency at launch**: each role loads a curated set of skills (from the
  Matt Pocock engineering collection at `~/Tools/herdr-plugins/skills/`) plus the bus
  peer/master skills.
- **Per-role model tiering** with graceful fallback (`Fable si dispo`).
- **A Haiku steward keeps the team healthy**: a cheap always-on agent that, woken by a
  machine cron (survives reboots), writes a daily project review and *nudges* the master
  to reset its own context (hand-off → `/clear`) when it saturates — it never clears the
  master itself.
- **Warm tooling at boot** (opt-in): the MCP servers (codegraph, codebase-memory) and the
  `rtk` hook are already inherited by every agent from the global config; `bootstrap --index`
  only pre-*builds* the per-project index so the first query isn't a cold multi-minute wait.
- Live in **this repo** (`agent-bus-monitor`) — it is the tooling around the bus.

## Non-goals (deferred)

- **S4 — conversational recall**: surviving a laptop reboot with each agent's
  *conversation* restored (worktree-per-agent + `claude --continue`). Brique 1 agents
  share the repo cwd and recall re-opens the workspace with **fresh** sessions.
- **S5 — deeper knowledge tooling**: the opt-in *index build* moves into brique 1 (see
  `bootstrap --index`); S5 keeps index **freshness** (re-index policy / watch), the Obsidian
  vault as shared memory, and ADR management via codebase-memory.
- **herdr-reviewr** integration (when added, set `auto_open = false` to avoid racing
  the herdr-plus worktree layouts).
- **No polling daemon for the steward**: it stays a one-shot wake-on-exit agent driven by
  the machine cron (and ad-hoc `cmd`s), not a `Restart=always` watcher — consistent with
  the rest of the bus (see CLAUDE.md "Things that bite").

## Key decisions (from brainstorming)

| Decision | Choice | Rationale |
|---|---|---|
| Team startup model | **Hybride**: core at boot + master pops extras | Matches the "master pops agents" vision while keeping a reliable static core. |
| Core (boot) team | master + coder + 4-eyes + steward + busmon | coder↔4-eyes is the permanent TDD+review pipeline; steward is the cheap Haiku caretaker; architecture is bursty. |
| Popped on demand | architect (+ test-runner, coder#2, docs) | Design work is intermittent; surge capacity is on demand. |
| herdr-plus role | **In scope** as the declarative front door | Composes with `agent-launch` (template tabs call it); no redundant spawn path. |
| Spawn mechanism | one leaf `agent-launch`, shared by template tabs and the master's `agent-spawn` | Single source of truth for how an agent starts. |
| Architect model | `claude-fable-5`, fallback `claude-opus-4-8` | Fable si dispo, sinon Opus (not Sonnet). |
| Executing agents' permissions | `bypassPermissions` (coder/architect/4eyes); master `acceptEdits` | Unattended agents need Bash without stalls; oversight via busmon + bus 4-eyes gate. |
| Code home | this repo (`scripts/`, `roles/`, `roles.toml`, extend the master skill) | It is the bus tooling; not bus protocol, so not in `bus/`. |
| Session naming | `claude --name <project>:<role>` | Mirrors the bus namespace; makes `/resume` show *who is who* across projects → human recall path in brique 1. |
| Steward role | `steward` — `claude-haiku-4-5`, boot tier, persistent | Cheap always-on caretaker; always present so the daily cron can reach it and it can check master's health. |
| Steward wake model | machine cron → `agentbus pane steward` → pane poke | Cron survives reboots (unlike a session task); the bus pane-bridge resolves the pane live (no hard-coded `w8:p1`). |
| Master reset | **notify-only** — steward `cmd`s master to hand-off + `/clear`; master self-executes | Respects hand-off-before-clear; a subordinate never drives the pilot's pane. |
| Context signal | reuse `agentbus usage` (the status-line tee) | No new heartbeat/telemetry; the steward reads master's Ctx/session% on its cron wake. |
| Tooling delivery | inherit global MCP servers + `rtk` hook; don't re-install per project | codegraph/codebase-memory tools + the rtk command-rewrite hook already reach every launched `claude`; only the *index* is per-project. |
| Index preflight | opt-in `bootstrap --index`: `codegraph init` (CLI) + steward runs `index_repository` | Warm tools on first query, not a cold build mid-task; best-effort / non-fatal, so a project that doesn't want it just skips the flag. |

## Architecture

```
  bootstrap new <proj>                       herdr-plus fuzzy picker
     │  (provisionne, ne lance rien)             │  Enter sur <proj>  → ouvre le workspace
     │  - valide ValidName                        ▼
     │  - broker up (docker compose up -d)   [[tabs]] master  command="agent-launch master  <proj>"
     │  - link-role-skills.sh                [[tabs]] coder   command="agent-launch coder   <proj>"
     │  - écrit projects/<proj>.toml         [[tabs]] 4eyes   command="agent-launch 4eyes   <proj>"
     │  - (opt-in) install crontab           [[tabs]] steward command="agent-launch steward <proj>"
     │  - (opt-in --index) codegraph init    [[tabs]] busmon  command="busmon --project <proj>"
     ▼
  ~/.config/herdr/.../projects/<proj>.toml                    │
                                                              ▼
                              ┌─────────────────────────────────────────────────┐
                              │ agent-launch <role> <proj>   ← LA feuille partagée │
                              │  résout roles.toml → {model, fallback, perm, skills}
                              │  export AGENT_BUS_PROJECT / AGENT_BUS_AGENT
                              │  exec claude --model M --fallback-model F <perm> \
                              │       --append-system-prompt-file roles/<role>.md --name <proj>:<role>
                              │  (l'agent au boot: /skills ; agentbus subscribe ; status)
                              └─────────────────────────────────────────────────┘
                                             ▲
                                             │ herdr CLI: nouvel onglet → agent-launch
              master (Sonnet 5) ── "pop architect" ──► agent-spawn architect <proj>

  machine crontab ─ daily ─► daily-review-trigger.sh ─► pane=$(agentbus pane steward)
     (survit au reboot)                                 herdr pane send-text <pane>
                                                        "[cron] revue du jour + checke Ctx master → nudge si haut"
```

**Division of labor**: `bootstrap` *provisions* (template + skills + broker); **herdr-plus
opens** (picker / worktree layout); the **master pops** via `agent-spawn` (raw herdr CLI).
This sidesteps the "to drive herdr you must be inside herdr" bootstrap problem — the human
already lives in herdr, so opening goes through the herdr-plus picker, not our scripts.

The **steward** is a boot agent like any other (its tab runs `agent-launch steward`), but its
work is driven from *outside* the session by a **machine crontab** entry (installed opt-in by
`bootstrap`) — the same wake-on-exit trick the sibling project uses for its daily journal, with
the pane resolved live via `agentbus pane steward` instead of a hard-coded id.

## Components

### `roles.toml` — the single manifest
Maps each role to its launch parameters. Editable by hand to add a role (no code change),
mirroring how `ValidName` replaced the old `ValidAgents` allowlist.

```toml
[roles.master]
model = "claude-sonnet-5"
permission = "acceptEdits"
tier = "boot"
skills = ["agent-bus-master", "wayfinder", "to-tickets"]

[roles.coder]
model = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "boot"
skills = ["agent-bus", "tdd", "implement"]

[roles.4eyes]
model = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "boot"
skills = ["agent-bus", "code-review", "diagnosing-bugs"]

[roles.architect]
model = "claude-fable-5"
fallback = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "pop"
skills = ["agent-bus", "codebase-design", "domain-modeling", "to-spec"]

[roles.steward]
model = "claude-haiku-4-5"
permission = "bypassPermissions"   # writes/commits the daily journal → needs Bash without stalls
tier = "boot"
skills = ["agent-bus", "agent-bus-steward"]
```

`busmon` is not a role (it is the TUI, launched directly as a tab command).

### `scripts/agent-launch <role> <project>` — the shared leaf
Runs *inside* the pane and **becomes** the agent.

1. Resolve `<role>` from `roles.toml`; fail loudly on unknown role.
2. `export AGENT_BUS_PROJECT=<project>` and `AGENT_BUS_AGENT=<role>`.
3. Build the `claude` invocation: `--model`, `--fallback-model` (if set), the permission
   flag (`--permission-mode acceptEdits` or `--dangerously-skip-permissions`),
   `--append-system-prompt-file roles/<role>.md`, `--name <project>:<role>`.
4. `exec claude …` (replaces the shell so the pane *is* the agent).
5. **Dry-run mode** (`AGENT_LAUNCH_DRYRUN=1`) prints the resolved command instead of
   exec — the testability seam.

**Session naming (`--name <project>:<role>`)**: the display name shown in the prompt box
and in `claude --resume`. Format mirrors the bus namespace (`{project}:{kind}`), so
`myproj:coder` / `myproj:4eyes` read the same as the streams. It disambiguates same-role
sessions across projects in the global resume list, and gives brique 1 a **human recall
path**: after a reboot, `claude --resume` lists agents by name and the human picks the
right one — even with a shared cwd. Scripted role→session-id recall stays in S4; this only
makes the *interactive* picker usable now.

The `roles/<role>.md` system-prompt file instructs the agent to: invoke its skills
(`/tdd`, `/implement`, …), arm on the bus (`agentbus subscribe <role>` as a background
task — the wake-on-exit model, **not** a long-lived loop), and publish an initial
`agentbus status` (which records `HERDR_PANE_ID` for the master's pane bridge).

### `scripts/agent-spawn <role> <project>` — the master's pop
Thin wrapper: uses the `herdr` CLI to create a new tab whose command is
`agent-launch <role> <project>`. Requires `HERDR_ENV=1` (same precondition as the master
skill today). Exact herdr CLI resolved against the `herdr` skill / `herdr --help` at
implementation time.

### `scripts/bootstrap [new|recall] <project>` — provisioning
- **new**: validate name → ensure broker → `link-role-skills.sh` → write
  `projects/<project>.toml` (core tabs, each `command="agent-launch <role> <project>"`,
  plus a `busmon` tab). Launches nothing.
- **recall**: template exists → re-pick in herdr-plus (fresh sessions in brique 1); ensure
  broker up.
- **auto**: `bootstrap <project>` with no verb → `recall` if `projects/<project>.toml`
  exists, else `new`.
- **`--index` (opt-in)**: preflight the knowledge tooling — `which rtk` (sanity), then
  `codegraph init` if `.codegraph/` is absent (CLI, best-effort, **non-fatal**). code-index has
  no shell entrypoint, so its *build* is delegated to the steward at boot (see its playbook);
  `bootstrap` just records that the project wants indexing. `--cron` (opt-in) installs the
  daily-review crontab line (below).

### `scripts/link-role-skills.sh` — skill delivery
Idempotent. Symlinks *only the skills referenced in `roles.toml`* into `~/.claude/skills/`
from two sources: this repo's own `skills/` (the bus skills — `agent-bus`, `agent-bus-master`,
`agent-bus-steward`) and the Matt Pocock collection at
`~/Tools/herdr-plugins/skills/skills/<bucket>/<skill>`. Each role's skills then resolve via
`/skill-name`. Two runs leave the same state.

### `scripts/daily-review-trigger.sh` — the steward's cron poke
Adapted from the sibling project's `daily_journal_trigger.sh`. Runs under the **machine
crontab** (not a Claude session task → survives reboots) with a minimal env, so it exports
what it needs (`PATH`, `HERDR_SESSION`, `AGENT_BUS_PROJECT`, the Redis connection env). It:

1. Resolves the steward's pane **live**: `pane=$(agentbus pane steward)` (our bus pane-bridge —
   no hard-coded `w8:p1`; skips with a clear message + exit 0 if the steward isn't up).
2. `herdr pane send-text "$pane" "$MSG"` + `send-keys Enter`, where `$MSG` tells the steward:
   write today's review, read `agentbus usage`, and if master's Ctx/session% is high, `cmd`
   master to hand-off + `/clear`.
3. **Dry-run** (`DAILY_REVIEW_DRYRUN=1`) prints the resolved pane + message instead of sending.

### `skills/agent-bus-steward/SKILL.md` — the steward's playbook
Three procedures for a cheap Haiku caretaker:

- **Daily review**: read STATUS / journal / `git log` / `MEMORY.md`, post a one-line `agentbus
  report` summary, and (if the project keeps one) append + commit a `docs/PROJECT-JOURNAL.md`
  entry (no push) — mirroring the sibling's journal ritual.
- **Master-context nudge**: read `agentbus usage`; if master's Ctx/session% crosses the
  threshold, `agentbus cmd master "Ctx NN% — write a hand-off then /clear"`. **Notify-only** —
  the steward never injects into master's pane; master owns its own reset (its existing
  "context-window creep" gotcha handles the hand-off-before-clear).
- **Index warm-up (only if `--index` was requested)**: on first boot, if code-index isn't
  indexed yet, call `index_repository` once — an MCP tool, so an *agent* (not a shell script)
  is the right place to run it, and the Haiku model keeps it cheap. It's a one-shot; keeping
  the index *fresh* over time is S5.

### `bootstrap` crontab install (opt-in)
`bootstrap new <proj> --cron` (or a `[y/N]` prompt) installs one idempotent crontab line
(`… AGENT_BUS_PROJECT=<proj> HERDR_SESSION=<sess> <repo>/scripts/daily-review-trigger.sh`).
Re-running never duplicates the line; omitting the flag provisions everything else and prints
how to add the cron later.

### `skills/agent-bus-master/SKILL.md` — extend with "Spawn a peer"
New section documenting `agent-spawn <role> <project>`: when to pop (design work →
architect; surge → coder#2 / test-runner), and that popping reuses the exact boot recipe.

## Data flow

1. Human: `bootstrap new myproj [--index] [--cron]` → template + skills + broker ready
   (+ opt-in `.codegraph/` build + daily cron).
2. Human: herdr-plus picker → Enter `myproj` → 5 tabs open.
3. Each core tab runs `agent-launch <role> myproj` → `claude` boots → agent invokes its
   skills, arms `agentbus subscribe`, publishes status. busmon shows master/coder/4eyes/steward.
   (If `--index`, the steward runs `index_repository` once on this first boot.)
4. Master (Sonnet 5) needs design: `agent-spawn architect myproj` → new tab →
   `agent-launch architect myproj` → architect (Fable, or Opus on fallback) joins the bus.
5. Master dispatches the pipeline (existing skill): point at plan, one task in flight,
   coder implements + reports, 4eyes reviews + reports, master gates.
6. Daily, the machine cron pokes the steward's pane → steward writes the review (report +
   journal commit) and reads `agentbus usage`; if master is context-heavy it `cmd`s master to
   hand-off + `/clear`. Steward then re-arms `agentbus subscribe` and idles until the next poke.

## Error handling & edges

- **Broker down** → `bootstrap` starts it (`docker compose up -d`); clear error if that fails.
- **Fable unavailable** → `--fallback-model claude-opus-4-8` handles transparently.
- **Outside herdr** (`HERDR_ENV` empty) → `bootstrap` still provisions (template + skills +
  broker) and prints "open via the herdr-plus picker"; `agent-spawn` refuses without
  `HERDR_ENV=1`.
- **Non-durable herdr pane ids** → already handled by the master skill (re-resolve via
  `herdr pane list`).
- **Unknown role** → `agent-launch` / `agent-spawn` fail loudly (mirrors `ValidName`).
- **Shared-cwd `--continue` collision** → not exposed in brique 1 (fresh sessions);
  documented as the motivation for S4 (worktree-per-agent).
- **Steward down when cron fires** → `agentbus pane steward` returns non-zero; the trigger
  logs "steward not up, skipping" and exits 0 (no daily review that day, no crash).
- **Cron minimal env** → the trigger exports `PATH`/`HERDR_SESSION`/`AGENT_BUS_PROJECT` + Redis
  env explicitly (cron starts with almost none); a missing var fails loud in dry-run first.
- **Master saturated / unresponsive to the nudge** → the nudge is a `cmd` (wakes master's
  subscribe); if master doesn't act, the human sees the same `cmd` in busmon and can step in.
  The steward never force-clears, so a missed nudge is a no-op, never lost work.
- **Index build fails / tool missing** → `bootstrap --index` is best-effort and **non-fatal**:
  `codegraph` absent or `codegraph init` failing prints a warning and continues; the team still
  boots (agents fall back to grep/Read). Same for the steward's `index_repository` — logged, not
  fatal.
- **`--index` not passed** → no `.codegraph/` build, no `index_repository`; agents cold-build on
  first use (or never, if unused). The flag is the only switch; nothing is implicit.

## Testing

- **`agent-launch` dry-run**: assert the resolved `claude` command per role (model,
  fallback, permission flag, prompt file, name).
- **`roles.toml`**: parses; every role has an existing `roles/<role>.md`; every listed
  skill exists under the collection.
- **`link-role-skills.sh`**: idempotent (run twice → identical state, valid symlinks).
- **Generated template**: valid TOML with the expected core tabs.
- **Documented manual e2e**: picker → 5 tabs → busmon shows coder/4eyes/master/steward armed →
  master `agent-spawn architect` → architect appears (Fable, or Opus on fallback).
- **`daily-review-trigger.sh` dry-run** (`DAILY_REVIEW_DRYRUN=1`): asserts the resolved pane +
  message; a stubbed `agentbus pane steward` non-zero exit → the "skipping" path (exit 0).
- **Crontab install idempotency**: `bootstrap … --cron` twice → exactly one line.
- **`--index` preflight**: with `codegraph` stubbed, `bootstrap --index` runs `codegraph init`
  only when `.codegraph/` is absent and stays non-fatal when the stub exits non-zero (asserts
  the warning path + `bootstrap` still succeeds).

## Deliverables

```
scripts/agent-launch
scripts/agent-spawn
scripts/bootstrap                  (+ opt-in --cron crontab install, --index preflight)
scripts/link-role-skills.sh
scripts/daily-review-trigger.sh    (machine-cron poke, adapted from the sibling)
roles.toml
roles/{master,coder,4eyes,architect,steward}.md
skills/agent-bus-master/SKILL.md   (+ "Spawn a peer" section)
skills/agent-bus-steward/SKILL.md  (daily review + master-context nudge + index warm-up)
tests (dry-run assertions + TOML validity + link idempotency + crontab idempotency)
```

Generated herdr-plus templates live in the plugin config dir (not committed).

## Prerequisites

- `herdr plugin link ~/Tools/herdr-plugins/herdr-plus` (herdr-plus not yet installed).
- Matt Pocock skills present at `~/Tools/herdr-plugins/skills/` (already cloned).
- User crontab writable for the opt-in daily-review install (`crontab -l` / `crontab -`).
- For `--index`: `codegraph` CLI on `PATH`, and the codegraph / codebase-memory MCP servers +
  the `rtk` hook configured at **user scope** in `~/.claude/` (so launched agents inherit them).

## Follow-on specs

- **S4 — persistence/recall**: worktree-per-agent, `claude --continue`, scripted
  role→session-id resume (building on the `<project>:<role>` names), herdr session
  restore, herdr-plus worktree layouts.
- **S5 — deeper knowledge tooling**: index **freshness** (re-index on change / watch), the
  Obsidian vault as shared memory, ADR management via codebase-memory. (The opt-in *initial*
  index build lands in brique 1 via `bootstrap --index`.)
- **herdr-reviewr**: review sidebar with `auto_open = false`.
