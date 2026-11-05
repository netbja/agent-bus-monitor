# A `waiting` the person has answered no longer counts as waiting (stale-waiting)

Status: approved (subject `stale-waiting`, 5a9b441, both reviewers); implemented on this branch (subject `stale-waiting-impl`). The task comes from master after a
two-hour team stall on 2026-11-05 (22:28 to 00:20).

## 1. The stall, and its root cause

`agentbus ask` stores the ask, then declares the asking agent `waiting` (`askcmd.go`: "the ask is
the record, waiting only shows it"). Only the agent lifts that declaration. On 2026-11-05 the
person answered master's last ask, and master did not declare anything after it. Its snapshot kept
`waiting` for two hours.

- **team-stall and review-queue** start with `teamIdle` (`health/stall.go`). `teamIdle` settles a
  pass as ok on any agent declared `working`, `blocked` or `waiting`: "master waiting", ok.
  Two reviews were owed. review-queue never reached them, and team-stall never reached the plan,
  so nobody was woken.
- **busmon** already reads such a wait as `answered` (`waiting.go`: `answered`,
  `waitingAnswerAt`). An ask's answer is a `human=yes` reply addressed to the agent, so the agent
  row read `answered`, muted. But that reading lives only in busmon. The dispatcher's checks and
  the notifications chore read the declaration alone.

## 2. The choice: judge from the record (1), never lift automatically (2)

**Chosen: (1).** A `waiting` whose wait the person has answered, with no open question left, is
read as `idle` from the moment of that answer. The stall guards read it that way, and so do the
notifications. busmon keeps reading it as `answered`. Nothing is written. The declaration stays
as the agent left it.

**Why not (2), an automatic lift when the last open ask is answered:**

- **It writes a declaration on the agent's behalf.** The bus has one rule here: what is observed
  or inferred never replaces what is declared. The same rule covers the `{p}:observed` flags,
  `EffectiveState`'s stuck turns (`auto-state`) and busmon's `answered`. Only the agent lifts
  `waiting`, and the role prompts say so. A lift written by `Answer` or by the dispatcher would be
  the first exception.
- **The wait may be about more than the ask.** An agent can declare `waiting` again after asking
  ("waiting for the person to merge PR 320"). A lift tied to the ask's last answer would erase
  that newer wait, unless the writer compared declaration times. That comparison is the rule (1)
  applies anyway, without a write.
- **A status entry has side effects.** It lands in Activity, moves `DeclaredAt` and voids a
  hand-off of any non-idle kind. It can also race the agent's own next declaration, since the
  person's answer wakes the agent. A derived reading has none of these effects and can never be
  wrong in the stored record.
- **(1) also covers the paths (2) would not:** an answer by a plain human message, and a reply on
  the agent's thread. Those are exactly the paths busmon already counts.

## 3. The rule, shared: `bus.WaitAnswered`

busmon's `waitingAnswerAt` moves into `bus` (`bus/waiting.go`) as a pure function, extended with
the asks:

```go
// WaitAnswered reports when a person answered agent's waiting declared at
// declaredMs, or 0 when nothing proves it. open is the agent's open questions.
func WaitAnswered(agent string, declaredMs int64, open int, asks []Ask, cmds []Event) int64
```

**It returns 0 (still waiting) when:**

- `open > 0`, meaning an open ask still holds a question;
- the agent is not declared `waiting` (the caller checks this);
- or nothing below proves an answer.

**Otherwise, it returns the earliest of these answers dated strictly after `declaredMs`:**

1. **A retained `cmd` that is `human=yes`, addressed to the agent**, or a `human=yes` reply whose
   `ref` is a thread the agent rooted. This is today's `waitingAnswerAt`, unchanged. Same-millisecond
   entries do not count, as today.
2. **An ask from the agent in state `answered`.** Its time is its latest answer that is not
   withdrawn (`AskAnswer.At`, Redis `TIME`). This one is new. It still holds when the reply has
   left the 1000-entry `cmd` stream, because the asks hash keeps an answered ask for 7 days
   (`askRetention`).

**What does not count:**

- **An ask the agent withdrew itself** (`withdrawn`, or only withdrawn answers). The agent was
  acting, so its next declaration is its own to make.
- **An `expired` ask, and a `replaced` one.** The replacement is open, so `open > 0` anyway.

**Any person's message to the agent after the wait counts as an answer:** an unrelated
directive, or the agent's share of an `@all`. That is busmon's rule today, and this spec keeps
it. A wait is meant for the person, so the person writing to that agent is the best signal the
bus holds. If the message did not answer the wait, the guards read the agent as idle and may
wake it, or master, too early. That is the safe direction: a stall woken too soon costs one
turn, while a stall that nobody wakes cost two hours here.

The answer happened after the declaration. So an agent that asks again, or declares `waiting`
again, is waiting again, because its new declaration postdates every answer.

## 4. The stall guards

**Where it runs.** team-stall (`stallchecks.go`) and review-queue (`reviewqueue.go`) read the
team the same way: `readTeam`, then `readOpenWork`, which takes `readTeam`'s team map. Neither
reads the `cmd` stream today; the 2000-entry read in `stallchecks.go` belongs to quiet-agent, a
separate check. `readOpenWork` reads the asks but does not return them.

So a new step, `answerWaits`, runs after `readTeam` and `readOpenWork`, before `Evaluate*`, in
both checks. It rewrites the waiting agents in `tr.agents`.

- **It reads only when someone is waiting.** If no non-dormant agent's effective state is
  `waiting`, it reads nothing and changes nothing. A failed read can then never turn a pass
  unknown when nobody waits.
- **When someone waits,** it reads the asks and the recent `cmd` stream (2000 entries, as
  quiet-agent does) once.
- **For each agent whose effective state is `waiting`,** the reader calls `WaitAnswered` with its
  declaration's millisecond (`snap.DeclaredTS()`; a turn flip never writes `waiting`, so it is the declaration's `TS`).
