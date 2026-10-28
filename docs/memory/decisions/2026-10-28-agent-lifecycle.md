---
id: 2026-10-28-agent-lifecycle
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-28
last_verified: 2026-10-28
author: coder
confidence: verified
---

## Finding/decision

**After a declared hand-off, the dispatcher may type the literal `/clear` into an agent's pane:
the one exception to "it types only the wake line".** Bernard decided it on 2026-10-27 ("/clear
auto: Dispatcher, spec", master's directive `1793134885283-0`). A full context otherwise needs a
person at the keyboard, once per agent.

- **What it types.** Exactly `ClearText` (`/clear`), through `Runtime.Clear`, which takes no text.
  The dispatcher types two things, ever: the fixed wake line and `/clear`, never message content.
  Every dispatch test enforces it (`dispatchTypes`, in the fake runtime): a prompt that is not a
  wake line, or a `/clear` without its reservation, fails the test.
- **When.** Only for an agent whose own hand-off record stands (`agentbus handoff`, lifecycle part
  A) for its verified pid and start and its proved session, and only when every condition of
  `DecideClear` holds: the switch on, a client whose `/clear` is known (Claude Code only), not
  paused, no swap or reload intent, its provider not held, no open ask, no open review gate on
  either side (the escalation target is told once which gates hold it), declared idle since the
  hand-off, not armed, no wake being typed, and the pane showing that same process, unfocused,
  ready and not on a prompt. `IdentSession` alone never authorises a clear.
- **Once per session.** A fenced reservation (`FencedReserveClear`) marks `clear:<agent>` =
  `session|Redis TIME|report|status id` before anything is typed, and the pane is inspected again right
  before. A dispatcher without the lease types nothing; a restart never clears twice.
- **Proof, then the ordinary wake.** The clear is proved when the same pid shows a new proved
  session (the SessionStart hook, part D) and the agent wrote nothing since the reservation. Then
  one directive tells it to re-run its boot steps and read its hand-off report, and the ordinary
  wake follows. A turn that wrote around the clear gets a rebuild directive instead (plus a notice
  to the escalation target). No proof in 30 s escalates once (and rings the desktop), with no retry
  for that session.
- **Off by default for now.** `AGENT_BUS_AUTO_CLEAR=1` turns it on. The spec's default is on, after
  live checks on this machine: Claude Code's `/clear` keeps the pid, argv and environment and fires
  `SessionStart` with a new session id; `/clear` typed with the slash menu open submits `/clear`;
  `--resume` after SIGTERM restores the conversation. Until those pass, it stays off.

## When it applies

The services' dispatcher (`agentbus up`), on Linux (verified identities read `/proc`). Reloads are
the other half of the decision: `agentbus reload`, run by sentinel on a client update, master
reloading sentinel (lifecycle part C); reload types nothing.

## Evidence

- It amends the [event-driven dispatch decision](2026-10-10-event-driven-dispatch.md) (the
  dispatcher, the one automated pane writer) by this one named exception.
- [Spec](../../superpowers/specs/2026-10-28-agent-lifecycle-design.md) (parts A-D, rollout), and its
  four reviewed steps: busmon-lifecycle-1 (part D), busmon-lifecycle-2 (A), busmon-lifecycle-3a/3b
  (C), busmon-reload-deny, busmon-lifecycle-4 (B).
- Code: `dispatch/clear.go` (`DecideClear`, `clearPass`, `proveClear`), `dispatch/herdr.go`
  (`Runtime.Clear`, `ClearText`), `bus/handoff.go` (`FencedReserveClear`, `ClearMark`),
  `cmd/agentbus/quotacmd.go` (`serviceClear`). Tests: `TestDecideClearEveryConditionVetoesAlone`,
  `TestClearPass*`, `TestFencedReserveClearRedis`, `TestServiceClear`, and the invariant in
  `dispatch/testhelp_test.go`.

## Limits / what would invalidate it

- When the capped status stream has trimmed the reservation's boundary by the proof, the history
  after it is incomplete and the proof fails closed (rebuild, never resume).
- A turn that starts between the last inspection and `/clear` and writes nothing on the bus is the
  residual: Claude Code queues `/clear` after it, and that turn's work survives only in Git and
  files.
- Only gates opened within 24 h hold a hand-off (`ReviewsOfSince`): a gate's open time is its latest
  challenge entry in `:cmd`. A gate whose challenge left the capped stream is dropped only when the
  oldest retained entry is itself past 24 h; otherwise its age is unknown and it still holds.
- Another client with known `/clear` semantics would need its own entry in `clearClients` and its
  own live check.
