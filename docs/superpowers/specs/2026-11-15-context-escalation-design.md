# The context alert gets a second step (context-escalation)

Status: spec, r2. r1 rejected by both reviewers: "a hand-off stands" is the existing `VoidFor`
rule, not a session match (§2); the second threshold is applied only above the effective alert
(§6). Task `context-escalation` (master's directive of 2026-11-15, at the person's
request, human directive 1794776197529-0): "the context alert has no teeth". Keep it small.

## 1. What happened on 2026-11-15

From `agentbus history -a coder`:

| Time | Fact |
|---|---|
| 21:30 | the dispatcher sends coder `context 80%: at your next stopping point commit WIP, publish your hand-off …` and master a `wake=no` notice |
| 21:30 to 21:57 | coder keeps working; its fill goes past 90%. Nothing else happens |
| 21:57 | master, noticing by itself, sends coder a second directive |

Why nothing else happened, read from the code on `origin/main` (289ce283):

1. **A working agent reads the bus only when its turn ends.** The 80% directive waits in its
   stream; the dispatcher wakes idle agents only. No hook delivers a message inside a turn. A long
   turn (here more than 27 minutes) never sees the alert. **Only the person can reach a working
   agent**: by interrupting it in its pane.
2. **The alert is one act, at one threshold.** `dispatch/contextalert.go` sends the directive and
   the notice once per episode (the session), then nothing, whatever the fill or the time.
3. **Nobody is told in a way that wakes or rings.** The notice to master is `wake=no`. The health
   check `context` (`health/health.go:108`) is a warn with `Handler: "dispatch"` and no `Person`
   and no `Reasons`: busmon shows it "in hand", so For you and the notification relay never carry
   it. The desktop notification (`Context.Notify`) rings once, at 80%, when the person may not be
   at the desk.
4. Sentinel's daily review saw a warn "in hand" and said nothing: correct by its rules.

## 2. The ladder

Three steps per episode, each once. The episode is unchanged: the session proved with the
agent's process; a fresh known fill below `dispatch.context_alert` ends it and deletes its marks.

| Step | When | What | Who sees it |
|---|---|---|---|
| 1. alert (today) | known fill ≥ `context_alert` (80) | directive to the agent; `wake=no` notice to the escalation target; desktop notification | the agent at its turn's end; master on its next wake |
| 2. **escalation** (new) | the episode has alerted, no hand-off stands for its session, **and** either known fill ≥ `context_urgent` (90) **or** the alert is older than `context_handoff_deadline` (20 min) | one directive to the escalation target **that wakes it**; one notification for the person (`FencedNotifyOnceForPerson`); the desktop notification; the `context` check goes **For you** | master; the person on ntfy/Discord and in busmon |
| 3. hand-off and clear (today) | the agent declared `agentbus handoff` | the dispatcher's existing `/clear` after a declared hand-off | nobody: it is the normal end |

**A hand-off stands** in this spec exactly as it does for the dispatcher's clear: the agent's
record in `{p}:handoff` exists and `Handoff.VoidFor(snapshot)` is false (same verified pid and
start, same proved session, and the agent has not declared `working` since the declaration).
No second rule: an agent that hands off early and keeps working has a void record, so it can
still escalate, and the deadline still counts from the alert, not from the voided hand-off.

Step 2 is one act, triggered by whichever comes first. A hand-off declared before it cancels
it; declared after, it only lifts the check (§4). No step 4: nothing repeats in the same episode.
If the client compacts by itself, the fill drops below the alert and the episode ends as today.

Why a deadline as well as a second threshold: an agent whose fill grows slowly can sit at 85%
for hours without handing off; the deadline catches it. Why the hand-off cancels: a declared
hand-off is the dispatcher's own path to `/clear`; there is nothing left to escalate.

## 3. The dispatcher (`dispatch/contextalert.go`)

### 3.1 The alert keeps its time

The alert's directive is sent with `FencedCmdOnceAt` instead of `FencedCmdOnce`, so
`context:<agent>:at` holds the entry's id (Redis time). The deadline is counted from it
(`bus.MarkAt`). An episode alerted by an older build has no `:at`: the deadline does not apply to
it (only `context_urgent` can escalate it). Its end deletes `context:<agent>:at` with the other
marks.

### 3.2 The escalation

In `contextAlerts`, for an agent whose fill is known and at or above `context_alert`, after
`alertContext`:

