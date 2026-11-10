# A Codex agent's tokens and context, counted like a Claude agent's (codex-token-attribution)

Status: proposed, for the person's review (task `idle-view-providers-1`, step 3). No code before
the answers in part 6. Base `origin/main`. This spec names functions and seams, not line numbers.

## 1. The gap

The person saw no token figure for reviewer-codex (Codex) on busmon's screensaver. Step 1
(branch `feat/idle-view-providers`) fixes the strip's account quota, which Codex already has. What
remains is per agent:

- **Tokens** (`{p}:tokens`, the U page): the dispatcher's tokens pass (`dispatch/tokens.go`)
  counts only sessions **proved** with the agent's process (`AgentSnapshot.IdentSession`), and only
  Claude Code's transcripts (`tokens.ClaudeScan`). A Codex agent has no proved session, so it is
  never counted. `tokens.CodexScan` already exists and counts Codex rollouts, but only for
  `agentbus tokens --unattributed`: nothing ties a rollout to an agent.
- **Context fill** (`{p}:usage`, the agent's page): read from Claude Code's transcripts only
  (`collectAgentUsage`).

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

The session is recorded with `Client: codex` (the record already has `Provider` and `Client`).

## 4. Counting

- **Tokens:** the tokens pass splits the recorded sessions by client. Claude's go to `ClaudeScan`
  as today. Codex's go to a `CodexScan` kept in the pass's memory, over the files
  `$CODEX_HOME/sessions/*/*/*/rollout-*-<id>.jsonl` (the id validated before any path is built,
  the files modified within the record's 8 days, the same per-pass budget). The sums go into the
  same `{p}:tokens` rows (agent, provider `openai`, day). Codex's counts then show on the U page
  and in `agentbus tokens`, and leave `--unattributed`.
- **Context fill:** from the proved session's last `token_count` event:
  `last_token_usage.input_tokens` (the request's context) against `model_context_window`, written
  to `{p}:usage` like Claude's. Part 6, Q2 asks how to show it.
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
   exact. Show it as a percentage, as the decision of 2026-10-27 does for Claude (from the
   statusLine), or as raw tokens? The recommendation is the percentage, with the raw count in the
   agent's page.
3. **Kimi, Gemini, Grok:** the explicit `no adapter` row until their clients record tokens per
   session, rather than anything estimated? The recommendation is yes.

## 7. Tests (when built)

- The hook: a Codex `SessionStart` payload with a valid id hands it to the prover; an invalid id,
  another event, or no payload hands nothing; the turn still ends; stdout stays empty.
- The pass: a recorded Codex session finds its rollout in a start-date folder other than today's;
  its `token_count` deltas land on the agent's rows by day; a Claude session in the same pass is
  counted as before; an id that is not valid builds no path.
- Context: the last `token_count` gives the fill against `model_context_window`; no event, no fill
  (never 0).

## 8. Steps (each its own subject for 4-eyes)

1. The hook hands the Codex session to the prover (`roles turn end`), with its tests.
2. The tokens pass counts proved Codex sessions with `CodexScan`.
3. Codex's context fill in `{p}:usage`, shown as part 6, Q2 decides.
