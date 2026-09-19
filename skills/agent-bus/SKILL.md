---
name: agent-bus
description: Use when you are a peer agent coordinating with other agents over the Agent Bus (Redis Streams via the `agentbus` CLI) — to publish your state/heartbeat, receive directives by arming `subscribe`, accept/block/complete a tracked request addressed to you, report progress, check whether you're piloted or autonomous, or gate a risky action with the 4-eyes challenge/verdict flow. For the master/pilot role (driving other agents' panes), use agent-bus-master instead.
---

# Agent Bus — Peer Agent

You are one agent among several coordinating over a shared **Redis Streams** bus, one CLI:
**`agentbus`**. You publish your `status`, receive directives by arming `subscribe`, gate
risky actions with `challenge`/`verdict`, and a human watches in busmon.

**Exact per-command syntax and the exact JSON field names live in `docs/AGENT-BUS-GUIDE.md`.
This skill is the mental model + the parts agents get wrong. Read the GUIDE before guessing
any flag or JSON field — don't invent them.**

## Setup
```bash
export AGENT_BUS_PROJECT=<project>   # REQUIRED namespace; every stream is {project}:{kind}
export AGENT_BUS_AGENT=<your-name>   # who you are; must match ^[a-z][a-z0-9_-]{0,31}$
agentbus notify "<name> online"      # sanity check → returns silently, exit 0
```

## Four traps that make a well-formed command fail
1. **Project is mandatory** — pass `--project` or export `AGENT_BUS_PROJECT`.
2. **Flags are `--double-dash <space> value`** — never `=`, never a single dash.
3. **Positional order is fixed;** the message is trailing words (goes **last**), no quotes needed.
4. **`<agent>`/`<project>` match `^[a-z][a-z0-9_-]{0,31}$`** — lowercase, letter-first, ≤32.

Full detail: GUIDE §0.

## Receiving directives — `subscribe` is wake-on-exit, NOT a loop
`agentbus subscribe <self>` **blocks for ONE addressed command, prints ONE JSON object,
then EXITS.** Arm it as a background task; its **exit wakes your session**, you handle the
object, then **re-arm**. Do **not** wrap it in a `while`/daemon loop — a long-lived loop
never wakes a terminal session.

- **Parse the JSON by its documented fields, not guessed ones.** The discriminator is
  **`event`** (`cmd` / `heartbeat` / `error` / `fatal`) — plus `rearm`, `id`, and, for a
  `cmd`, `type`/`from`/`ref`/`body`. `event` (delivery kind) and `type` (the cmd's kind:
  directive/challenge/reply/verdict) are **different fields** — don't collapse them. Every
  line also leads with `"v"` (protocol version); ignore fields you don't recognize. The
  exact table is GUIDE §3.
- **Re-arm iff `rearm` is `true`** *and you still have work in flight*. A `fatal` event is
  `rearm:false` → stop, you're misconfigured. When your work is done, stop re-arming on
  purpose — see "Disconnect when you have nothing left to do".
- **Persist the `id`** and pass it back as `--since <id>` on the next arm — that's your
  cursor (at-least-once; no replay of what you already handled). No `--since` = start at "now".

## Before you act — are you piloted or autonomous?
```bash
agentbus pilot status     # "piloted by hermes"  OR  "autonomous"
```
- **piloted** → wait for a directive (arm `subscribe`); don't act on your own initiative.
- **autonomous** → proceed on your own plan; just keep emitting `status`/`report`.

## Your `status` IS your heartbeat
Agents are one-shot CLI calls, not daemons — there is no separate heartbeat. Emit
`agentbus status <self> <working|idle|blocked|done> <msg>` when your state changes, and
`agentbus report <self> <msg>` at milestones (its full text is retained — `reports <id>`
reads it). busmon shows the state you DECLARED plus how old that declaration is ("no update 18m") —
it never relabels your silence, so a stale `working` stays on screen as your own claim
until you correct it. While armed and waiting you are **`idle`, never `blocked`**.

## Gating risky actions (the 4-eyes money-path)
`blocked` is reserved for an **open 4-eyes gate**. Before proceeding or marking done:
```bash
agentbus gate <self>      # lists open challenges; NON-ZERO exit = you are gated
```
To review a peer's risky change: `agentbus challenge <target> <why>` opens a gate; they
`reply --ref <R>`; a **second, independent** agent resolves it with `verdict … approve|reject`
(a self-approval never counts). Query the recorded state with `agentbus verdicts`.

## Shared working tree — one checkout, several agents
The team usually shares ONE physical checkout. Read-only git (`log`, `diff`, `show`,
`status`) is always safe; anything that moves HEAD or rewrites the tree is a coordination
act, because the same files are on every peer's screen:

- **Never `git checkout`/`git switch` on your own** — it yanks the tree from under the peer
  whose branch is currently checked out (typically mid-review). Branch switching is master's
  call, announced on the bus.
