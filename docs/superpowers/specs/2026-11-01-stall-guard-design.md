# Stall guard: the team never stops silently (design, spec only)

Status: spec for review; no code yet.
Task: stall-guard (master's directive of 2026-11-01; the person's priority, "chaîne
d'asservissement"): when the work stops with nobody deciding that it stops, something must
notice, restart it through the normal channels, and tell the person when only the person can.

## What happened on 2026-11-01

- coder handed off at ~90 % context (`report` + `handoff`), then waited for the dispatcher's
  `/clear`. Auto-clear is **on** for this project (`AGENT_BUS_AUTO_CLEAR="1"` in
  `~/.config/agentbus/services/agent-bus-monitor.env`), yet no `/clear` came. The dispatch log holds
  no `clear` line and no reason: `tryClear` returns quietly on every `no(...)` of `DecideClear`
  (`dispatch/clear.go`), so a deferred clear leaves no trace anywhere.
- The person cleared the pane by hand. coder drained, answered that the next task was queued, and
  stopped, as the role says: one task at a time, then hold.
- Every agent was then `idle`. No request was open, no ask, no gate, no pause. The plan still had
  tasks ready for an agent. Nothing on any screen said "the team has stopped"; the person found
  out by looking.

Three faults, each silent: a hand-off whose clear never came, a team idle with ready work, and (the
same family, seen before) an agent idle on a queue it never read. This spec measures all three
from Redis, the way health already measures everything else, and routes each to whoever can act.

## Principles

1. **Measure, never infer from silence alone.** Idle means *declared* `idle`/`done` for longer
   than the threshold **and** seen not busy by a fresh observation of its pane. A missing or stale
   reading is `unknown`, never `ok`, and never a stall.
2. **Use what exists.** Three new health checks in the dispatcher's health loop (`realHealthChecks`):
   no new daemon, no new stream, no new key except the once-marks below. The fail wake to sentinel
   is the existing `FencedHealthPut` episode: one wake per fail episode.
3. **Nothing typed but the wake line.** Sentinel restarts work only by sending master one
   directive through `agentbus cmd`. The dispatcher wakes master with its usual wake line. Nobody
   starts work that needs the person's approval.
4. **Every dispatcher write fenced.** The health records go through `FencedHealthPut`, the notices
   for the person through `FencedNotifyOnceForPerson` (`bus/once.go`) under a mark in `{p}:quota`,
   both with the lease token, which only the health loop holds: a check's `Run` gets a context and
   no token, so a check never writes; it returns its outcome, and the loop acts on it (see "Who is
   told"). Only the dispatcher writes a `for=person` notice: agents have no such flag, and this spec
   adds none. Sentinel's one directive to master is an agent's ordinary `agentbus cmd`, not fenced
   (no agent write is); it is kept to one per episode by sentinel's own note (below).
5. **Off is one setting.** Each threshold is a `config.toml` key; `0` turns its check off. It then
   reads `unknown` with the detail `off (stall_after = 0)`: not measured, never `ok`.

## The three checks

| Check | Measures | Statuses | Place (F5, For you) | Handler |
|---|---|---|---|---|
| `team-stall` | every registered agent idle for N with nothing open, and work ready | ok / warn / fail / unknown | warn `waiting-person` **for you**; fail **in hand** (sentinel) | sentinel |
| `clear-stall` | a valid hand-off older than N with no `/clear` | ok / warn / unknown | in hand (dispatch) | dispatch |
| `quiet-agent` | a directive delivered to an idle agent, no activity from it since, older than N | ok / warn / unknown | in hand (master) | master |

All three: `Interval` 1 min, `Deadline` 5 s, like `cmd-delivery`. Each record's detail names the
agents and ages it judged from (bounded, `health.SafeText`), never message text.

### 1. `team-stall`

**Plan reads.** `plan.Derive` needs the verdicts by subject. The check keeps the ledger in its own
memory and reads it forward with `VerdictsAfter` from the last id seen, as busmon does: one full
read at the dispatcher's start, then only new entries each minute. The plan itself (`{p}:plan`,
`{p}:plan:facts`) and the board are small and read whole.

**Inputs** (all already read by the dispatcher or one read away): `{p}:agents` (state, `TS`),
`{p}:observed` (busy, fresh), `{p}:board`, `{p}:asks`, the gates (`OpenChallenges`), `{p}:pause`,
`{p}:swap:*` (`SwapIntents`), each agent's `Obligation` (pending + unread), and the loaded plan
derived with `plan.Derive` over the same inputs busmon uses.

**Idle team**: every agent in `{p}:agents` that is not dormant (`dormantAfter`, as busmon hides
them; the constant moves from `cmd/busmon/agents.go` to `bus.DormantAfter` so both read one value)
has declared `idle` or `done` for at least N (`stall_after`), and a **fresh** observation of its
pane (`{p}:observed`, `Observation.Fresh`, under 120 s) shows it not busy. An agent in `working`,
`blocked` or `waiting` makes the team not idle: `blocked` and `waiting` already have their own
circuits (blocked notices, For you).

An idle-declared agent with **no fresh observation** (none recorded, aged out, a failed inspection
past its grace, an unverified identity the observe pass skips, or the observe pass off) cannot be
judged: the check reads `unknown`, `cannot see coder's pane (no fresh observation)`, never `fail`
(reviewer-codex). A team whose panes are not observed is therefore never declared stalled; that is
the honest cost of measuring rather than guessing. Verified identities, and so observations, are
Linux-only (`/proc`): **on macOS `team-stall` always reads `unknown`**, and the guard is blind there.
The F5 detail says so (`cannot see panes on this host: no verified identities`) rather than a
per-agent line (foureyes).

**Nothing open**, else `ok` ("work is open: <what>"):
- no board entry `requested` or `accepted` (an accepted task whose owner sleeps is
  `quiet-agent`'s case, below);
- no open ask, no open gate, no project pause, no swap or reload intent;
- no agent with an obligation (pending or unread entries): the dispatcher already wakes or
  escalates those, and a second signal would double the noise;
- no valid hand-off (`{p}:handoff`, `Handoff.VoidFor` false): an agent waiting for its `/clear` is
  `clear-stall`'s case. Without this, 2026-11-01 itself would have made sentinel ask master to
  dispatch the next task to the agent sitting at 90 % context (foureyes);
- no quiet agent: `team-stall` evaluates the same predicate as `quiet-agent`, from the same inputs
  (the retained `{p}:cmd` history, each group's `LastDelivered`, the activity sources listed under
  `quiet-agent`), through one pure function, `health.QuietAgents`, that both checks call. A
  delivered directive is no longer an obligation, so without this shared evaluation `team-stall`
  could fail while `quiet-agent` warns for the same fault (reviewer-codex). The `{p}:cmd` history
  read (XREVRANGE, bounded to the stream's ~1000 entries) is added to `team-stall`'s inputs;
- no mission attempt in progress for any agent (the conductor's, read as `swap`'s `openWork` reads
  it, `missionsOf`).

**Held agents.** An agent whose provider is held (`{p}:quota` hold marks) declares `idle` and the
dispatcher neither prompts nor escalates it. A ready task whose owner is held is **not ready**: the
long-hold notice (`longhold:<p>`) already speaks for it, and dispatching to a stopped provider helps
nobody. If every ready task's owner is held, the check reads `ok` with the detail
`ready work waits on a quota hold: pages-5 (coder, anthropic until 15:40)`.

**Work ready**, read from the derived plan:
- *agent-ready*: a row `Upcoming`, its owner an agent (not `person`) whose provider is not held,
  `WaitingOn` empty;
- *person-ready*: a row `Waiting` (the person's task, or a merge waiting for the person).

| Idle team, nothing open | Plan | Status | Reason code | Detail |
|---|---|---|---|---|
| no | | ok | | `<agent> working` / `work is open: 1 gate` |
| yes | agent-ready task | **fail** | `stalled` | `team idle 18m, nothing open; next ready: pages-5 (coder)` |
| yes | person-ready only | **warn** | `waiting-person` | `team idle 18m; waiting for you: Reinstall and tag v0.26` |
| yes | nothing ready | ok | | `team idle, plan has nothing ready` |
| yes | no plan loaded / unreadable | unknown | | `no plan loaded: cannot tell whether work remains` |
| a read failed | | unknown | | `agents unavailable` (and so on) |

`waiting-person` is declared in the check's `Reasons` with the command `agentbus plan` (what the
person reads first), so `PlaceOf` puts it **for you**. `stalled` is the fail: the existing
transition wakes sentinel once per episode. The episode ends when any agent declares `working`, a
request is opened or the plan moves. A flaky read (`unknown` in between) keeps the episode, as
`FencedHealthPut` already does.

### 2. `clear-stall`

**Inputs**: `{p}:handoff` (agent, `at` from Redis `TIME`), `Handoff.VoidFor` against the agent's
snapshot, `dispatch.auto_clear`, the `clear:<agent>` reservation in `{p}:quota`, and the clear
pass's **last decision per agent** (new, in memory, see below).

| Case | Status | Detail |
|---|---|---|
| no valid hand-off | ok | |
| hand-off younger than N | ok | `coder handed off 6m ago; clear pending` |
| hand-off older than N, auto-clear off | warn | `coder handed off 22m ago; auto-clear is off: clear the pane by hand` |
| hand-off older than N, auto-clear on, not cleared | warn | `coder handed off 22m ago; not cleared: the pane is focused` |
| the reason is unknown (dispatcher just restarted) | warn | `coder handed off 22m ago; not cleared: no decision yet` |

The detail ends with `DecideClear`'s own `no(...)` text, the one the log never showed. To have it,
the clear pass keeps `lastClear map[agent]{reason, at}` in the loop's memory, guarded by a mutex,
and `HealthConfig` gets a read function for it, as it has `UsageRefresh`. This is also the fix of
the queued auto-clear task's first half: the same pass **logs** each deferral once per (agent,
reason) change: `{"agent":"coder","action":"clear-deferred","reason":"the pane is focused"}`.
Never once per pass. This log stands on its own and can ship first, as the queued auto-clear task,
before any check here (foureyes).

Warn, not fail: a missed clear is the person's or the dispatcher's to fix, and sentinel can do
nothing about it (it never types into a pane). When the agent's pane is the person's focused pane,
the reason itself says so, and For you shows it (see busmon below).

### 3. `quiet-agent`

**Inputs**: the retained `{p}:cmd` stream (`cmdHistory`, as busmon's Requests view reads it), each
agent's `Obligation.LastDelivered`, and the agent's last status `TS` and last report time.

An agent is **quiet** when all of these hold:
- the newest entry addressed to it that asks for work, a `directive` or an `escalation` (not
  `wake=no`), from master, the person or the dispatcher, is **delivered**: its id ≤ the group's
  `LastDelivered`, and not pending. A `reply` or a `verdict` never counts: an informational reply
  ("approved", "nothing more needed") rightly leads to nothing (foureyes);
- the agent shows **no activity** since that entry, where activity is any of: a status (`TS`), a
  report, any entry it wrote in `{p}:cmd` (`from` = the agent: a reply, a verdict, a challenge, a
  directive), a board entry it owns with `Updated` after the entry, an ask it opened
  (`CreatedAt`), or a hand-off it declared. Every one of these is compared in Redis time with
  the entry's id. The board's `Updated` is the writer's own clock in Unix seconds today
  (`time.Now().Unix()` passed into `boardMutation`), so a remote agent's slow clock could stamp a
  real update before the directive (reviewer-codex, foureyes). Every script that writes the board
  (`boardMutation` in `bus/board.go`, `publishRequest` and `requestTransition` in
  `bus/requests.go`, the board writes in `bus/delivery.go` and `bus/conductorkeys.go`) therefore
  also stamps `updated_ms` from Redis `TIME`, an additive field next to `updated`. `quiet-agent`
  reads only `updated_ms`. An entry the agent owns that has no `updated_ms` (written before the
  change) counts as activity, so an unknown clock never makes an agent quiet;
- the agent is `idle` or `done`, not seen busy, not held, and the entry is older than N
  (`quiet_after`).

That is "the queue was read by something, and nothing came of it": a plain `subscribe` that ACKed
it unseen, a session that drained and died, a cleared session whose new context never saw it.

Warn, in hand by master, detail `coder: directive from master 25m ago delivered, nothing since`.
`quiet-agent` contributes to `team-stall`: a quiet agent counts as **open work** (so `team-stall`
stays ok while `quiet-agent` warns, and master is the one to resend). This keeps one signal per
fault.

## Who is told, by whom

**How the loop acts.** A check returns an outcome; it never writes. The health loop gains one
hook beside `UsageRefresh`: `HealthConfig.StallNotify func(ctx context.Context, token string, rec
health.Record) error`, called by the loop right after a successful `FencedHealthPut` of
`team-stall`, with the lease token. The hook decides from the stored record (its status, reason
and `since`) and the current snapshots whether a notice for the person is due, and writes it with
`FencedNotifyOnceForPerson`. A lost lease returns `bus.ErrNotHolder`, which the loop already turns
into `ErrLeaseLost`.

**When master cannot act, the dispatcher tells the person at once.** On the pass that enters the
`team-stall` fail, the `StallNotify` hook applies the dispatcher's own rule for "the escalation target
cannot act" (`recipientCannotAct`, `dispatch/loop.go`: its provider is held, or its own tab was last
seen blocked), made a pure function of the target's snapshot, its hold and its observation so the
health pass and the reconciliation share it without sharing state. If master cannot act, the pass
sends one notice for the person instead of relying on sentinel:

```
FencedNotifyOnceForPerson(mark "stall:<since>", episode "<since>",
  "Stall guard: the team is idle with pages-5 ready, and master cannot act (master is held by its
   provider's limit). Dispatch from busmon, or wait for the reset.")
```

Sentinel is still woken (the fail transition does that), finds the notice already sent (the
detail says so: `person told: master cannot act`), records a note and stops.

**When master can act, sentinel tells master.** The wake is today's: `Health check team-stall
entered fail (…); inspect with agentbus --project P health detail team-stall`. The sentinel skill
gains one section:

1. Read `agentbus health detail team-stall` and `agentbus plan`. Re-check that it still holds (the
   fail may have ended since the wake); if not, record a note and stop. If the detail says the
   person was told, record a note and stop. If a `team-stall` note of its own is newer than the
   record's `since`, this episode was already handled (a second wake, a daily review): stop. That
   note is what keeps sentinel's directive to one per episode.
2. Send master **one** directive, then stop:

   ```
   agentbus cmd master "Stall guard: every agent idle 18m, nothing open (no request, ask, gate,
   pause or swap). Next ready in the plan: pages-5 (coder) 'GitHub-reachable health check'.
   Dispatch it, or tell the person why the team waits."
   ```

3. Record a health note: `agentbus health note team-stall "sent master the next task: pages-5"`.

Sentinel never dispatches a task itself, never starts work, never types into a pane, and never
answers on master's behalf. One wake, one message. The dispatcher wakes master with its usual wake
line, as for any directive.

### Second tier: master did not move

The `StallNotify` hook also reads the fail episode (`Record.Since`, Redis time). When the fail has
lasted `2 × stall_after` with nothing changed, it sends **one** notice for the person:

```
FencedNotifyOnceForPerson(mark "stall:<since>", episode "<since>",
  "Stall guard: the team has been idle 35m with work ready (pages-5); master was told 17m ago
   and nothing moved. Look at master's pane, or dispatch from busmon.")
```

Fenced, once per episode (the episode is the fail's `since`, from Redis `TIME`), under the same
`stall:<since>` mark as the cannot-act notice, so the person is told once per episode by either
path. The mark has one field per project at most: the health pass deletes any `stall:*` field whose
episode is not the current fail's (a fenced `HDEL`, in the same script family as the once-marks), on
the first pass after the episode ends. So `{p}:quota` never grows by one field per episode. It is a `for=person` notice, so busmon marks it unread
(●) and For you lists it. No bell beyond what an unread line already does.

## busmon

Nothing new to draw: the three checks appear wherever health already appears.

Header pill, the stall waiting for the person (`waiting-person` is for you):

```
 health ▲ 1 to do (F5)
```

F5, For you section:

```
╭─ Health · 1 to do · 1 in hand · 14 fine · 2 not measured ──────────── y copy · esc back ─╮
│   ── for you ──                                                                           │
│ ▲ Team stall          team idle 18m; waiting for you: Reinstall and tag v0.26     2m  │
│                       run: agentbus plan                                                  │
│   ── in hand ──                                                                           │
│ ● Clear after hand-off  coder handed off 22m ago; not cleared: the pane is focused   dispatch│
│   ── fine · 14  (a shows them) ──                                                         │
╰───────────────────────────────────────────────────────────────────────────────────────────╯
```

The fail case (in hand by sentinel) reads `● Team stall  team idle 18m, nothing open; next ready:
pages-5 (coder)  sentinel`, muted in the pill (`●`), since something already deals with it.

**For you panel.** It lists asks, waits, gates and unread lines today, never health. Two
additions, both from data busmon already reads:

- a `team-stall` record in `waiting-person` adds one For you row, rank just after asks:

  ```
  ▲ stall  team idle 18m · waiting for you: Reinstall and tag v0.26
  ```

  Enter opens the plan task's page (its `Action:` row already offers the compose window to
  master).
- a `clear-stall` warn whose reason is `the pane is focused` adds one row, since only the person
  can unfocus the pane:

  ```
  ▲ clear  coder handed off 22m ago; its pane is focused: switch away to let it clear
  ```

The second-tier notice is an ordinary `for=person` notify: its ● line already reaches For you.

## Settings (`config/keys.go`)

| Key | Env | Default | `0` |
|---|---|---|---|
| `dispatch.stall_after` | `AGENT_BUS_STALL_AFTER` | 15m | `team-stall` off |
| `dispatch.clear_stall_after` | `AGENT_BUS_CLEAR_STALL_AFTER` | 15m | `clear-stall` off |
| `dispatch.quiet_after` | `AGENT_BUS_QUIET_AFTER` | 15m | `quiet-agent` off |

Durations, `ReadBy: "dispatch"`, the env rule of `dispatch.prompt_grace` (a bare number is seconds,
a Go duration otherwise, `0` = off). `agentbus config` prints them; `up` writes nothing new.

## What does not change

- The dispatcher's wake and escalation rules (`Decide`): obligations stay theirs.
- The one-task-at-a-time rule for agents: the guard asks master to dispatch, it never makes an agent
  start the next task by itself.
- `/clear` rules: `clear-stall` only reports `DecideClear`'s answer, it never relaxes one.

## Tests (plan)

Pure evaluators in `health/stall.go`, table-driven like `EvaluateGitHub`:

- `EvaluateTeamStall`: each row of the table above, plus one agent `working` (ok), one `waiting`
  (ok), one dormant agent ignored, a fresh busy observation on an idle agent (not idle), an aged
  observation ignored, each open-work kind (request, accepted task, ask, gate, pause, swap,
  obligation, quiet agent, valid hand-off, mission attempt), a void hand-off (not open), an
  agent-ready task with `WaitingOn` (not ready), a ready task whose owner is held (not ready; the
  quota-hold detail when it is the only one), an idle agent with no observation, an aged one, and
  an unverified one (each `unknown`, never `fail`), a delivered directive with no activity after it
  (open work through `QuietAgents`, so `team-stall` is ok while `quiet-agent` warns), the boundary
  at exactly N.
- `EvaluateClearStall`: no hand-off, young, old with auto-clear off, old with each `DecideClear`
  reason, old with no recorded decision, a void hand-off (ok).
- `EvaluateQuietAgent`: delivered and silent (warn), pending (ok: the dispatcher's case), `wake=no`
  (ok), a held agent (ok), and each kind of activity after the entry (ok): a status, a report, a
  reply, a verdict, a challenge, a board update by its `updated_ms`, an ask, a hand-off. An owned
  board entry with no `updated_ms` is activity (ok). Each board-writing script stamps `updated_ms`
  from Redis `TIME` even when the caller passes a far-off `updated` (a fake clock in the test). A `reply` or `verdict` as the
  newest entry never makes an agent quiet. The boundary at N.

Dispatcher (`dispatch/`, the fake runtime, Redis opt-in through `AGENTBUS_TEST_REDIS_URL`):

- the `StallNotify` hook is called only after a successful put of `team-stall`, with the token; a
  check's `Run` has no way to write (it receives no token);
- a `team-stall` fail wakes sentinel once; a second pass in the same episode wakes nobody; an
  `unknown` pass between two fails keeps the episode.
- the second tier sends one `for=person` notice at `2 × stall_after`, none before, none twice, and
  a new one only for a new episode; with a lost lease it writes nothing.
- the clear pass logs one `clear-deferred` line per (agent, reason) change and the health read sees
  the same reason.
- `stall_after = 0`: the check reads unknown `off (stall_after = 0)` and wakes nobody.
- master held or its tab blocked on the fail pass: one `for=person` notice at once, under the
  `stall:<since>` mark; the second tier then sends nothing more for that episode.
- the cannot-act rule as a pure function: the reconciliation's escalation tests still pass on it.
- the `stall:*` mark is deleted, fenced, on the first pass after its episode ends; with a lost lease
  nothing is deleted.
- the ledger is read forward: a second pass reads only entries after the last id (a fake reader
  counts the calls).

busmon: goldens of F5 with each record, the For you rows, and the alignment suite with long agent
and task names. The sentinel skill's new section is reviewed as text (no skill test exists).

Live check (after merge, before any default change): replay 2026-11-01 on a scratch project: hand
off, keep the pane focused, see `clear-stall` name the reason; leave the team idle with a ready
task, see sentinel's one directive to master; stop master, see the second-tier notice.

## Questions for Bernard

1. **Threshold.** 15 minutes for all three, or a longer `team-stall` (30 min) so that a short
   pause between tasks never wakes sentinel?
2. **Accepted task, sleeping owner.** Counted as open work (no `team-stall`, `quiet-agent` handles
   it) as proposed, or a stall in itself?
3. **No plan loaded.** `unknown` (proposed: without a plan nothing says work remains), or should an
   idle team with no plan warn after a longer delay?
4. **Second tier.** A notice at `2 × stall_after` (proposed), or also the desktop (`notify-send`,
   as prompt-notify does)?
5. **Sentinel's directive.** Name only the next ready task (proposed), or every ready task?
6. **For you rows.** Only `team-stall` waiting for you and a clear blocked by the focused pane
   (proposed), or every health check that is for you?
7. **macOS.** The guard is blind there (no verified identities, so no observations). Accept it, or
   allow `team-stall` on declared states alone on hosts without `/proc`, with a longer threshold?
