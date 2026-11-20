---
id: 2026-11-20-lost-turn-notice
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-20
last_verified: unknown
author: architect
confidence: unknown
---
# A turn that ends without its Stop hook while work is open: one notice to master, no auto-wake (waiting for the person)

## Finding/decision

**What happened.** On 2026-11-20 at 10:17, a network cut ended coder's turn mid-task. Claude
Code printed `API Error: Can't reach the API server (EAI_AGAIN)`, then sat at "Resend your
message to try again". Nothing resumed coder for about 25 minutes.

**Why nothing did, on `dev` as of 6e0149f4.**
- The turn ended without its Stop hook, so coder's snapshot kept the `working` that its turn
  hook had set.
- `bus.EffectiveState` reads such a turn as idle once the pane has been seen ready for
  `dispatch.turn_stale_after` (2 min). The same rule covers Esc and a killed client.
- The dispatcher wakes an agent only for a waiting message. Coder had already drained its
  directive, so nothing was waiting.
- **No check covers it.** The team-stall check (`dispatch.stall_after`, 15 min) reports a stall
  only when no work is open: `health.EvaluateTeamStall` returns OK as soon as the board holds an
  accepted request. `dispatch.idle_after` belongs to the idle-implementer check, which concerns
  a request nobody has accepted yet. An agent whose turn died with accepted work stays idle
  until a person or another agent notices.

**The proposal (not specced, not built).**
- **When it fires.** The dispatcher sees an agent's turn-hook `working` first read as idle, by the
  `EffectiveState` rule.
- **The condition.** The agent has open work: a board request it owns in `accepted` or `working`.
- **What it does.** It sends master **one** notice per episode through `FencedCmdOnce`. The
  episode is the agent, its pid and the time of the turn hook's `working`. The notice reads:
  "`<agent>`'s turn ended without its Stop hook at `<t>` (API error, Esc or a killed client); it
  reads idle with open work".
- **Master decides.** Master resends the directive or asks the agent. The resend wakes the agent
  through the normal path.
- **The dispatcher's writes do not change.** It still types only the fixed wake line.
- **If master is the agent whose turn was lost,** the notice goes to master **without a wake**
  (`FencedCmdOnce`'s `noWake`). A wake there would be the automatic wake this note rules out,
  since the person may have pressed Esc in master's pane. Master reads the notice at its next
  wake, and busmon shows it. Whether master's own lost turn should wake it is the person's
  decision (open question below).
- **No new setting.** The notice uses `turn_stale_after`.

**What it deliberately does not do.**
1. **No screen-text matching.** "API Error … EAI_AGAIN" and "Resend your message" are one
   client's wording, in one version. The screen classifier keeps prompt signatures per client;
   an error-text list would need an entry for every client and every release.
2. **No auto-wake.** A person's Esc is the same fact, a turn ended without its hook on a ready
   pane, and an automatic wake would undo a deliberate stop. A wrong notice costs master one cheap
   message; a wrong wake takes the pane from the person.
3. **No "network is back" detection.** The dispatcher reaches Redis locally, not the model's
   API. A wake into an API that is still down loses one more turn.

A typed "continue" is not a risk: the wake line is an ordinary prompt, and the client resumes
from its context. The risk is only waking when nobody should.

## When it applies

- **When to build it.** Now possible: the dispatch service's move to `internal/dispatchsvc` merged
  in #643, with the reader it extends (`stallchecks.go`).
- **The person decides** whether to build it; master routes the spec, then coder.
- **Open question 1:** whether "open work" should also count a directive drained since the agent's
  last report. Today, master's directives are not always board requests. Counting them catches
  more lost turns. It also needs a read of the cmd and report streams that the stall check does
  not do now.
- **Open question 2 (the person's):** whether master's own lost turn should wake master. The
  proposal says no: the notice is recorded without a wake. Yes would resume master unattended,
  but it would also override an Esc the person typed in master's pane.

## Evidence

- The state rule: [bus/effective.go](../../../bus/effective.go) (`EffectiveState`) and its
  setting `dispatch.turn_stale_after` in [config/keys.go](../../../config/keys.go).
- The team-stall reader, which already applies it:
  [internal/dispatchsvc/stallchecks.go](../../../internal/dispatchsvc/stallchecks.go), and its evaluator
  [health/stall.go](../../../health/stall.go) (`EvaluateTeamStall`: OK while work is open;
  `dispatch.stall_after`).
- The once-per-episode write: [bus/once.go](../../../bus/once.go) (`FencedCmdOnce`).
- The screen classifier this stays out of:
  [the agent-screen-blocked spec](../../superpowers/specs/2026-11-17-agent-screen-blocked-design.md).
- The decision it builds on: [event-driven dispatch](../decisions/2026-10-10-event-driven-dispatch.md),
  under which the dispatcher is the one pane writer.
- The incident: master's directive of 2026-11-20 (bus cmd `1795166919661-0`), which quotes coder's
  screen.

## Limits / what would invalidate it

- The screen text comes from master's report; the pane itself was not read.
- The notice cannot tell an API error from Esc. If the person wants the two told apart, the
  client has to say so: a hook on the error, which Claude Code does not offer today (not
  verified for every client), not the screen's words.
- If the person wants an automatic resume after all, it belongs behind an opt-in setting, with
  backoff, once per episode. It should wake only after master has not acted on the notice.