- **Work on a branch named `<role>/<task-slug>`**, created when master hands you the tree.
- **Never leave uncommitted changes behind.** Before you hold, hand off, or go idle: commit,
  or `git stash push -m "<role>: <task>"`. Loose changes ride along on the next checkout and
  silently land in someone else's review.
- **Pop only your own stash** (match the `<role>:` tag), and only once your branch is the
  one checked out. The full dance is: stash → wait for the current branch to merge → your
  branch checked out → pop.
- **A tracked file has exactly one writer at a time.** The one-task-in-flight rule is what
  makes that true — don't edit outside your dispatched task.

## The session budget is shared — check it before you spend
Every agent on the team draws the same subscription window. Read it before starting a large
task (broad survey, subagents, long plan):
```bash
agentbus budget     # account session/weekly % per provider, with reset times
```
- **< 75%** — work normally.
- **≥ 75%** — economy mode: small steps, no exploratory sweeps, finish what's in flight.
- **≥ 90%** — hold: commit or stash, report, go idle until the reset.
The numbers are as fresh as the sentinel's last `agentbus refresh`; if they look stale, say
so on the bus instead of finding out by hitting the wall mid-task.

## The board — who owns what, check it before you start
`{project}:board` is the shared ownership registry (task → owner → state → branch). Read it
before starting anything — a task already owned is off-limits, no matter what a stale
message told you:
```bash
agentbus board                                          # TASK OWNER STATE BRANCH AGE
agentbus board claim <task> --branch <role>/<task>      # fails if a peer owns it
agentbus board state <task> review                      # as the task moves
agentbus board done <task>                              # merged / finished
agentbus board drop <task>                              # released without doing it
```
Claim BEFORE you invest in the work: a failed claim (`owned by <peer> (<state>)`) means
pick another task or ask master — never duplicate. Keep your own entries honest as the task
moves; the board is only as good as its last update.

These verbs are for **untracked** tasks. On a task that carries a tracked request, `claim`,
`state` and `done` are all refused (`tracked request: use request accept/block/done or
explicit board drop`) — see the next section. `drop` is the exception: it is allowed, and it
deletes the tracking without cancelling anything.

## Tracked requests — the only thing that records that you took the work

A `cmd` directive is fire-and-forget: nothing anywhere remembers whether you took it. A
**tracked request** is a directive recorded on the board, and it carries three facts a
`cmd` cannot: whether you accepted it, whether you are blocked on it, and what you answered.

**Nothing infers your acceptance.** Not the delivery, not your `status`, not a reply you
wrote on the thread. If you never run `accept`, nothing records that you took it and the human watching busmon
reads "no acceptance recorded" — which says nothing about you either way, and is exactly
why you should record it. The verbs, in no fixed order beyond the rules below:

```bash
agentbus request accept <task>              # BEFORE you start working. Not optional.
agentbus request block  <task> <reason>     # when you cannot proceed — say why, in words
agentbus request accept <task>              # to resume after a block
agentbus request done   <task> <reply>      # when finished — the reply is your answer
agentbus request                            # EVERY tracked request, done ones included
```

- Only the **target** agent can accept, block or complete its own request.
- `block` does not require a prior `accept` — you may refuse work you never took, with a
  reason. After a block, `accept` again before you can complete it.
- `done` requires a prior `accept`. A reply on the thread completes nothing, and a second
  `done` returns the first response id without replacing what you already answered.
- A deadline gates the **first** acceptance only: a request you never accepted cannot be
  accepted once `expires_at` has passed (nor if its body has been trimmed) — say so on the
  bus rather than starting work that is already out of time. Work you had already accepted
  can still be resumed and completed afterwards, deadline or not.
- **`board claim` / `board state` on a tracked task are refused** — the broker tells you to
  use the request verbs. `board drop` is not refused, and it is the sharp edge: it deletes
  the board entry and its request metadata **without cancelling the command already sent or
  the work in flight**. Dropping loses the tracking, not the action.

A request's `blocked` state is not your agent `status`: the first says this one task cannot
move and why, the second is reserved for an open 4-eyes gate. You can be blocked on a
request while you are `working` on something else.

## What the subscribe JSON now tells you about a request

Where relevant, a `cmd` event carries `task`, `expires_at`, `delivery`, `duplicate_possible`
and `attempt`. The protocol version stays `1` — these are additive, so **ignore fields you
don't recognise** and never treat a missing one as false.

- **`delivery`** describes the transport, never you. On stdout an ordinary delivery reads
  `uncertain`; `queued` and `output_written` are board observations you read with
  `agentbus request`, and `queued` means no attempt was *recorded*, not that none happened.
- **`duplicate_possible: true`** means this exact entry may have reached you before —
  deduplicate on project + message `id`. An absent flag guarantees nothing, since a producer
  may have retried on its own. When the event carries `task`, check `agentbus request`
  before redoing the work.
- **`expires_at`** is omitted when it is zero, so an absent field means no deadline was
  expressed. Never invent one.

## Persist your cursor — a floor is not a filter, it discards

