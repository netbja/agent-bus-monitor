# A Codex agent's tokens and context, counted like a Claude agent's (codex-token-attribution)

Status: merged (PR 416); the person answered part 6 on 2026-11-10 (task
`codex-token-attribution-impl`): yes to all three; being implemented in the steps of part 8.
Base `origin/main`. This spec names functions and seams, not line numbers.

## 1. The gap

The person saw no token figure for reviewer-codex (Codex) on busmon's screensaver. Step 1
(branch `feat/idle-view-providers`) fixes the strip's account quota, which Codex already has. What
remains is per agent:

- **Tokens** (`{p}:tokens`, the U page): the dispatcher's tokens pass (`dispatch/tokens.go`)
  counts only sessions **proved** with the agent's process (`AgentSnapshot.IdentSession`), and only
  Claude Code's transcripts (`tokens.ClaudeScan`). A Codex agent has no proved session, so it is
  never counted. `tokens.CodexScan` already exists and counts Codex rollouts, but only for
  `agentbus tokens --unattributed`: nothing ties a rollout to an agent.
- **Context**, two contracts (reviewer-codex): the **fill as a percentage** is `bus.ContextFill`
  in `{p}:context`, published by the dispatcher's context pass (`dispatch/context.go`) from the
  reading `agentbus statusline` recorded for the agent's proved session (decision 2026-10-27).
  The **raw context count** is `UsageSnapshot.Ctx` in `{p}:usage`, from Claude Code's transcripts
  (`collectAgentUsage`). Both are Claude-only today.

Measured on 2026-11-10 (`agentbus agents --json`): every Claude agent has an `ident_session`;
reviewer-codex has none.

## 2. What the installed Codex gives (checked, codex-cli 0.166.0)

- **The session id reaches a hook.** Codex's `SessionStart` hook receives a JSON payload with
  `session_id` (and `hook_event_name`, `source`). herdr's own hook (`~/.codex/herdr-agent-state.sh`)
  reads it, and herdr holds it for reviewer-codex's pane (`herdr pane list`: `agent_session`
  `01a1a38b-…`, source `herdr:codex`). Codex also exports it to its children as `CODEX_THREAD_ID`.
- **The id names the session's file.** `$CODEX_HOME/sessions/2026/11/03/rollout-2026-11-03T23-33-20-<id>.jsonl`.
  The folder is the session's **start** date: a resumed session keeps appending to it, so the file
  is found by its id, never by today's folder.
- **The file has what both figures need.** Its `token_count` events carry `total_token_usage`,
  `last_token_usage` and `model_context_window`. `CodexScan` already sums the first two per day.

## 3. The proof: the same path as Claude's

The launcher already gives every Codex launch a `SessionStart` hook running `agentbus roles turn
end` (`codexHookArgs`, codex-identity-recovery). Claude Code's `SessionStart` hook
(`roles report-resume`) hands its `session_id` to a **detached prover** (`proveSessionSend`):
with the hook's process ancestry read before detaching, it proves the session only when the
agent's verified pid, with its start time, is an ancestor of the hook (`bus.ProveSession`). A
status merely naming a session never points the pass at a file.

**Proposed:** on a Codex launch, `roles turn end`, when its stdin is a `SessionStart` payload with
a valid `session_id` (`bus.ValidSessionID`), also hands it to that same prover. Same checks, same
refusals, same log. Nothing else changes in the hook: it still ends the turn, never prints on
stdout, and always exits 0. A Codex started outside the launcher (no hook) stays unproved, as
today.

