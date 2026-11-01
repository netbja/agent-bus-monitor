# Automatic agent state from the client's lifecycle (design, spec only)

Status: spec for review, revision 3 (foureyes on bd805f3, reviewer-codex on 09a7faf); no code yet.
Task: master's directive of 2026-11-01 (`1793552718736-0`), from Bernard: the "declare working" rule must
hold for every agent, and its root cause must be fixed, not just reminded.

## The problem

The Agents panel shows the state each agent **declares** (`agentbus status`). Nothing else writes it. An
agent that forgets the call stays `idle` while it codes (Bernard saw coder do exactly that on 2026-11-01),
or stays `working` after it stopped. busmon's `busy` flag (an observed turn under a declared non-working
state) catches the first case only where panes are observed, and nothing catches the second.

## (1) The state follows the client's own turns, where the client has hooks

Every client we launch has turn hooks. Read from the installed binaries on 2026-11-01:

| Client | Version | Turn start | Turn end | How a launch can pass the hook |
|---|---|---|---|---|
| Claude Code | (installed) | `UserPromptSubmit` | `Stop` | `--settings` JSON, already passed on every launch (it carries the `SessionStart` resume hook) |
| Codex | 0.164.0 | `UserPromptSubmit` | `Stop` | hooks are a feature (`codex_hooks`, `hooks.json`), behind a one-time trust dialog (`screens/codex-hooks.txt`); `-c` overrides per launch; to verify |
| Kimi Code | 2.2.0 | `UserPromptSubmit` | `Stop` | hook events present in the binary; the config route is to verify |
| Grok | 1.0.70 | `UserPromptSubmit` | `Stop` | `.grok/hooks`, `hooks.json` (per directory); to verify |

**Rule.** At turn start the state becomes `working`; at turn end it becomes `idle`. Each flip happens only
when the stored state is `idle` or `working`:
- It **never overwrites** a declared `waiting`, `blocked` or `done`.
- It **never replaces the message**: the agent's last message is kept.
- It writes **nothing** when the state already matches.

The check and the write are one transaction against the stored snapshot (`putSnapshot`'s WATCH/MULTI, a
new `Bus.TurnState`), so a declaration that lands in between is never overwritten.

**A flip writes the snapshot only, never a stream entry** (revision 2). It updates `{p}:agents` (state, `ts`
from Redis `TIME`, and a new field `auto: "turn"` that any declaration clears), and it appends nothing to
`{p}:status`. Why:
- **Volume:** two entries per turn would push the declared statuses out of the ~1000-entry stream, and
  out of busmon's backfill, the recap and the clear proof's window, much sooner (foureyes).
- **Activity:** Activity stays the conversation, with nothing to fold. This replaces revision 1's
  question 1.