Pending entries are recovered before new ones, and **recovery respects your `--since`
floor**: an entry older than the floor is acknowledged *without being delivered to you*. So
arming with no `--since` (which means "start at now") silently drops everything addressed
to you at or below that id, i.e. everything that arrived while you were not armed — and on a
group that already exists, the server cursor has moved past them for good.

Persist the `id` of the last event you handled and pass it back as `--since <id>` on every
re-arm — and keep the same cursor when a fire carries no id (a `heartbeat` or an `error`),
or you move your floor forward for nothing.

That is what stops you eating your own backlog. It is **not** a promise that nothing is
lost: the stream is capped, so a body can age out of it, and an entry is acknowledged before
you act on it, so a crash can lose the execution. The **board record survives both** — there
is no garbage collection, so a tracked request stays visible even once its body no longer
reads back. `--since 0` lifts the floor for what is still deliverable; it does not resurrect
what your group already acknowledged.

## Pushing a signal nobody asked for — the outbox convention
`notify` and `report` are fire-and-forget: they show up in busmon but wake NO agent. When
you discover something the team must act on — a stash that already exists, a subagent that
died on a provider error, a scope change, a task that's already in review — don't wait to
be asked:
1. `agentbus notify "<finding>"` for the record (the human sees it in busmon), and
2. `agentbus cmd sentinel "relay: <what happened, what you need>"` to wake the caretaker.
   Sentinel judges: blocking → it forwards to master by `cmd`; informational → it logs a
   report.
Sentinel is the cheap relay — relaying is its job. Reserve a direct `cmd master` for when
YOU are blocked and need a decision, not for FYI traffic.

## Disconnect when you have nothing left to do

An armed `subscribe` is not free. Every idle window it exits and **wakes your session**,
which spends tokens whether or not anything arrived. Idling armed "just in case" is the most
expensive way to do nothing. So when your work is finished: stop, and do **not** re-arm.

Re-arm while work is in flight — you are mid-task, you expect an answer, a review is coming
back. That is what the wake-on-exit loop is for. It is the *idle* waiting that must end.

Before you stop, leave nothing **silently** unfinished. You may leave work behind — you may
not leave it unexplained:

- your finished work is reported (`agentbus report <self> …`);
- every board task you own is `done`, or its state says where it really stands;
- no tracked request addressed to you is still `requested` — accept it and do it, or
  `agentbus request block <task> <reason>`. A blocked request is a perfectly good reason to
  leave: the record carries the reason, so master can act on it without you.

Then publish your last state (`agentbus status <self> idle "<what you finished>"`) and simply
do not arm again. An unarmed session costs nothing.

**You do not decide to come back — master does.** A `cmd` addressed to an unarmed agent is
not lost: it stays in the shared stream. Whether it reaches you depends on what happened to
it in **your group**, and the three cases are not the same:

- **Never delivered** (above your group's `last-delivered-id`) — still deliverable. Whether
  you are handed it depends on the floor you arm with: at or below the floor it is
  acknowledged unseen, above it, delivered.
- **Pending** (delivered to your group but never acknowledged) — **still recoverable**, even
  though it sits *below* `last-delivered-id`. Pending entries are recovered before new ones,
  subject to the same floor, as long as the body is still retained.
- **Already acknowledged, or skipped when your group was created** — out of reach. Lowering
  `--since` does not bring these back: it filters what you are handed, it does not rewind
  `last-delivered-id`. Only an operator rewinding the group changes that.

So the floor you choose decides what you see, and the one that creates your group decides
what you will never see. What is genuinely out of reach can still be read out of band —
`agentbus thread`, `agentbus reports`, busmon — and master can re-send what matters.

One live project has 210 such commands sitting unread today.

Master wakes you by injecting into your herdr pane. When you wake, start at your boot
sequence: read `agentbus request` and `agentbus board` **first** — that is where work
assigned during your absence is recorded. The board holds the request's **metadata**, not its
text: read the body with `agentbus thread <thread>`, using the `thread` field the request
records — with a custom `ref` that is not the root entry's id — and check that the body is
still available and the deadline not passed **before** your first `accept`. Then arm, if
there is anything to wait for.

## A `shutdown` directive means the team is done
When master broadcasts `shutdown`, the work is over and idling would just burn
the shared budget on heartbeat wakes. Set `agentbus status done`, post a final
`agentbus report` if you have unreported work, and do **not** re-arm
`subscribe` — an idle session with no armed subscribe costs nothing. Master
closes your pane. If you genuinely still have work, answer on the thread
instead of going silent.

## The whole bus in one line
> Every stream is `{project}:{kind}`. Publish `status`/`report`, receive with `subscribe`
> (wake-on-exit — re-arm while work is in flight, persist the `id` cursor or you discard your
> backlog, and stop arming once you are done: master wakes you through your pane, not the bus),
> `accept` a tracked request before you start and `done` it with your answer, gate the risky
> with `challenge`/`verdict`, and read exact flags & JSON from `docs/AGENT-BUS-GUIDE.md`.
