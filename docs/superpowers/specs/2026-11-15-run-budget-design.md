# A run budget: stop cleanly after a duration, a number of deliveries, or a quota (run-budget)

Status: spec, for review (subject `run-budget-spec`), r6. Not built. It depends on master-idle-spec
§3.1 (the git test, `-U0` patch-ids; master-idle r8). From the person's review of
master-idle-spec (2026-11-15, thread `1794733043827-0`, its §2 and §4), relayed by master; r6 folds
in the person's second review and answers (thread `1794734231604-0`). §8 records the answers.

## 1. What exists, and what is missing

| Already built | Where |
|---|---|
| A provider at `dispatch.quota_warn` (85 %): master told once per window "finish what is in flight, start nothing new" | `dispatch/quota.go`, `quotaPass` |
| At `dispatch.quota_hold` (95 %): its agents are held (no wake, no escalation); master's own provider pauses the project until its reset | same, `FencedQuotaPause` |
| At the reset: each held agent with accepted tasks gets one resume directive; master's pause ends | `dispatch/resume.go`, `release` |
| A dead agent is relaunched with its session resumed | self-repair (R1-R4), `{p}:resume` |
| A project pause: nobody woken until a deadline | `bus/pause.go` |

Missing: a run that stops by itself after a duration or a number of deliveries; a drain before
the stop; the warn reaching the agent that must finish, not only master; and a record of what an
agent held when its client stopped without warning.

## 2. The run

One record per project, `{p}:run` (a hash), written only by the dispatcher, fenced:

| Field | Meaning |
|---|---|
| `started_at` | Redis `TIME` (ms) of the run's start |
| `paused_ms` | time spent paused, summed |
| `seen_at` | the last pass's Redis time, to add a paused interval |
| `deliveries` | heads delivered since the start |
| `d:<head>` | one field per head counted, so none counts twice |
| `state` | `running`, `draining`, `stopped` |
| `since` | when `state` last changed (Redis `TIME`) |
| `why` | `duration`, `deliveries`, or `quota:<provider>` |
| `drains` | how many times this run has entered `draining`: the episode of its once-marks (§3) |

**Start.** The first merge-queue pass that finds no record writes one, `running`, with a
**baseline**: a `d:<head>` field for every approved head (within 72 h) the git test already finds
in the base, and `deliveries = 0`. So a new run never counts what was merged before it (foureyes:
about 15 such heads on this repository today). `agentbus run reset` (§5) deletes the record, and
the next pass starts a new run with a new baseline.

