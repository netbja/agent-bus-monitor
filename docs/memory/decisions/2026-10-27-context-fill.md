---
id: 2026-10-27-context-fill
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-27
last_verified: 2026-10-27
author: coder
confidence: verified
---

## Finding/decision

**busmon and the dispatcher now know each Claude Code agent's context fill as a percentage.**
This reverses CLAUDE.md's "context percentage deliberately not reported". That rule held because
a percentage needed a per-model window table that rots with every release. Claude Code now
computes it itself: its statusLine stdin carries `context_window.used_percentage` (0-100, `null`
before the first message). Verified in Claude Code 2.1.311's own statusLine documentation
(embedded in the installed binary). Nothing derives a percentage from tokens or a window size.

- **Recorded locally, by the status line.** `agentbus statusline` writes
  `~/.cache/agentbus/context/<session_id>.json` (pct, observed_at, session; atomic, 0600) when the
  value is a number. `null` or absent writes nothing, so the agent reads *unknown*, never 0.
  Files of sessions not seen for 24 h are pruned on each write. The status line makes no Redis
  call: it runs on every refresh.
- **Published by the dispatcher, fenced.** Each minute it replaces `{p}:context` (agent → pct,
  observed_at, session, `at` from Redis `TIME`). It is `PEXPIRE` 120 s like `{p}:observed`, not
  in `projectKinds`, and ignored by `HasKeysBesidesDispatch`.
- **Only the session proved with the agent's process is read.** `AgentSnapshot.IdentSession` is
  set only with an identity proof (a status with a pid, or a bind); an unverified status that names
  another session changes `Session`, never `IdentSession` (foureyes' review of ca1). A snapshot
  anyone could write never points the dispatcher at another session's file.
- **Published during a pause too, on purpose.** Unlike the observe pass, the context pass only
  reads local files and replaces its own key; a paused project still shows its agents' fill.
- **Still not reported:** cost. Raw context tokens stay in `{p}:usage`.

- **The alert (ca2): once per episode, never during a pause.** At `AGENT_BUS_CONTEXT_ALERT` %
  (default 80, `0` = off) the dispatcher sends the agent a directive (`context N%: at your next
  stopping point commit WIP and reply to <escalation target> with a hand-off`), the escalation
  target a `wake=no` notice (none when the target is the agent itself), and a desktop notification
  through `notify-send` when it is on PATH (nothing otherwise; no new dependency). Each entry is
  written with its `{p}:quota` mark in one fenced script (`FencedCmdOnce`, marks
  `context:<agent>` and `context-notice:<agent>`, value the proved session): one alert per agent
  and session, a new session is a new episode, and a fresh reading below the threshold deletes the
  marks, so the next rise alerts again. A reading older than 30 min (aged) or missing (unknown)
  neither alerts nor ends an episode. The alert runs after the pause check: a paused project still
  publishes the fill, and the episode waits for the end of the pause. The desktop notification
  follows only a directive actually written, so a restarted dispatcher does not repeat it.

What follows (busmon display, health check) is the context-alert change's ca3.

## When it applies

To any reader or writer of an agent's context fill: the status line, the dispatcher's context
pass and alert, and the busmon and health readers that come in ca3. Only Claude Code agents have a fill;
the other clients read unknown until their own client gives one.

## Evidence

- Related: the [quota decision's](../../../CLAUDE.md) statusline reader, which this one sits beside.

- Master's directive `1793095487926-0` and its accepted design and split (`1793095608409-0`).
- Claude Code 2.1.311 binary, statusLine docs: `"used_percentage": number | null, // Pre-calculated:
  % of context used (0-100), null if no messages yet`.
- Code: `quota/context.go`, `bus/context.go`, `dispatch/context.go`; tests
  `TestRecordContext`, `TestRecordContextPrunes`, `TestContextPassPublishesVerifiedSessionsOnly`,
  `TestIdentSessionFollowsTheProofOnly`,
  `TestHasKeysBesidesDispatch`; the alert (ca2): `dispatch/contextalert.go`,
  `TestContextAlertOncePerEpisode`, `TestContextAlertSkipsUnknownAgedAndOff`,
  `TestContextAlertWaitsForThePause`, `TestServiceContextAlert`.

## Limits / what would invalidate it

A Claude Code release that drops or renames `context_window.used_percentage` makes every agent
read unknown (never a wrong value); check its statusLine docs on a client update. A client that
computes the fill on another base (a different window) would change what 80 % means.
