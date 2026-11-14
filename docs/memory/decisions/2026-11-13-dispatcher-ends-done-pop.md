---
id: 2026-11-13-dispatcher-ends-done-pop
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-13
last_verified: 2026-11-13
author: coder
confidence: verified
---

## Finding/decision

**A pop that has finished is ended by the dispatcher itself: it closes the pop's pane, unregisters
it, and tells master with one notice that wakes nobody.** The person decided it on 2026-11-13
(master's directive `pop-retire-auto`, bus entry 1794602118318-0: "a finished pop (e.g. security)
must close itself, without waking master to run `agentbus retire <role> --forget --episode <ms>`").
It is the [pop-auto-retire spec](../../superpowers/specs/2026-11-03-pop-auto-retire-design.md)'s
option B, which that spec had set aside for option A (the dispatcher tells master, master runs
retire) because B is "a new class of automated act on panes ... a decision note and its own
review would be needed". This is that note.

It **amends in writing**:

- `CLAUDE.md`'s rule "the dispatcher is the one automated pane writer", which names this act. The
  dispatcher still types nothing new: closing a pane writes nothing into it;
- the [event-driven dispatch decision](2026-10-10-event-driven-dispatch.md): the dispatcher stays
  the one automated actor on panes; what it may do grows by closing a done pop's pane;
- the pop-auto-retire spec's choice of A, now superseded by B where the service reaches herdr.

What it keeps:

- **The same act as `retire --forget`**: the whole `PopDone` predicate re-judged before the begin,
  after it and right before the close; a `kind=retire` intent with its own nonce; the pane closed and
  the process proved gone; `ForgetIfClear`, which keeps the consumer group. Every write is fenced by
  the dispatch lease.
- **Never a boot role, never a running turn**, never a pop something still waits for (a message,
  an ask, a task, a gate, a mission).
- **Master's `agentbus retire` stays as it is.** With `dispatch.pop_retire` on but no herdr session
  named in the services' env, master is told to run it, as before. Off, nobody ends a pop.

- *Why:* the round trip woke master for an act whose facts are all measured; a held or busy master
  left finished pops open for hours.

## When it applies

The dispatch service and `agentbus dispatch`, with `dispatch.pop_retire` on (the default) and a
herdr session named in the env (`relaunch.ServiceHerdr`); `cmd/agentbus/popend.go`,
`dispatch.PopRetireConfig.End`.

## Evidence

- Master's directive 1794602118318-0 (the person's product decision).
- The PR that built it (pop-retire-auto), reviewed by foureyes and reviewer-codex.

## Limits / what would invalidate it

The exception covers closing the pane of a registered pop the whole predicate judges done, by the
lease holder. Closing a boot role's pane, a pane with a turn running, or any pane typed into
breaks the rule instead of extending it.