```
escalate when
  marks[context:<a>] == f.Session                    // this episode alerted
  and no hand-off stands for <a>      // bus.Handoffs (read once per pass) + VoidFor(snaps[a])
  and ( (urgent > 0 and f.Pct >= urgent)
        or (deadline > 0 and MarkAt(marks, context:<a>) > 0
            and now - MarkAt(...) >= deadline) )      // now = the publication's At (Redis TIME)
```

The act, each entry with its own mark in one fenced script, episode = `f.Session`:

1. `context-escalate:<agent>`: `FencedCmdOnce` to the escalation target, **wake=yes**, directive:
   `context: <agent> at <pct>%, alerted <n> min ago at <alert>%, no hand-off declared. A working
   agent reads the bus only when its turn ends; the person is told to interrupt it. Send it no new
   work; do not type into its pane. Once it declares its hand-off the dispatcher clears it.`
   Skipped when the agent is the escalation target itself (the person's notice says it all).
2. `context-person:<agent>`: `FencedNotifyOnceForPerson`: `<agent> context <pct>%, no hand-off
   after <n> min: interrupt it in its pane (Esc) and tell it to hand off now.`
3. When (2) was sent: `Context.Notify` (desktop), as the alert does.

`ErrSwapInProgress` skips the agent, as today; `ErrNotHolder` ends the pass. An unknown or aged
fill never escalates, never ends an episode (unchanged). A paused project is not judged
(unchanged: reconcile stops before).

### 3.3 The end of the episode

The marks deleted on a fresh known fill below the alert become: `context:<a>`, `context:<a>:at`,
`context-notice:<a>`, `context-escalate:<a>`, `context-person:<a>`. The mark names are functions
in `bus/context.go` (`ContextEscalateMark`), since the health check reads one (§4).

## 4. The check `context` (`health/evaluate.go`, `health/health.go`)

Evidence per agent gains `Escalated bool`: `{p}:quota` holds `context-escalate:<a>` or
`context-person:<a>` equal to the fill's session, and no hand-off stands for the agent (§2: the
record and `VoidFor` against `{p}:agents`, which `contextEvidence` reads with `bus.Handoffs`).
A hand-off that is voided afterwards (the agent works again) puts the agent back For you; its
marks are still there, and nothing is sent again.
The check reads the marks the dispatcher wrote; it never judges the thresholds itself, so
busmon, the check and the dispatcher cannot disagree.

- **warn, reason `escalated`** when any known agent is escalated: detail `escalated: coder 91%
  (no hand-off 27 min)`, the escalated agents first, then the others at or above the alert.
- warn, no reason, when agents are at or above the alert and none is escalated (today).
- ok and unknown as today.

`health.go`: the definition gains `Reasons: {"escalated": "interrupt the agent in its pane (Esc)
and tell it: hand off now"}`, so a warn with that reason is **For you** (`PlaceOf`) in busmon;
every other warn stays in hand, dealt with by `dispatch`.

**One carrier for the ring: the dispatcher's notice.** The relay (`cmd/agentbus/notifyfacts.go`)
sends a For-you warn as one event keyed `id|since`, the warn's start. For `context` that start is
the first 80% warn, so a second agent escalating inside the same warn would never ring, and the
first would ring twice (the event and §3.2 (2)). The relay skips `context`'s For-you event, as it
skips `merge-queue`'s (`notifyfacts.go:119`): its items are the per-agent, per-episode notices.

Remediation text: add "Escalated (no hand-off past the deadline or at
the second threshold): only the person can reach a working agent; interrupt it and tell it to
hand off. A hand-off declared lifts it."

Never a fail: a fail would page sentinel, which can do nothing for a working agent.

## 5. Roles

- **The person**: the only one who can reach a working agent. Told once per episode (§3.2 (2)),
  and the For-you item stays until the hand-off is declared or the episode ends.
- **Master**, woken by the escalation: sends that agent no new work; keeps its pending items for
  the next session; never types into its pane; asks the person nothing more (the person is
  already told). When the agent goes idle without a hand-off, the dispatcher's ordinary wake makes
  it read the alert: master does not resend it. Master may `agentbus ask` the person only for a
  decision the hand-off does not cover (e.g. reassigning a step). The `agent-bus-master` skill
  gains this paragraph.
- **Sentinel**: nothing new. It never reloads a working agent (unchanged), and the check is never
  a fail, so it is not paged. Its daily digest of warns already lists `context`.