**The same accepted limit** (reviewer-codex): the prover trusts the hook's ancestor chain, so a
process of the same local user, the agent itself included, can run the hook by hand and name any
valid session. The lifecycle spec (`2026-10-28-agent-lifecycle-design.md`, part D, "What it still
trusts") accepts this for Claude Code's resume hook, for the reason a forged `status` is accepted:
the bus authenticates no local process (one Redis password, the user's own files), and the worst
outcome is that the fill and the tokens follow the wrong session until the next proof, which never
moves a pid, a pane or a declared state. Codex adds no new trust; an unforgeable proof (a launcher
token) would be a change to both clients' path, outside this spec.

The session is recorded with `Client: codex` (the record already has `Provider` and `Client`).
The hook seam is `codexHookArgs` (`rolecfg/resume.go`, used by `launch.go`).

**What else reads `IdentSession`** (foureyes), and stays as it is for Codex: auto-clear acts only
for `clearClients` (`claude-code`), so a proved Codex session never gets `/clear`; the context
pass's statusline read simply misses for a Codex session until part 4 gives it a source.

## 4. Counting

- **Tokens:** the tokens pass splits the recorded sessions by client. Claude's go to `ClaudeScan`
  as today. Codex's go to a `CodexScan` kept in the pass's memory, over the files
  `$CODEX_HOME/sessions/*/*/*/rollout-*-<id>.jsonl` (the id validated before any path is built,
  the files modified within the record's 8 days, the same per-pass budget). The sums go into the
  same `{p}:tokens` rows (agent, provider `openai`, day). Codex's counts then show on the U page
  and in `agentbus tokens`, and leave `--unattributed`.
- **Context fill (`{p}:context`):** the context pass gets a second source for a proved Codex
  session: its last `token_count` event, `last_token_usage.input_tokens` (the request's context)
  against `model_context_window`, as `bus.ContextFill{Pct, ObservedAt: the event's timestamp,
  Session}`, published in the same fenced replace as Claude's. A consequence to accept: the
  context alert (ca2, `dispatch.context_alert`) then applies to Codex agents at the same
  threshold, telling them to hand off. Part 6, Q2 asks how to show it.
- **Raw context count (`{p}:usage`):** the same event's input tokens as `UsageSnapshot.Ctx`, with
  `Source: rollout`, so the agent's page shows the count beside the percentage.
- **Nothing is invented:** an agent with no proved session gets the explicit row of step 2 (`no
  token count for codex (no proved session)`), never a zero.

`CODEX_HOME` is read as the quota reader reads it (`$CODEX_HOME`, else `~/.codex`). The pass reads
only files under it, names only; no account identifier from `session_meta` reaches a structure
(`CodexScan`'s rule).

## 5. Other clients

Kimi, Gemini (agy) and Grok have quota readers, but their clients write no per-session token
records that busmon could tie to an agent. They get step 2's explicit row (`no token count for
<client> (no adapter)`). An adapter for each follows the same three parts (a hook that carries the
session id, a file named by it, a token record in it), once its client provides them.

## 6. Questions for the person

1. **Count Codex agents' tokens per agent** as in parts 3–4 (the Codex hook also proves the
   session; the dispatcher reads `~/.codex/sessions`)? The recommendation is yes.
2. **Codex's context fill:** Codex states its window (`model_context_window`), so a percentage is
   exact. Publish it as a percentage in `{p}:context`, as the decision of 2026-10-27 does for
   Claude, with the raw count in `{p}:usage`, or the raw count only? The percentage also turns on
   the context alert (hand off at the threshold) for Codex agents. The recommendation is the
   percentage and the alert, as for Claude.
3. **Kimi, Gemini, Grok:** the explicit `no adapter` row until their clients record tokens per
   session, rather than anything estimated? The recommendation is yes.

**The person's answers (2026-11-10):**

1. **Yes:** Codex agents' tokens are counted per agent, the session proved by the hook as Claude's,
   then `CodexScan` on the proved rollout.
2. **A percentage:** Codex's fill in `{p}:context`, exact from `model_context_window` (the context
   alert applies to Codex agents as to Claude's); the raw count on the agent's page.
3. **Yes:** Kimi, Gemini and Grok read `no token count (no adapter)`, nothing estimated.

## 7. Tests (when built)

- The hook: a Codex `SessionStart` payload with a valid id hands it to the prover; an invalid id,
  another event, or no payload hands nothing; the turn still ends; stdout stays empty. A resumed
  session (`source: resume`, the same id, a new process) is proved again, never refused as a stale
  proof (foureyes).
- The pass: a recorded Codex session finds its rollout in a start-date folder other than today's;
  its `token_count` deltas land on the agent's rows by day; a Claude session in the same pass is
  counted as before; an id that is not valid builds no path.
- Context: the last `token_count` gives `{p}:context`'s fill against `model_context_window` and
  `{p}:usage`'s raw count; no event, neither (never 0); a Claude agent's fill still comes from its
  statusline reading.

## 8. Steps (each its own subject for 4-eyes)

1. The hook hands the Codex session to the prover (`roles turn end`), with its tests.
2. The tokens pass counts proved Codex sessions with `CodexScan`.
3. Codex's context: the fill in `{p}:context` (the context pass's second source) and the raw count
   in `{p}:usage`, as part 6, Q2 decides.
