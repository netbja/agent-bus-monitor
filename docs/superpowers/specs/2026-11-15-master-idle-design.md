# A guard for master left idle with work in hand (idle-master)

Status: spec, for review (subject `master-idle-spec`). Not built. Task `master-stall-guard`
(master's directive of 2026-11-15, at the person's request: "a reliable and durable fix").
§8 holds the questions for the person.

## 1. What happened on 2026-11-14/15

From the bus history (`agentbus history -a master`, `-k health`):

| Time | Fact |
|---|---|
| 23:01 | coder2 reports herdr-6c2-2 approved by both reviewers at `fa2d796f`, ready to push |
| 23:32 | master asks the person "Merger 505-512"; its status becomes `waiting: 1 question(s)` |
| 00:21 | the person answers; master reports and ends its turn. It had started its own checks (k1/k2) on two approved heads in a shell job that marks a `.done` file, not a tracked background task |
| 00:33 | the checks end. Nothing addresses master, so the dispatcher has nothing to wake |
| 09:09 | the person asks why the project stopped |

No stall guard changed state all night: `team-stall`, `review-queue`, `quiet-agent` and
`idle-implementer` stayed `ok`. Each looks somewhere else:

| Guard | Sees | Why it missed this |
|---|---|---|
| `team-stall` | everyone idle and a ready **plan task** | an approved head to push, or master's own checks, is not a plan task |
| `review-queue` | a review owed | the reviews were done |
| `quiet-agent` | a directive delivered, nothing written since | master wrote a report at 00:21 |
| `idle-implementer` | an implementer idle with requests | master is not an implementer |
| `merge-queue` | approved heads **already pushed** (a PR, or a branch tip on origin) | `fa2d796f` was never pushed |
| the dispatcher's wake | a message waiting for master | there was none |

The stale `waiting` was not the cause: `bus.WaitAnswered` already reads a `waiting` that the
person answered as `idle` from the answer's time (stale-waiting). The hole is that **work only master
can move** has no measure. This spec adds that measure, and nothing more.

## 2. The check: `idle-master`

A health check in the stall guard's family (`stallChecks`, `cmd/agentbus/stallchecks.go`), run
by the dispatch service every minute. It judges one agent: **the escalation target**
(`escalateTo()`, master in this project).

**Warn** when all of these hold:

1. master is registered and its state is `idle` or `done`, or `waiting` answered since
   (`WaitAnswered`, as `team-stall` reads it), for at least `dispatch.master_idle_after` (§5),
   counted from the status entry's id or the answer's id (Redis time);
2. a fresh observation of its pane (`Observation.Fresh`) shows it not busy;
3. it has no obligation (no pending or unread entry): when it has one, the dispatcher's own wake
   already deals with it, and the check reads `ok`, `master has messages; the dispatcher wakes it`;
4. the project is not paused, and master's provider is not held;
5. **something is in master's hand** (§3).

Otherwise:

- `ok`, with the reason: `master working`, `master idle 6m`, `master waits for you (1 ask open)`,
  `nothing in master's hand`.
- `unknown` when a read fails, or when master's pane has no fresh observation (the stall guard's
  rule: never judge an unseen pane). The record's detail says which.
- `off` when the setting is `0`.

A `waiting` that is still open (an ask unanswered) is the person's turn and reads `ok`, even with
work in hand: For you already shows the ask.

`Order` after `idle-implementer`; `Handler: "master"`; never a fail. Its detail names how long and
at most three items, then `+N`: `master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f)`.

## 3. What counts as in master's hand

Three sources, each already on the bus or one read away. Nothing else counts.

| Item | Read from | Key (for the episode) |
|---|---|---|
| **Approved, not delivered**: a head some subject's deciding approve names (`mergequeue.ApprovedHeads`), within 72 h (`ReviewQueueWindow`), that is no open PR's head, no branch tip on origin, and not in the base; a subject whose plan task is recorded done is left out (`DoneSubjects`, as `review-queue` does) | `{p}:mergequeue`, new field `undelivered` (below) | `head:<sha>` |
| **A request for master**: a board entry addressed to master in state `requested` or `accepted` | `{p}:board` | `req:<task>` |
| **A check-back due**: master said work is in flight and when to look again, and that time has passed | `{p}:checkback` (§4) | `cb:<set id>` |

**The `undelivered` field.** The merge-queue pass (`cmd/agentbus/mergequeuechore.go`, every
5 min, lease held) already has the verdicts and either the open PRs (`gh`) or origin's tips, the
fetched base and `InBase` (the git fallback). In every mode but local it computes, for the
approved heads (`ApprovedHeads`, within 72 h) that are no open PR's head and no tip in hand:
origin's tips (`mergequeue.RemoteTips`, one `git ls-remote`; the `gh` path adds this read and the
base fetch it does not do today), then `InBase` on what is left. What remains is written as
`undelivered: [{subject, head, branch, approved_at}]` in the same fenced write of `{p}:mergequeue`.
`branch` is a local branch whose tip is that head (`mergequeue.LocalTips`), or empty. In the local
forge mode the field stays empty: delivery there pushes nothing, and the existing `Items` already
list approved local tips for the person. A failed or stale pass (`ItemsAt` older than 3 passes)
makes this source `unknown`, and the check names it, never `ok` on it.

**Not counted:** asks, open merge-queue items (the person's merge), gates (the reviewers'), plan
tasks (`team-stall`), requests to other agents (`idle-implementer`).

## 4. Check-back: work in flight, said once

Master's own background work (a long test run, a build) is not on the bus, so no check can see it
end. Claude Code's tracked background tasks do wake their agent when they exit, but other clients
have no such hook, and a job started outside them (the 00:21 case) wakes nobody. One command makes
it visible, for any client:

```
agentbus checkback 30m "k1/k2 checks on 7d27262c, fa2d796f"
agentbus checkback --clear
```

- It writes one field per agent in `{p}:checkback`: `{due_ms, what, set_id}`. `due_ms` is
  Redis `TIME` plus the duration, `set_id` the time of the write, from the same `TIME`. It is an
  agent's write (no lease, like `status`). A new one replaces the old one; `--clear` deletes it.
  `retire --forget` deletes it too.
- `idle-master` counts it once `due_ms` has passed. It never expires by itself: if master forgets
  to clear it, the next nudge says so (`check-back due 3h ago: k1/k2 checks`), and master clears it.
- Any agent may set one. Only master's is counted in this spec, since only master has this check.
  Another role's field is shown in `agentbus checkback` (no argument: list) and nothing else.
- Text: `health.SafeText`, at most 200 runes.

The master role's text gains one rule (the role file and the agent-bus-master skill): *before
ending a turn with work in flight, run it as a tracked background task, or set
`agentbus checkback`*. That is the person's point 1 and 2 of 2026-11-15, in a form every client
can follow and the guard can verify.

## 5. The nudge, and how it stays quiet

**One wake to master per episode.** While `idle-master` warns, the stall guard's wake hook
(`StallWake`, as `IdleImplementerWakes`) sends master one waking directive through
`FencedCmdOnce`, mark `stall-master`, episode the first 8 bytes of SHA-256 of the sorted item
keys (§3). The dispatcher then types its fixed wake line in master's pane, and nothing else: the
text below is a bus message, never typed (the one-pane-writer rule).

```
Stall guard: master idle 22m with work in hand: approved, not pushed: herdr-6c2-2 (fa2d796f);
check-back due: k1/k2 checks. Move it, or say why it waits (an ask, or checkback with a new time).
```

**The same facts never wake twice.** One mark field per project, overwritten by each new
episode, so `{p}:quota` does not grow. A new item, or one gone, is a new set of keys and so a new
episode; a master who works and goes idle again with exactly the same items is not woken again by
this tier.

**Second tier: the person.** On a pass that warns with an episode already woken (the mark holds
that episode), and the warn has lasted at least `master_idle_after` since that wake, the hook sends
**one** notice for the person: `FencedNotifyOnceForPerson`, mark `stall-master-person`, the same
episode:

```
Stall guard: master was woken 25m ago for: herdr-6c2-2 approved, not pushed. It is idle again and
nothing moved. Look at master's pane, or tell it in busmon.
```

So one episode costs at most one wake and one notice, whatever its length.

**Master cannot act.** When `dispatch.CannotAct` says master cannot (its provider held, or its
pane last seen blocked), the hook skips the wake and sends the person's notice at once, as
`team-stall` does.

**Interplay.** A wake hook that also fires from `quiet-agent` or `idle-implementer` for the same
night is a different fact (a directive unanswered, a coder passed over), with its own mark.
`idle-master` reads nothing from them.

## 6. Settings

| Key | Env | Default | `0` |
|---|---|---|---|
| `dispatch.master_idle_after` | `AGENT_BUS_MASTER_IDLE_AFTER` | 20m | `idle-master` off |

A duration in the registry (`config/keys.go`), `ReadBy: "dispatch"`, the env rule of the other
stall durations. Its own key, not `stall_after`: the person asked for cruising speed, and master's
idle with work in hand is the costliest wait of all. busmon's C page lists it.

## 7. Tests

- **Evaluation, pure** (`health/idlemaster_test.go`), one case per rule: idle past N with each item
  kind warns; `working`, `blocked`, an open `waiting`, idle under N, a busy pane, an unseen pane
  (`unknown`), an obligation, a pause, a hold: no warn; an answered `waiting` counts from the
  answer; a check-back not yet due is not an item; a stale merge-queue pass is `unknown`; `0` is
  off. The detail caps at three items then `+N`.
- **The 2026-11-15 replay**: master `waiting` answered at 00:21, `fa2d796f` approved and not on
  origin, N = 20m: warns at 00:41 and wakes master once; the same input at 00:42 wakes nothing.
- **Episodes**: a new item makes a new episode and a new wake; a removed item too; the same items
  in another order are the same episode.
- **Second tier**: woken, idle again N later with the same episode: one person notice; the next
  pass sends nothing; cannot-act: notice at once, no wake.
- **`undelivered`**: an approved head with an open PR, a tip on origin, or in the base is left out;
  an approved head on none of these is listed with its local branch; local mode lists nothing; an
  approve older than 72 h is left out; a done plan subject is left out.
- **`checkback`**: set, replace, clear, list; the due time is Redis time; `retire --forget`
  removes it; text over 200 runes is refused.
- **Never typed**: the wake goes through `FencedCmdOnce`, and the existing pane-writer test still
  sees only the wake line.
- The setting is in the registry (registry test), with its env name.

## 8. Questions for the person

1. **The threshold.** Recommended: 20 minutes. Or 30 (master's own proposal), at the cost of
   ten more minutes per stall; or 10, at the cost of nudging master between two steps of one task.
2. **`agentbus checkback`.** Recommended: yes, it is the only way the guard can see background
   work for every client (Codex, Grok, agy, Kimi as master, not only Claude). Or no: the guard then
   sees only approved heads and requests, and background work rests on master's rule alone.
3. **The second tier.** Recommended: one notice to you per episode when master was woken and
   nothing moved. Or none: master's wake only.

## 9. Not in scope

- Watching a job's process or a `.done` file: the dispatcher never runs or reads the
  repository's work.
- Pushing, merging, or dispatching for master: the guard asks; master acts.
- Other roles idle with work in hand: `idle-implementer` covers implementers; the reviewers'
  queue is `review-queue`.