- **The dispatcher**: §3; no new pane write. The `/clear` after a declared hand-off stays the only
  exception, unchanged.

## 6. Settings (`config/keys.go`)

| Name | Env | Kind | Default | Rule |
|---|---|---|---|---|
| `dispatch.context_urgent` | `AGENT_BUS_CONTEXT_URGENT` | Float | 90 | 0 = off; the registry accepts 0 to 100 (outside: its warning, the default used); applied only above the effective alert (below) |
| `dispatch.context_handoff_deadline` | `AGENT_BUS_CONTEXT_HANDOFF_DEADLINE` | Duration | 20m | 0 = off; at least 1m |

**The effective second threshold** is computed once, where both are read (`bus.ContextUrgent(getenv,
alert)`, used by the dispatcher's wiring and by nothing else): the configured `context_urgent`
when it is strictly above the effective `context_alert`, else **0 (off)**, and the dispatch
service logs once at start `dispatch.context_urgent <u> is not above dispatch.context_alert <a>:
second threshold off, the deadline still applies` (settings values, never anything else). So
`context_alert = 95` with the default 90 never escalates in the alert's own pass; the deadline
alone escalates it. A single publication that jumps over both (79% then 92%) alerts and
escalates in the same pass, in that order: the agent is already past the second threshold.

Both read by `dispatch` (wired in `cmd/agentbus/quotacmd.go`, `ContextConfig.Urgent` and
`.Deadline`). `context_alert = 0` turns the whole ladder off, as today. Both off = today's
behaviour exactly.

## 7. Tests and footprint

Tests:
- dispatch: fill 85% then 91% escalates once (directive with wake, person notice, desktop); a
  second publication at 93% sends nothing; deadline: alert at T, fill 85% at T+19m nothing, T+20m
  escalates; a standing hand-off before T+20m: nothing; a hand-off declared at T+5m then voided by
  `working` at T+6m: escalates at T+20m; a hand-off of another session or pid does not count; the agent is the escalation target: person notice only; an episode
  without `:at` escalates only on `context_urgent`; a fill below the alert deletes the five marks;
  unknown or aged fill: nothing; `ErrSwapInProgress` skips; lost lease writes nothing; both
  settings 0 = today's entries exactly.
- health: `EvaluateContext` with an escalated agent → warn, reason `escalated`, `PlaceOf` For
  you; same agent with a standing hand-off → warn without reason (in hand); with a voided one
  (working since) → escalated again; a mark for an
  older session is not escalated.
- config: `context_urgent` > 100 or < 0 and deadline < 1m invalid (default used);
  `bus.ContextUrgent`: alert 80 / urgent 90 → 90; alert 95 / urgent 90 (default) → 0; alert 90 /
  urgent 90 → 0; urgent 0 → 0; alert 0 → the ladder off.
- dispatch: alert 95, urgent default, fill 96%: the alert only, no escalation in that pass or the
  next; the escalation at the deadline; fill 79% then 92% in one publication: alert then
  escalation, once each.

Footprint (one PR):
- `dispatch/contextalert.go`, `dispatch/context.go` (`Urgent`, `Deadline`), their tests;
- `bus/context.go`: the mark functions and the two setting readers;
- `health/evaluate.go`, `health/health.go`, tests;
- `cmd/agentbus/healthchecks.go`: `contextEvidence` reads the marks, `bus.Handoffs` and the agents' snapshots (`VoidFor`);
- `cmd/agentbus/quotacmd.go`: wiring;
- `cmd/agentbus/notifyfacts.go`: skip `context`'s For-you event (§4), with a test that an
  escalation rings once, through the notice;
- `config/keys.go`: two settings;
- `skills/agent-bus-master/SKILL.md`: §5's paragraph;
- `docs/operator-notes.d/context-escalation.md`: one bullet.

No protocol change beyond the marks in `{p}:quota`; no new stream, no new pane write, no new
service.

## 8. Questions for the person

1. **Defaults**: second threshold 90%, deadline 20 minutes after the 80% alert. Coder went from
   80% to 90% in 27 minutes on 2026-11-15. Keep, or other values?
2. **The 80% notice to master**: today `wake=no`. Proposed: unchanged (master is woken at step 2
   only, so a hand-off that comes in time costs master nothing). Or wake master at 80% too?
3. **The person's notice at step 2 only**, not at 80% (the desktop ring at 80% stays). Agree?