- **Other readers:** the readers of the stream (quiet-agent, `proveClear`'s `StatusAfter`, the archive)
  see only what agents declared.

The Agents panel, the freshness bar and the stall guard read the snapshot, so they follow the flips.

**Prompts that start no model turn are ignored** (revision 2, foureyes' blocking 1).
- A prompt that begins with `/` writes nothing at turn start: `/clear`, `/compact`, `/model` and the
  others. The hook's input carries the prompt.
- Without this rule:
  - the dispatcher's typed `/clear` would flip the agent to `working`;
  - that flip would void the hand-off (`Handoff.VoidFor`: working since) and would make the clear's proof
    read "a turn ran", which sends the rebuild directive instead of the resume;
  - a local command gets no `Stop`, so the agent would stay `working`.
- A custom slash command that does run the model is not flipped either: its `Stop` writes `idle` (a no-op).
  busmon's `busy` flag shows the turn meanwhile.

The per-client trial records whether turn start fires for a typed `/clear` at all.

**Identity.** The hook's process descends from the client's process, so `agentbus` resolves the agent and
proves its pid exactly as `agentbus status` does today: `AGENT_BUS_AGENT` comes from the launcher's
environment, the pid from the ancestor chain. An unproved call writes nothing. A subagent's events
(`agent_id` set in the hook's input) are ignored, as the resume hook already ignores them. Codex's
shared app-server may run hooks outside the bound pid's chain; the trial checks it (foureyes).

**The hook command.** `agentbus roles turn start|end` is an internal entry point, like
`roles report-resume`:
- It reads the client's JSON on stdin and **never prints**. `UserPromptSubmit`'s stdout becomes model
  context, and a `Stop` hook exiting 2 blocks the stop.
- It always exits 0 and runs synchronously, the whole call bounded at 2 s, **the dial included**
  (`bus.ConnectContext` under that deadline), so a down broker costs at most that per prompt.
- It never fails the turn: no broker means nothing is written.

## (2) A turn that ends without its hook, and clients without hooks

**Stuck `working`** (revision 2, foureyes' blocking 2). `Stop` does not run in every case:
- when the person interrupts a turn (Esc);
- when an API error ends a turn;
- when the client is killed.

The agent would then read `working` until its next turn ends. Two readers must not be fooled, and no
stored state is ever rewritten:

- **Effective state:** `bus.EffectiveState(snap, observation, now)` reads an `auto: "turn"` `working` as
  `idle` once a fresh observation (120 s) has seen the pane ready, not blocked, for `turn_stale_after`
  (2 min). A declared `working` (no `auto`) is never read that way.
  - The stall guard's `team-stall` uses the effective state for its "every agent idle".
  - **Its time** (revision 3, reviewer-codex): an effective `idle` carries its own since, the
    observation's ready `since`, bounded below by the flip's `ts`: `max(snap.TS, observation.Since)`.
    The idle clock starts when the pane was first seen ready, never at the turn's start. For example, a
    turn flipped at 09:00, killed at 10:00 and seen ready from 10:00 reads `idle` at 10:02, idle since
    10:00. It is 2 minutes idle, not 62, so team-stall cannot fail on it before its threshold.
  - The dispatcher's `Decide` needs nothing: it already judges the pane through herdr's inspection
    (`in.State`), never the declared state, so a stuck declared `working` never delays a wake.
- **A new session:** the `SessionStart` hook (already on every Claude Code launch) also runs
  `roles turn end`, so a relaunched or resumed client starts `idle` from `working`.
- **Failure event:** where a client has a turn-failure event, it runs `roles turn end` too. The trial
  lists them per client.

**busmon** shows what is observed and never rewrites the declared state. Both flags are kept for hooked
clients too, as the check that the hooks work:
- `busy`, unchanged: declared not working, but a fresh observation shows a running turn.
- **`idle?`, new:** declared or flipped `working`, but a fresh observation shows the pane ready, not
  blocked, for more than `display.idle_hint_after` (new key, default 10 min, `0` = off). It is a hint:
  it never rings, counts or relabels.
- **The flip's source:** a flipped state shows its kept message muted with `(turn)` after it, so
  "approved; nothing pending" under a turn's `working` does not read as the agent's own word (foureyes).

## (3) The stall guard keeps its eyes (revision 2, foureyes' blocking 3)

The wake line starts a turn, so every woken hooked agent would flip to `working` at once.
- **quiet-agent** counts the agent's activity from the streams (status, report, cmd). Flips write no
  stream entry, so they are not activity: an agent that drains and then does nothing is still judged
  quiet.
- **team-stall's "idle for N"** reads the snapshot. A `Stop` flip to `idle` starts that clock, which is
  right. A stuck flip reads `idle` through the effective state above.

## (4) The rule, written where every agent reads it

The shared `agent-bus` skill (`skills/agent-bus/SKILL.md`) and every role prompt (`roles/*.md`) state
one rule:
- `working` at the start of each step or fix;
- `idle` while waiting on reviewers or another agent;
- `waiting` only when a person must decide;
- with hooks, the state follows the turns, and an agent still declares `waiting`, `blocked` and `done`
  itself.

A test keeps the sentence in every role prompt.

## Steps (code, each reviewed)

1. `Bus.TurnState` (compare-and-set on `idle`/`working`, message kept, snapshot only, `auto: "turn"`,
   no-op when equal; a declaration overrides an `auto` snapshot whatever their `ts`, foureyes:
   `Status` XADDs, then writes the snapshot with the entry's time, so a flip landing between the two
   with a later `TIME` must not win), `bus.EffectiveState`, and `agentbus roles turn start|end` (stdin, silent, exit 0,
   2 s with the dial, identity proved, `/` prompts and subagents ignored). Claude Code's `--settings`
   gains `UserPromptSubmit` and `Stop`, and `SessionStart` also runs `turn end`. Launch dry runs are
   updated byte for byte.
2. Stall guard: `team-stall` reads the effective state; a quiet-agent test with a flipped `working`.
3. busmon: the `idle?` flag and its setting, and the `(turn)` mark.
4. The rule in the skill and the role prompts, with its test.
5. Per client, after its trial (hook route, `/clear` firing, failure event, Codex's app-server chain):
   Codex, then Kimi and Grok.

## Tests (plan)

- `TurnState`:
  - `idle` → `working` → `idle` in the snapshot, with the message kept and `auto` set;
  - **no `{p}:status` entry is written**;
  - `waiting`, `blocked` and `done` are untouched;
  - equal states write nothing;
  - a declaration between read and write wins (WATCH);
  - a declaration clears `auto`;
  - a declaration overrides an `auto` snapshot even when the flip's `ts` is later (the race between
    `Status`'s XADD and its snapshot write);
  - an unverified caller writes nothing.
- `EffectiveState`:
  - a flipped `working` with a fresh ready observation over 2 min reads `idle`;
  - a declared `working` never does;
  - a stale observation, a blocked pane, or a busy one keep `working`;
  - its since is `max(snap.TS, observation.Since)`: a long turn killed after an hour, then seen ready,
    reads idle since the ready observation, and team-stall stays under its threshold.
- `roles turn`:
  - prints nothing on stdout;
  - exits 0 with no broker (within 2 s, the dial bounded), bad JSON or a subagent's event;
  - **a `/clear` prompt writes nothing**.
- Auto-clear: a flip from `/clear` neither voids the hand-off nor makes `proveClear` read a turn (with
  the ignore rule); the resume directive's turn still voids it.
- Stall guard:
  - `team-stall` sees a stuck flipped `working` as idle through the effective state;
  - **quiet-agent still warns** for a hooked agent woken by a directive whose only change is a flip.
- Launch:
  - Claude Code's `--settings` holds `SessionStart` (resume, then `turn end`), `UserPromptSubmit` and
    `Stop`;
  - other clients are unchanged until their trial;
  - the dry runs are updated.
- busmon:
  - `idle?` only after N of a fresh ready observation under `working`, hooked or not;
  - never stale, never while blocked;
  - `0` turns it off;
  - the `(turn)` mark;
  - goldens.
- Rule: every `roles/*.md` and the skill carry it.

## Questions for Bernard

Revision 1's question 1 (fold flips in Activity) is moot: flips write no Activity line. foureyes agrees
with the recommendations of questions 2 and 4.

1. **`waiting` after an answer:** a person's answer starts a turn, but the rule never lifts `waiting`. The
   agent must declare `working` itself, and busmon shows `busy` meanwhile. Keep that (recommended:
   `waiting` is yours to clear), or let a turn that follows a person's answer lift it?
2. **`idle?` after:** 10 minutes? And `turn_stale_after`, the effective-state threshold, 2 minutes?
3. **Codex's hook trust:** Codex asks once to trust new or changed hooks. The hook is the same on every
   launch, so it is trusted once per machine. Acceptable (recommended), or should the launcher pass
   `--dangerously-bypass-hook-trust` (not recommended)?
