# Agent-Bus Peer Skill — Design Spec

- **Date**: 2026-07-03
- **Status**: Approved (brainstorm), ready for authoring
- **Scope**: One new skill file, `skills/agent-bus/SKILL.md`. No code changes.
- **Backlog item**: the "agent-bus skill for AI agents" follow-up noted in
  [[verdict-ledger-and-feedback-backlog]].

## Problem

A peer agent's only path to bus fluency today is the paste-into-`CLAUDE.md` block
(GUIDE §"Drop this into…") plus reading the full `docs/AGENT-BUS-GUIDE.md`. There is
a skill for the **master** role (`skills/agent-bus-master/SKILL.md` — driving peer
panes, unblocking, budget broadcast), but it *assumes* bus fluency it never teaches.
Nothing gives a regular peer agent on-demand, discoverable "here is how you use the
bus" guidance.

This adds the peer-agent counterpart: a lean skill that teaches the mental model and
the parts agents get wrong, and defers exact syntax to the GUIDE.

## What already exists (do not rebuild / mirror)

- `skills/agent-bus-master/SKILL.md` — 65 lines, frontmatter `name` + `description`,
  references `docs/AGENT-BUS-GUIDE.md` by relative path for the bus basics. The new
  skill mirrors this convention (in-repo `skills/<name>/SKILL.md`, GUIDE-deferring).
- `docs/AGENT-BUS-GUIDE.md` — the exhaustive, copy-paste command reference (the "4
  traps", every command's exact syntax, the wake-on-exit loop, busmon). Stays the
  **single source of exact syntax**.

## Design decisions (locked during brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Content model | **Lean orientation + GUIDE pointer** — concepts and verb names only; exact flags/syntax stay in the GUIDE (minimal sync burden). |
| 2 | Name | `agent-bus` (the peer/default skill; `agent-bus-master` remains the specialized role). |
| 3 | Location | `skills/agent-bus/SKILL.md`, mirroring `skills/agent-bus-master/`. |
| 4 | Implementation tool | `superpowers:writing-skills` (single markdown file), **not** the plan→multi-task-TDD pipeline. |

## The skill

### Frontmatter

```yaml
name: agent-bus
description: <the trigger below>
```

The `description` is the activation trigger (it decides *when* the skill fires),
verbatim:

> Use when you are a peer agent coordinating with other agents over the Agent Bus
> (Redis Streams via the `agentbus` CLI): to publish your state/heartbeat, receive
> directives by arming `subscribe`, report progress, check whether you're piloted or
> autonomous, or gate a risky action with the 4-eyes challenge/verdict flow. For the
> master/pilot role (driving other agents' panes) use `agent-bus-master` instead.

It enumerates the concrete triggers (publish state, receive directives, report,
check mode, gate) **and** disambiguates from the master skill.

### Body (~50–70 lines, stable conceptual content)

1. **When to use / one-line mental model** — you publish `status`, you read
   directives via `subscribe`, you gate risky actions with `challenge`/`verdict`; a
   human watches in busmon.
2. **Setup** — `export AGENT_BUS_PROJECT` / `AGENT_BUS_AGENT`, and the one-line sanity
   check (`agentbus notify "<self> online"` → silent exit 0).
3. **The 4 traps, condensed** — project required · flags are `--double-dash <space>
   value` (never `=`, never single dash) · positional order is fixed (message last) ·
   name regex `^[a-z][a-z0-9_-]{0,31}$`. One line each; "full detail → GUIDE §0".
4. **The loop agents get wrong** — `status` IS your heartbeat (one-shot calls, no
   daemon); `subscribe` is **wake-on-exit** (arm as a background task, one JSON object
   per fire, re-arm **iff `rearm:true`**, persist the `id` as your `--since` cursor,
   **do not** wrap it in a `while`/daemon loop); **piloted vs autonomous** (`agentbus
   pilot status` before acting — wait for a directive if piloted, proceed on your plan
   if autonomous).
5. **Gating** — `blocked` state is reserved for an open 4-eyes gate; run `agentbus
   gate <self>` before proceeding/marking done (non-zero exit = gated); `challenge` /
   `reply --ref` / `verdict` in one line each.
6. **Exact syntax pointer** — every command's precise flags and examples live in
   `docs/AGENT-BUS-GUIDE.md`; this skill deliberately does not duplicate them.

### Sync-burden mitigation

The body carries only concepts + verb **names**, never detailed flag syntax. A CLI
syntax change touches the GUIDE only; the skill's verb list changes rarely. A short
in-skill note states this ("exact syntax lives in the GUIDE — not duplicated here"),
so a future editor keeps the boundary.

## Non-goals (explicit)

- **The master role** — pane-driving/unblocking/budget-broadcast stays in
  `agent-bus-master`; this skill points to it for that role.
- **Exhaustive per-command syntax** — stays in the GUIDE (single source).
- **A cross-project distribution/install mechanism** — mirrors the master skill's
  in-repo convention; cross-project onboarding remains the `CLAUDE.md` paste-block.
- **Any code / CLI change** — this is a documentation/skill file only.

## Verification (in lieu of tests — this is a markdown skill, not code)

- Frontmatter is valid: a `name` and a single-paragraph `description` that reads as a
  clear activation trigger (per `superpowers:writing-skills` guidance).
- Every claim is accurate against the current CLI and GUIDE (verb names exist; the
  wake-on-exit / rearm / cursor / piloted-vs-autonomous behavior matches the GUIDE
  and `cmd/agentbus`).
- No detailed flag syntax is embedded (the sync-burden boundary holds); the GUIDE
  reference path is correct (`docs/AGENT-BUS-GUIDE.md`).
- `superpowers:writing-skills` self-verification passes; `go build ./...` still clean
  (guards against an accidental code touch — expected no-op).

## Files touched

- `skills/agent-bus/SKILL.md` — **Create** (the skill).
- No other files (the GUIDE, CLAUDE.md, and code are unchanged).