- **If the answer is proved,** the agent enters `StallAgent` as `State: "idle"`, with `DeclaredAt`
  set to the answer's time and the new field `AnsweredWait: true`. The team's idle clock starts
  when the person answered, not when the wait was declared. Otherwise the two hours of waiting
  would count as idle and the guard would fire at once.

`teamIdle` itself does not change. It shares the reading with **review-queue**
(`cmd/agentbus/reviewqueue.go`), which builds its agents through the same team read.

**Details:**

- **team-stall names the answered wait** in its fail or warn detail:
  `team idle 18m (master's wait answered 22:30), nothing open; next ready: …`.
- **review-queue** names it the same way.
- **A failed read of the asks or the `cmd` stream** while an agent waits makes the pass unknown,
  with the phrase "asks unavailable" or "cmd stream unavailable". It never guesses that the wait
  was answered.

**The person's plan tasks need no special case.** If master's stale wait was really the person's
plan task, team-stall now reaches its own `PersonReady` branch. That is the warn
`waiting-person`, for you. It replaces an ok that said "master waiting" and woke nobody.

**quiet-agent and clear-stall do not change.** quiet-agent never excused a declared `waiting`.
clear-stall reads hand-offs.

## 5. Notifications

`notifyfacts.go` raises a `decision` event per agent declared `waiting` (episode
`wait:<agent>|<ts>`). An answered wait is no longer one:

- the chore passes the same `WaitAnswered` over the asks and the `cmd` stream it already reads;
- the episode's field is pruned as a fact that is gone, by `notify.Decide`'s own rule;
- no message is sent.