**Duration.** `elapsed = now − started_at − paused_ms`, all Redis time. Each pass adds `now −
seen_at` to `paused_ms` when the project is paused (a person's pause, a quota pause or a suspend),
then sets `seen_at`. The clock stops while the project is paused, to within one pass.

**A gap with no pass** (the service stopped, the host asleep) counts as paused when it is longer
than 3 passes (`now − seen_at` > 15 min, the pass being 5 min): the whole gap is added to
`paused_ms` (the person's answer: a night of sleep must not consume `max_duration`). A shorter gap
(a restart, a slow pass) counts as run time. Both are Redis time, so a sleeping host's own clock
plays no part.

**Deliveries.** A delivery is an approved head that the git test of master-idle-spec §3.1
(`mergequeue.Delivered`: in the base, squash, or rebase-merge) finds in the base. The merge-queue
pass (every 5 min) adds one per head not yet counted, with its `d:<head>` field, in its fenced
write. Forge-independent: an opened PR or a push does not count, and the same head never counts
twice. `run reset` clears the fields with the record.

**The 72 h window.** The git test judges approved heads within 72 h (`ReviewQueueWindow`, as
master-idle §3). A head approved more than 72 h before it is merged is never counted. Accepted as a
limit: a branch that waits three days after its approval is rare here, and counting it would mean
running the git test on every head ever approved, each pass.

**The pass.** The checks above run in the merge-queue pass, which already holds the lease, has the
git test and runs every 5 minutes. Limits are therefore seen within 5 minutes.

## 3. The limits, and the drain

| Key | Env | Default | `0` |
|---|---|---|---|
| `run.max_duration` | `AGENT_BUS_RUN_MAX_DURATION` | 0 | no limit |
| `run.max_deliveries` | `AGENT_BUS_RUN_MAX_DELIVERIES` | 0 | no limit |
| `run.drain_max` | `AGENT_BUS_RUN_DRAIN_MAX` | 2h | drain until nothing is in flight |
| `run.screen_hold_max` | `AGENT_BUS_RUN_SCREEN_HOLD_MAX` | 5h | no notice for a long screen hold (§4); it never releases one |

The registry (`config/keys.go`), `ReadBy: "dispatch"`; busmon's C page lists them. The first
limit reached wins.

**Drain** (`state = draining`, `why` set). On the pass that reaches a limit:

- `drains` grows by one, in the same fenced write that sets `draining`; the episode of this
  drain's marks is `<started_at>:<drains>`, so a run that drained for a quota, ran again, then
  reached a limit, announces its second drain too.
- master gets one waking directive (`FencedCmdOnce`, mark `run-drain`, episode as above):
  `Run budget reached (6 deliveries of 6, 3h10m): dispatch no new task; finish and deliver what is
  in flight; the project pauses when nothing is left.`
- the person gets one notice (`FencedNotifyOnceForPerson`, mark `run-drain-person`, same episode)
  with the totals. Its own mark: both calls deduplicate through a `{p}:quota` field, so a shared
  mark would let the directive's write suppress the notice.
- `agentbus request send` refuses a new task while the run drains, `run is draining: no new task
  (agentbus run)`. The person may still send one; nobody else may. This is a bus rule
  (`bus/requests.go`), so every client meets it. The person is recognised as `deliver` recognises
  them (`cmd/agentbus/deliver.go`): a caller with no `AGENT_BUS_AGENT`, which is busmon's compose
  window and the person's own shell. Like `deliver`'s role check, this is defence in depth, not a
  boundary: an agent that unsets the variable passes, as the forge-delivery decision already
  says (2026-11-02); whether it must be one is the security role's call.
- `team-stall` reads `ok` (`run draining: ready plan tasks wait`): a ready plan task is not a stall
  during a drain.
- `idle-master` keeps working: an approved head not delivered still wakes master, or the drain
  would never end.

**A limit reached while draining for a quota** replaces `why` with that limit (no new drain, the
same episode: master and the person were already told the run drains; the notice of the stop
gives the reason). So the quota's release never returns a run to `running` past a real limit.

**Stop** (`state = stopped`). When nothing is in flight, or `run.drain_max` after the drain began:

- *in flight* is: a board entry `requested` or `accepted`, an `undelivered` head (master-idle-spec
  §3), an open gate, or an agent `working`;
- the dispatcher writes a project pause with kind `run` (`bus.PauseRecord.Kind`, additive), its
  deadline one year ahead, so every existing pause reader stops waking as it does today, and no
  quota release lifts it (`QuotaPauseProvider` is "" for it, as for a person's pause);
- the person gets one notice (mark `run-stop-person`, the drain's episode) with the totals and
  what was still in flight if `drain_max` cut the drain short.

Each once-mark of this spec has its own name: `run-drain`, `run-drain-person`, `run-stop-person`,
`run-held:<agent>` (§4). One field each per project, overwritten by the next run's episode.

## 4. The quota trigger

**A client that reports its quota** (a reading in `{p}:budget`). The existing warn changes in one
way: at `dispatch.quota_warn` it is sent once per window to **each agent on that provider** that
holds work (an accepted task or `working`), not only to master, through the same per-window mark
extended with the agent:

`Quota anthropic 88% (five_hour, resets 15:40): finish your step, commit work in progress on your
branch, report what is left, then status done. Start nothing new.`

Master still gets its own line. If the provider is master's, the run enters `draining` with
`why = quota:<provider>`. The existing hold at `quota_hold` and the pause until the reset are
unchanged.

**A client that gives no warning.** No reading, so the hold is seen only when it happens. One
source is added: a client profile in `agentstate` may declare `Limit` patterns (its "usage limit
reached" screen); none declared, never seen.

**Where it matches (blocking in the person's review).** The agents work on this very repository,
whose code, tests and diffs contain "usage limit reached". So `Limit` is never matched on the
screen as a whole, nor on the conversation: only on the client's own status area, as `Ready` is
matched only in the input box (`inputRegion`). For a client that prints the limit as a status line
under or just above its input box, the match is on those lines only, outside the box itself (so
text the person or the agent types never matches), with whole-line patterns (`^…$`) pinned by a
captured screen (`agentstate/testdata/screens`, as every signature is). For a client that replaces
the whole screen with a limit page, the match is on the bottom lines, as `Dialog` is
(`bottomLines`), and the page must also show no input box. A client whose limit screen fits
neither declares no `Limit` and is never held from its screen. Today's `hold:<p>` field stores a reset in Unix seconds and every release path needs a
reading, so a silent client's hold gets its own field and its own release:

- A match on an agent's settled pane writes `hold-screen:<p>:<agent>` in `{p}:quota` (fenced) = the
  hold's start (Unix s, Redis `TIME`), and, when the signature captures a reset time (an optional
  `reset` group, the next such clock time), `<start>:<reset>`. **Its zone is the pane host's.**
  The dispatcher reads a pane through its own host's runtime (`herdr` or the holder, a local
  socket), so the process that reads the screen runs on the pane's host; it converts the captured
  clock time with its local zone to Unix seconds before anything is written to Redis. A remote
  broker changes nothing: the broker stores only the converted value.
  The provider counts as held (`l.quota.held[p]`) while any such field exists, as for `hold:<p>`.
- The hold is the provider's, but the screens are the agents': `hold-screen:<p>:<agent>` (one
  field per agent whose pane showed the signature, same value) records which panes showed it, and
  a counter of settled reads without it per agent is kept in the dispatcher's memory (a restart
  starts it again at 0, which only delays a release).
- **Release**, with evidence only: a reading of the provider that releases it as today; or
  **every** agent field has its own evidence, either its captured reset plus `resumeMargin` has
  passed, or its pane has been seen settled without the `limit` signature on 3 observe passes in a
  row (the observe pass keeps inspecting a held agent's pane; it only skips the wake). So one
  pane's clear screen or captured reset never resumes another that still shows the limit. The release goes
  through `release` as today: resume directives, then the fields deleted last.
- **No release by time alone.** A provider's windows run from five hours to seven days (Claude's
  `seven_day`), so an unknown reset has no safe bound. At `run.screen_hold_max` (default 5h) the
  hold stays, and the person gets **one** notice (mark `screen-hold-person`, episode the hold's
  start): `coder has shown anthropic's usage limit since 14:02 and its reset is unknown. It
  resumes when its screen clears; open its pane to see the reset.`

**What a held agent had.** When a hold begins for an agent with work (from either source), the
dispatcher writes one field of `{p}:held`, fenced: agent → `{provider, since, tasks, branches,
report}`: its accepted tasks (board), their `branch` field where the board has one, and the id of
its last report. Master gets one directive per agent and episode (mark `run-held:<agent>`,
episode the hold's start): `coder is held (anthropic, since
14:02, released when its screen clears): accepted wiki-3 (feat/wiki-3). Reassign it, or wait for the reset.` The
field is deleted by the release, or by `retire --forget`.

**Master held.** As today: the project pauses until the reset. For a screen hold of master's
provider, the pause's deadline is the latest captured reset plus `resumeMargin` when every field
captured one; otherwise there is no reset to wait for, and the quota pause is written with a
deadline one year ahead (kind quota, as today), ended only by that provider's `release` (screens
clear, or a reading). `coordinatorMoved`'s `resetOf` follows the same rule, so a deadline is never
computed from an absent `hold:<p>` (which would be 1970 and pause nothing). Added: the person's notice lists
what is in flight (the same list as §3's stop).

**At the reset.** As today: the release sends each held agent its resume directive and ends
master's quota pause; a dead agent is relaunched by self-repair with its session resumed. A run in
`draining` for `quota:<provider>` (and still that `why`, §3) returns to `running` on that
release, since the quota was its only reason. A run drained for a duration or deliveries is not resumed by a quota.

## 5. Commands

```
agentbus run            # state, elapsed / max, deliveries / max, why, since
agentbus run reset      # the person only: deletes {p}:run, lifts a kind-run pause
```

`run reset` is refused to a caller with `AGENT_BUS_AGENT` set, the same recognition as §3's
(defence in depth, not a boundary): only the person restarts a run (§8, question 1).

## 6. Interplay

| Guard | While `draining` | While `stopped` |
|---|---|---|
| `idle-master` | wakes master for undelivered heads | quiet (paused) |
| `team-stall` | `ok`, ready plan tasks wait | quiet (paused) |
| `idle-implementer` | `ok`: requested tasks were sent before the drain and stay master's call | quiet |
| dispatcher wakes | unchanged | none (paused) |
| quota hold and pause | unchanged | unchanged; its release never lifts a kind-run pause |

## 7. Tests

- **Duration**: elapsed excludes paused passes; a limit of 3h is reached at 3h of unpaused time;
  `0` never limits. **Gap**: no pass for 10 min counts as run time; no pass for 8 h (a host
  asleep) adds the 8 h to `paused_ms`, so a 3h limit is not reached by the night.
- **Deliveries**: a merge, a squash and a rebase-merge each count once; the same head on the next
  pass does not count again; a pushed head or an open PR does not count. **Baseline**: a new run,
  or `run reset`, with N approved heads already merged starts at 0, and the next merge counts 1.
- **Drain**: one master directive and one person notice per run, both emitted (distinct marks: the
  directive written first does not suppress the notice), and neither again on the next pass; `request send` refused to an
  agent, allowed to the person; `team-stall` `ok` with a ready task; `idle-master` still wakes.
  **A second drain**: a quota drain, its release (`running`), then `max_duration`: `drains` = 2, a
  second directive and a second notice. **A limit during a quota drain**: `why` becomes the limit,
  and the quota's release leaves the run `draining`.
- **Stop**: nothing in flight → kind-run pause; `drain_max` passed with work in flight → pause and
  the notice lists it; a quota release does not lift a kind-run pause; `run reset` lifts it.
- **Quota, reporting client**: at `quota_warn` each agent on the provider with work gets one
  directive per window, none twice; an idle agent without work gets none; master's provider sets
  `draining` with `quota:<p>`, and its release sets `running`.
- **Quota, silent client**: a `limit` signature on a settled screen holds the provider; `{p}:held`
  records the agent's accepted tasks and branches; master gets one directive; the release deletes
  the field; a client without a `limit` signature is never held from its screen. **Not the agent's
  own output**: a pane whose conversation shows a diff, a test or a log line with "usage limit
  reached", and whose status area does not, is not held; the same words typed in the input box
  are not a match either; the captured limit screen is. **Zone**: a reset `15:40` captured with
  `TZ=Asia/Tokyo` on the reading process is stored as 15:40 Tokyo time in Unix seconds. **Screen hold
  release**: the limit screen, then 3 settled reads without it → released, resume directive sent;
  a captured `reset` → released at reset + margin; 2 settled reads without it then the screen
  again → still held. **Two agents on one provider**, both showing the limit: one clears 3 times,
  the other still shows it → still held, nobody resumed; both clear → released. **Long hold**: at
  `screen_hold_max` with no evidence → still held, one person notice, none on the next pass.
- **Master held**: the project pauses (as today), and the notice lists the work in flight.
  Master's provider screen-held without a captured reset: the project stays paused across passes
  (deadline a year ahead, never 1970), and resumes when the screen clears; with captured resets:
  paused until the latest one plus the margin. One agent's captured reset passed while another
  field has none and still shows the limit: still held.
- Settings in the registry with their env names (registry test).

## 8. The person's answers (2026-11-15)

1. **Who may reset the run:** the person only (§5).
2. **Drain or hard stop:** drain, then pause when nothing is in flight or after `run.drain_max`
   (2h) (§3).
3. **The quota threshold:** reuse `dispatch.quota_warn`; no separate `run.quota_drain_at` (§4).
4. **When a run starts:** by itself, at the first pass with no record, and after `run reset` (§2).
5. **A gap with no dispatcher pass:** counted as paused beyond a few passes: 3, i.e. 15 min (§2).

## 9. Not in scope

- Committing an agent's work in progress for it: the agent commits, on its own branch; the
  dispatcher never runs git in an agent's worktree.
- Per-agent budgets, or a token count: the run counts time and deliveries; quotas stay the
  provider's readings.
- Relaunching a held agent that is still alive: it resumes from the release's directive; only a
  dead one is relaunched (self-repair, as today).
