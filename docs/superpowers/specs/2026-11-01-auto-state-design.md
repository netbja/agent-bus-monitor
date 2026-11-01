# Automatic agent state from the client's lifecycle (design, spec only)

Status: spec for review; no code yet.
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
- It **never replaces the message**: the status keeps the agent's last message.
- It writes **nothing** when the state already matches, so there is no entry for `working` → `working`.

The check and the write are one transaction against the stored snapshot (`putSnapshot`'s WATCH/MULTI, a
new `Bus.TurnState`), so a declaration that lands in between is never overwritten.

**Identity.** The hook's process descends from the client's process, so `agentbus` resolves the agent and
proves its pid exactly as `agentbus status` does today: `AGENT_BUS_AGENT` comes from the launcher's
environment, the pid from the ancestor chain. An unproved call writes nothing. A subagent's events
(`agent_id` set in the hook's input) are ignored, as the resume hook already ignores them.

**The entry.** Each flip appends one `{p}:status` entry with `auto=turn` (an additive field), so the
Agents panel, the stall guard and the history archive read the same facts as for a declaration. busmon's
Activity folds `auto=turn` entries: they are not shown as lines, only as the state (question 1).

**The hook command.** `agentbus roles turn start|end` is an internal entry point, like
`roles report-resume`:
- It reads the client's JSON on stdin and **never prints**. `UserPromptSubmit`'s stdout becomes model
  context, and a `Stop` hook exiting 2 blocks the stop.
- It always exits 0 and runs synchronously within a 2 s bound. Detached, a start and an end could land
  out of order.
- It never fails the turn: no broker means nothing is written.

**Interplay.**
- **Hand-off:** an `idle` flip never voids a hand-off (a non-idle status does, unchanged). A turn started
  after the hand-off, such as the resume directive after `/clear`, voids it, as a declared `working` does
  today.
- **Wakes:** the dispatcher's wake line starts a turn, so the woken agent reads `working` at once and
  `idle` when its turn ends.
- **Pause and quota:** busmon's `paused`/`quota` cover is unchanged, because it reads the state whoever
  wrote it.

**Codex, Kimi, Grok.** The first code step is a trial per client: pass the two hooks for one launch,
**without editing `~/.codex`, `~/.kimi-code` or the person's home** (Codex `-c`; for Kimi and Grok,
whatever per-launch route each offers), and record a turn's status entries. A client with no such route
stays on (2). The trial's result goes in this spec before that client's code.

## (2) Clients without hooks: the observed state, shown as observed

The dispatcher's observe pass already writes `{p}:observed` (busy or ready, `since`, fresh for 120 s).
busmon gains one flag, and never rewrites the declared state:

- `busy`, unchanged: declared not working, but a fresh observation shows a running turn.
- **`idle?`, new:** declared `working`, but a fresh observation shows the pane ready, not blocked, for
  more than `display.idle_hint_after` (new key, default 10 min, `0` = off). The flag is a hint: it never
  rings, counts or relabels.

With (1) in place, both flags should be rare for hooked clients. They stay as the check that the hooks
work.

## (3) The rule, written where every agent reads it

The shared `agent-bus` skill (`skills/agent-bus/SKILL.md`) and every role prompt (`roles/*.md`) state
one rule:
- `working` at the start of each step or fix;
- `idle` while waiting on reviewers or another agent;
- `waiting` only when a person must decide;
- with hooks, the state follows the turns, and an agent still declares `waiting`, `blocked` and `done`
  itself.

A test keeps the sentence in every role prompt.

## Steps (code, each reviewed)

1. `Bus.TurnState` (compare-and-set on `idle`/`working`, message kept, `auto=turn`, no-op when equal) and
   `agentbus roles turn start|end` (stdin, silent, exit 0, 2 s, identity proved). Claude Code's
   `--settings` gains the two hooks. Launch dry-run outputs are updated byte for byte.
2. busmon: fold `auto=turn` entries in Activity, and add the `idle?` flag with its setting.
3. The rule in the skill and the role prompts, with its test.
4. Per client, after its trial: Codex, then Kimi and Grok.

## Tests (plan)

- `TurnState`:
  - `idle` → `working` → `idle`, with the message kept and `auto=turn` on each entry;
  - `waiting`, `blocked` and `done` untouched;
  - equal states write nothing;
  - a declaration between read and write wins (WATCH);
  - an unverified caller writes nothing;
  - an idle flip leaves a hand-off; a working flip after it voids it.
- `roles turn`:
  - prints nothing on stdout;
  - exits 0 with no broker, bad JSON or a subagent's event;
  - is bounded at 2 s.
- Launch:
  - Claude Code's `--settings` holds `SessionStart`, `UserPromptSubmit` and `Stop`, in that order;
  - other clients are unchanged until their trial;
  - the dry runs are updated.
- busmon:
  - `idle?` only after N of a fresh ready observation under a declared working;
  - never with a stale observation, never while blocked;
  - `0` turns it off;
  - Activity folds `auto=turn` entries;
  - goldens.
- Rule: every `roles/*.md` and the skill carry it.

## Questions for Bernard

1. **Activity:** fold the automatic flips (recommended: the Agents panel shows the state, so Activity
   stays the conversation), or show them as muted lines?
2. **`waiting` after an answer:** a person's answer starts a turn, but the rule never lifts `waiting`. The
   agent must declare `working` itself, and busmon shows `busy` meanwhile. Keep that (recommended:
   `waiting` is yours to clear), or let a turn that follows a person's answer lift it?
3. **`idle?` after:** 10 minutes?
4. **Codex's hook trust:** Codex asks once to trust new or changed hooks. The hook is the same on every
   launch, so it is trusted once per machine. Acceptable, or should the launcher pass
   `--dangerously-bypass-hook-trust` (not recommended)?
