---
id: 2026-10-27-prompt-notify
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

**When an agent's pane shows its client's own prompt, the dispatcher rings the desktop, once per
episode.** Bernard decided it on 2026-10-27 ("Bureau"). A permission or limit menu in a pane
stops that agent until a person answers it in the pane. busmon already shows the flag `prompt`,
but only to someone who is looking at busmon.

- **What counts.** The observe pass reads the pane as herdr state `blocked`, for the agent's
  current pid: busmon's `prompt` flag. It is suppressed when the agent declares `blocked` or
  `waiting`, or has an open ask. It is also suppressed while the project is paused (the check runs
  after the pause check) and while the agent is being swapped.
- **Once per episode.** The episode is the observation's pid and `since` (Redis `TIME` of the
  first blocked reading by that pid). The mark `prompt:<agent>` in `{p}:quota` holds `pid:since`.
  It is set by a fenced write before the notification, so a dispatcher without the lease rings
  nothing and an episode rings at most once. It is deleted when the pane is no longer blocked by
  that pid, or when the agent leaves the observation (forgotten, identity lost); a new `since` is a
  new episode.
- **A grace: 30 s by default.** `AGENT_BUS_PROMPT_GRACE` sets it in seconds (`0` = off). A prompt
  is first seen up to one observe pass (15 s) after it opens, so it rings 30-45 s later. A
  permission answered by someone at the screen does not ring; one left alone rings well before
  the turn is lost.
- **Desktop only.** It goes through the same `notify-send` runner as the context alert. The text
  is `<project>: <agent> waits on a prompt in its pane (<pane>)`. A missing `notify-send` or a
  failed run is a logged note, never a failure, and the episode stays marked. Nothing is typed, no
  state changes, and no bus entry is written.
- **No notice to master.** Only a person can answer a prompt in another agent's pane (agents never
  type into panes), so a `wake=no` notice would give master nothing to act on. busmon already
  shows the flag.

## When it applies

The services' dispatcher, with observation on (`ObserveEvery`). It needs a desktop on the
dispatcher's host: under SSH or with no notification daemon, it is a logged note only.

## Evidence

- Master's directive `1793111712068-0` (task prompt-notify).
- Related: the [context-fill decision](2026-10-27-context-fill.md), whose alert this one shares the
  runner and the `{p}:quota` mark pattern with.
- Code: `dispatch/promptnotify.go` (`promptNotices`, `PromptGrace`), `dispatch/loop.go` (called
  after the observe pass), `cmd/agentbus/quotacmd.go` (`servicePrompt`, `desktopNotify`). Tests:
  `TestPromptNotifyOncePerEpisode`, `TestPromptNotifyGrace`, `TestPromptNotifySuppressions`,
  `TestPromptNotifyWaitsForThePause`, `TestPromptNotifyRunnerFailure`,
  `TestPromptNotifyDropsTheMarkOfAnAgentNoLongerObserved`, `TestPromptNotifyJudgesEachObservationOnce`,
  `TestServicePrompt`.

## Limits / what would invalidate it

It relies on herdr's `blocked` state for a client's own menu. A client whose prompt herdr does
not read as `blocked` never rings, and neither does busmon's flag. A grace longer than the
observation TTL (120 s) is fine: `since` is carried across passes by the same pid.