The chore reads the asks today but not the `cmd` stream, so it adds one read of the stream's last
2000 entries per pass (the guards' figure, `stallCmdScan`). A failed read sends nothing, as every failed read does
today.

The same chore raises a `prompt` event for a pane blocked on its client's prompt. It skips an
agent declared `waiting` (`notifyfacts.go`, `s.State == "waiting"`), the same silence as
`promptnotify`. It applies `WaitAnswered` there too, since the facts are in hand: an answered
wait no longer suppresses the prompt event. This needs the `cmd` read, so the chore reads the
stream only when an agent is declared `waiting`, as the stall guards do.

## 6. busmon

The display stays the same. `answered(a)` keeps reading `answered`, muted. The fade, the bell, the
header count, For you, the recap and the dormancy rule all read the same rule they read today.
The only change is the source:

- `waitingAnswerAt` is replaced by `bus.WaitAnswered`, fed `st.cmdHistory` and the asks busmon
  already polls;
- so a wait answered through an ask whose reply left the 1000-entry history still reads
  `answered`.

## 7. reviewer-codex's role prompt

Master adds a rule to `roles/reviewer-codex.md`, under "Reviewing":

> Never end a turn without an `agentbus verdict` for each open challenge on you (`agentbus gate
> reviewer-codex`). If a review is unfinished, record `reject` with what is unfinished, then
> continue it in a later turn. A verdict on a newer head replaces it.

The prompt calls the verdict `approve|reject`. "CHANGES" is the word in the reply, so the rule
names both: the verdict `reject`, the reply `CHANGES`.

## 8. Tests (test-first)

- **`bus.WaitAnswered`**, as a table test:
  - an answered ask after the declaration;
  - an answered ask before it (an agent asking again);
  - an open question;
  - withdrawn only;
  - expired;
  - replaced with the replacement open;
  - a human message to the agent;
  - a human reply on the agent's thread;
  - the same millisecond;
  - the earliest of two proofs;
  - the reply trimmed out of the `cmd` stream while the ask still holds the answer.
- **`EvaluateTeamStall` and `EvaluateReviewQueue`**, with an `AnsweredWait` agent:
  - a stall fails when the answer is older than `stall_after`;
  - the check stays ok ("idle only Nm") when it is newer;
  - the detail names the answered wait.
- **The reader, through `stallReader` on a fake bus:**
  - master `waiting` and its ask answered gives `idle` from the answer;
  - master `waiting` with no proof keeps `waiting`;
  - a failed asks read is unknown.
  - Mutants: drop the `open > 0` guard, compare answer ≥ declaration, ignore the ask source.
- **notifyfacts:**
  - an answered wait raises no `wait:` event;
  - an answered wait no longer suppresses a `prompt` event, while an unanswered one still does;
  - with nobody waiting, there is no `cmd` read.
- **answerWaits (both checks):**
  - with no waiting agent, it makes no read, even when the fake bus would fail one;
  - with one waiting agent and a failed `cmd` or asks read, the pass is unknown.
- **busmon:** the existing `answered` tests pass unchanged on the moved function. One new case
  covers an ask answer with the reply out of history.

## 9. Deliverables

- `bus/waiting.go`: `WaitAnswered` and its table test;
- `answerWaits` in team-stall and review-queue;
- notifications;
- busmon's source;
- `roles/reviewer-codex.md`;
- **CLAUDE.md**, as master promised the person: the stall guard paragraph gets a sentence saying
  a `waiting` answered by the person reads as idle from the answer (`bus.WaitAnswered`), and the
  notifications paragraph the same for the `wait:` and `prompt` events. The regression test is
  the reader test of §8: master `waiting`, its ask answered, gives `idle` from the answer, and
  team-stall reaches the plan.

## 10. Not in scope

- `dispatch/promptnotify.go` (the dispatcher's desktop ring) still suppresses the ring for an
  agent declared `waiting`, so a pane prompt under a stale wait stays silent there. The
  notifications chore's `prompt` event is fixed in §5. The ring would need the asks and the `cmd`
  stream in the dispatch pass, so it is a separate change, noted here for later.
- The master skill's own rule ("lift `waiting` once the person answers") stays. This spec makes
  forgetting it cost nothing. It does not make the rule unnecessary.
