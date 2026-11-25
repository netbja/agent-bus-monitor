# Cleaning up agents (cleaning-up-agents)

Status: spec r3, for review (subject `cleaning-up-agents`). Not built. Base: origin/dev `49926f09`.
- r2: master's addition (cmd `1795648805663-0`): a lifecycle with a deliberate **parked** state,
  auto-park, wake on demand, orphan requests blocked when not closed, parking at a holder switch.
- r3: the person's answers to ask `1795649139554-0` (Q1-Q6, typed, §12.1), and foureyes' reject of
  r1 (verdict `dn4oi37vwmn6`). A request closes only on a link **recorded on the request** at
  delivery, never on a name inferred by the pass: `deliver` records it (§8.2). Four displays:
  running, parked, **stopped**, lost (§3). Owing work includes unpushed commits and a stash tagged
  with the role, and only actionable, non-stale messages (§3.1). A close is reversible (§8.5).
  Master is woken for an **unplanned** holder loss only (§6.2). What r2 alone raised goes to a
  second ask (§12.2).

Author: architect, from master's directives (cmd `1795648670814-0` and `1795648805663-0`, task
`cleaning-up-agents-spec`). No new dependency.

## 1. The problem

The holder that hosts the agents' terminals stops or is replaced (an update, a crash). Agents
registered in its terminals stay on the board as if they still worked, and their accepted requests
stay `accepted` after their pull request merges. busmon shows every agent without a terminal the
same way, "no terminal" in red: an optional role that finished and a coder that died mid-task look
alike. An on-demand role either runs idle for hours or is retired and popped back by master.

**The case in point.** docs ran in terminal `t21` of the v0.33.0 holder, which died. docs held
`agent-skill-needs-answer` accepted (request `1795621095391-0`). Its pull request, #794, merged into
`dev`, approved under subjects `agent-skill-needs-answer` and `pr-agent-skill-needs-answer`, author
`docs`. The request is still `accepted`. docs came back on the v0.34.0 holder (`t15`) as a new
process and left it open as "pre-session". Nothing else closes it.

What this spec delivers:
1. **A lifecycle**: running, parked (deliberate, resumable), stopped (owes nothing), lost (a fault)
   (§3);
2. **Parking**: preconditions, auto-park, wake on demand (§4, §5);
3. **A holder that stops or is replaced**: lost agents detected; master woken for an unplanned
   loss; at a planned switch agents park on the old holder and resume on the new one (§6, §7);
4. **Orphan requests**: closed when their own pull request, recorded at delivery, merged;
   otherwise blocked and listed to master; never left `accepted`; a close can be reopened (§8);
5. a lost agent that owes nothing is **retired by rule** (§9); busmon and health show each state
   apart, and never "no terminal" (§10).

## 2. Today (code at `49926f09`)

- **An agent's terminal.** `{p}:agents` records `pane = "<build key>/<terminal>"`
  (`AgentSnapshot.Pane`, `bus/stream.go:75`), with the proved `pid`, `pid_start`, `boot_id` and
  `ident_session`. `health.HolderPane` splits it (`health/holder.go:68`). `{p}:resume` keeps each
  role's last `{session, profile}` (`ResumeRecord`, `bus/resume.go:31`).
- **"no terminal".** `bus.ShownState` (`bus/shownstate.go:13`) shows `no terminal` when the agent
  has a pane, a holder of the project answered, and none holds a live terminal of it. busmon draws it
  red, group 1 (`stateLook`, `cmd/busmon/agents.go:235`), whatever the role and whatever it owes.
- **Holder health.** `holder_restarted` warns when a holder that **answers** started after an
  agent's last proof in its terminals and that agent's process is gone (`holderRestarted`,
  `health/holder.go:130`). An agent whose build **no longer answers at all** is left out ("a build
  that does not answer: the fail's business", `health/holder_test.go:91`), and the fail fires only
  when the **current** build does not answer. The dead v0.33.0 holder raised nothing.
- **A holder switch.** An older build's holder never ends by itself while its agents live.
  `agentbus holder drain` moves each idle agent to the current build by the reload protocol; it is
  never automatic: the person runs it (`holderdrain/drain.go`).
- **Close on merge** (board-drift §3, #763) closes a request in **`review`** whose `request.branch`
  head is delivered, judged by `Delivery.Delivered` (ancestor, squash and rebase by patch-id)
  (`internal/dispatchsvc/closeonmerge.go`, `FencedRequestMerged`, `bus/requestmerged.go:59`). An
  `accepted` request is never closed. A request with no branch is never judged:
  `agent-skill-needs-answer` has none, since docs delivers with `agentbus deliver --task`, whose task
  is a **plan** task (`cmd/agentbus/deliver.go:343`), and `deliver` writes nothing on the board.
- **The pull request.** `deliver` opens or finds the PR with `gh pr view <branch>` /
  `gh pr create` (`deliverGitHub`, `deliver.go:709`), or, in local mode, hands the approved commit to
  the person (`--ref <branch>@<sha>`). The merge queue reads open PRs only (`ReadGitHub`,
  `mergequeue/forge.go:50`).
- **A stranded pop** (board-drift §6, #758): `popRetire` tells master once per episode when a
  **closed** pop (terminal and process gone, `f.Closed`, `dispatch/popfacts.go:121`) owns open board
  tasks, with its `pop --resume` line.
- **A pop's end.** The dispatcher ends a pop judged done, one closed by hand that owes nothing
  included, through the fenced `ForgetIfClearFenced` (`bus/forgetclear.go:153`). `forgetIfClear`
  holds on pending or unread addressed entries, an open ask, a board task not `done`. Only pops are
  judged (`s.Tier != "pop"`, `dispatch/popretire.go:116`).
- **A hand-off** is `agentbus handoff <self> --report <id>`, recorded for the process
  (`Handoff{Report, Session, PID, PIDStart}`, `bus/handoff.go:27`).
- **Who relaunches.** Self-repair (decision 2026-11-09) relaunches a **required** boot role whose
  process died, in a pane it creates. Its limit: "relaunching an agent whose departure was declared
  (reload, swap, retire, suspend, pause, hand-off) breaks the rule instead of extending it"; a pop
  "comes back through master only" (`rolecfg/config.go:169`). Otherwise the dispatcher types only
  the wake line (decision 2026-10-10) and `/clear`.
- **Not built from board-drift:** step 3 (`report --task/--done`), step 5 (`board drift`), step 6
  (answer guard). This spec depends on none of them.

## 3. The lifecycle

| Presence | When | Shown |
|---|---|---|
| **running** | a holder of the project answered and holds a live terminal of the agent; or no holder was read (another host, the holder down): its declared state shows, as today | its declared state |
| **parked** | a park record (§4.3) names this registration, and no live terminal holds it | `parked`, muted, "since `<age>`, resumes on demand" |
| **stopped** | no live terminal, no park record, the role is **not required**, and it **owes nothing** (§3.1) | `stopped`, grey, the "no state" group |
| **lost** | no live terminal, no park record, and it **owes something** or is **required** | `lost`, red, group 1, its reason (§10) |

Red means someone must act (the person, Q6). An optional role that finished and whose terminal
closed is stopped, not lost. An agent retired (`retire --forget`, a pop's end, §9) has no row.
"no terminal" is no longer shown anywhere.

### 3.1 Owes

An agent **owes** something when any of these holds:
1. a tracked request targeted at it in `requested`, `accepted`, `review` or `blocked`, or a board
   task it owns that is not `done` (`BoardOwes`);
2. an open 4-eyes gate it authored, or its own open ask;
3. a declared hand-off of its last process that no `/clear` or relaunch has taken;
4. **actionable** addressed cmd entries, pending or unread in its group: entries not `stale`
   (inside the 24 h horizon) and not `wake=no`. Master can acknowledge the rest once it has decided
   (`subscribe --drain` on its behalf is not offered: master reads and answers them, as today);
5. **losable work** (Q6): a branch it holds open on the board (a claim's branch, a request's
   `request.branch`) whose local tip is not on its remote, or a stash whose message names the role.

Items 1 to 4 are read from Redis by every reader. Item 5 needs git, so only the dispatcher's
merge-queue pass reads it: it already reads `LocalTips` and `RemoteTips` (`mergequeue/git.go`), and
adds `git stash list` (git plumbing only; no hook, no repository code). The pass writes its finding,
fenced, to a new hash `{p}:losable` → `<role>` = `{branches, stashes, at}`. A reader takes it when
less than 15 minutes old; older or absent, item 5 reads as **unknown**.

### 3.2 One judge

`bus.Presence(snapshot, park record, holder read, owes)` returns the presence and its reason. It
replaces `bus.ShownState`, is pure, and never rewrites the snapshot (`putSnapshot` stays the only
writer of `{p}:agents`). An unknown input fails closed: a park record that could not be read, or an
owes item that is unknown, makes a terminal-less agent **lost**, with "unknown: `<item>`" as reason.

## 4. Parking

### 4.1 Who may park

Any role but master (the pilot and escalation target) and a client with no resume (no session id
captured: Grok, to verify), which can only be retired. A required boot role may be parked: parking is
a declared departure, so self-repair leaves it alone (as for `retire` or `suspend`).

### 4.2 `agentbus park <self> --report <id>`

The agent parks itself. Refused on the first check that fails, nothing written:
1. **No accepted request.** None targeted at it is `accepted`. `blocked` (with its reason) or
   `review` is allowed: a verdict or an unblock wakes it (§5).
2. **Nothing actionable waiting** (§3.1 item 4): drain first.
3. **No uncommitted changes.** `git status --porcelain` is empty in its current directory's working
   tree and in each worktree of that repository whose branch it holds open on the board. **No
   losable work** (§3.1 item 5): each such branch is pushed, and no stash names the role. The agent
   runs these in its own directory; the dispatcher never runs `git status` or repository code.
4. **A hand-off.** `--report <id>` names its own report, declared as its hand-off.
5. **A session to resume.** Its proved `ident_session` and its `{p}:resume` record agree.

Then one Lua script, `ParkIntent`, writes `{p}:park` → `<role>` = `{session, profile, client,
build, report, pid, pid_start, at, by}` (`at` from Redis `TIME`; `by` = `self`, `idle` or
`holder`), re-checking inside the script that no request targeted at it is `accepted` and that its
registration is still that process. The agent declares `idle "parked"` and ends its turn.

### 4.3 The stop

The dispatcher's pass sees a park intent whose registration is still that process, idle, no turn
running, no wake being typed (the conditions `popEnd` already waits on). It closes the terminal, the
same act as a pop's end, waits for the process to be gone, then marks the record `parked = true`
(fenced, lease `DispatchKey`). The registration and the consumer group stay. An intent whose process
died before the close is marked parked all the same: its checks passed before.

### 4.4 Auto-park after an idle period

A new registry setting, **`dispatch.idle_park_after`**, person-only (`PersonOnly`), in `config.toml`
(not `roles.toml`, whose new keys break an update from the previous release). Its value is a list
`role=duration,…`, like `start.team`; a role absent or `0` never auto-parks; master is refused at
validation. Default: §12.2 Q8.

When a role has been declared `idle` for its duration, nothing actionable waits for it and no
request of it is `accepted`, the dispatcher sends it **one directive per idle episode** through
`FencedCmdOnce`, mark `park-ask:<role>`:

> Idle for 1h: park. Commit and push your work, write a hand-off report, then run:
> agentbus park `<role>` --report `<id>`. If you cannot (work you must keep, a decision pending),
> say why in a report to master and stay.

It never parks an agent itself: the checks of §4.2 need the agent's own directory, and the hand-off
is the agent's.

## 5. Wake on demand

The dispatcher's wake decision gets one rule before rule 7 ("pane gone", `dispatch/decide.go:224`):
an agent with an obligation (the same wake rules: an addressed entry with `wake` not `no`, a tracked
request) whose presence is **parked** is **relaunched**, not escalated:

1. **Holds first.** Nothing is relaunched while the project is paused or suspended, or its provider
   is held (quota or login); the obligation waits, as a wake does today.
2. **The relaunch** starts a **new terminal** on the **current** build with the role's launcher and
   `--resume <session>` on its recorded profile: what `agentbus pop <role> --resume <session>` does
   for a pop, the template command with the resume session for a boot role, as self-repair's
   `relaunch.ServiceRole` does. One relaunch at a time per agent (`BeginSwap`'s intent).
3. **The proof** is self-repair's: the new process registers itself with that session. On proof, the
   park record is deleted (fenced) and the wake line is typed into the new terminal once ready.
4. **A failed proof** keeps the park record, tells master once (`FencedCmdOnce`, mark
   `park-wake:<role>`, with the reason and the `pop --resume` line), and counts against self-repair's
   budget (1 per 30 min, 3 per 24 h per agent).

This **amends two decisions** and needs the person's yes (§12.2 Q7): the self-repair decision
(2026-11-09), whose limit names a declared departure, and "a pop comes back through master only".
The amendment stays inside the existing exception: a pane the dispatcher has just created, the
role's own launch command, the wake line; never message text, never an existing pane. The 2026-10-10
rule (the dispatcher is the one automated pane writer) stands. Without the yes, a parked agent with
an obligation is escalated to master with its `pop --resume` line.

## 6. A holder that stops: lost agents

### 6.1 Detection (health, always)

`holderRestarted` (`health/holder.go:130`) also covers a build that **no longer answers**: an agent
of build `K`, with no holder of `K` answering, whose proved process is gone, not `done`, and not
parked, is counted with it:

> holder build `<K>` gone: 2 agent(s): docs lost, architect stopped

beside today's "holder build `<K>` restarted …". Each agent is named with its presence (§3), so a
stopped one is visible without being a fault. Health records every holder loss, planned or not (Q5),
for the history and for sentinel. Status: Warn, In hand, when any agent is lost; OK with the line
otherwise.

### 6.2 Who is told (Q5)

- **An unplanned loss** (a crash, a holder stopped with agents in it) that leaves an agent **lost**
  or affects a request: one directive to master per episode, `FencedCmdOnce`, mark `holderlost:<K>`
  (episode `<K>@gone`, or `<K>@<socket start>` for a restart). It wakes master and lists, after §8's
  pass, each agent with its presence, its open requests and what §8 did:

  > Holder build v0.33.0.… gone (unplanned). Agents:
  > - docs (lost): agent-skill-needs-answer (accepted): closed on merge, #794 head ec39b1f9 in dev;
  >   readme-ops-guide-finish (requested): open. Bring docs back: agentbus --project
  >   agent-bus-monitor pop docs --resume `<session>`
  > - architect (stopped): nothing owed.

- **A planned switch** (§7) where every agent parked or stopped and no request was touched wakes
  nobody: a notice to master with `wake=no` ("holder switch v0.33 → v0.34: 4 parked, 0 lost") and the
  health line.
- **Master on the lost holder.** The directive waits in master's queue (self-repair relaunches it
  first). Meanwhile the person is told once (`FencedNotifyOnceForPerson`, mark
  `holderlost-person:<K>`): "holder build `<K>` lost with master in it; master is told when it
  returns."

`popStranded` (board-drift §6) skips a pop whose build key carries a `holderlost:<K>` mark, so
master is not told twice; it now applies to any tier but a required one (self-repair's).

## 7. A holder switch parks and resumes

At an update, the dispatcher of the new build finds agents running on an older build (its
holder-behind state). It sends each, master excepted, the park directive of §4.4 **at once**,
whatever `idle_park_after` says (mark `park-ask:<role>@<K>`, one per build). An agent that parks
closes its old terminal; when the old holder holds no terminal, it exits and its build is collected,
as an emptied drain does today. The agent resumes on the **current** build at its next obligation
(§5). An agent that cannot park keeps running on the old build; the directive is not repeated for
that build. Master moves by `holder drain`, run by the person, as today. Subject to §12.2 Q9.

## 8. Orphan requests

### 8.1 The owner is gone

A request's owner is **gone** when the process that accepted it no longer holds it (Q3):
- the owner is **lost** or **stopped** (§3); or
- it runs as **another process** than the one that accepted it (a relaunch, a resume). `request
  accept` now records `request.accepted_by = {pid, pid_start, session}` from the caller's snapshot
  when that snapshot is **verified** (`pid` set); from an unverified one it records nothing (foureyes'
  nit). A resume from `review` or `blocked` records it again. A request with no `accepted_by` (older,
  or accepted unverified) has its owner gone only when the owner is lost or stopped.

A **parked** owner is never gone: it resumes its session and closes its own request (Q1, Q3). A
`/clear` keeps the process.

### 8.2 The link, recorded at delivery

A request is closed only on a link **recorded on the request** (Q1); a name the pass would infer is
never a close (foureyes' reject: a relaunched owner's step 1 delivered under the task's name would
close the request while step 2 runs).

1. **branch**: `request.branch` and the head recorded by `request review` or `request done`, judged
   as close on merge judges it today (board-drift §3.2, `Delivery.Delivered`).
2. **delivery** (new): `agentbus deliver` records `request.delivered = {pr, head, branch, base, at}`
   on the board request whose task is **exactly** the delivered subject or, for `pr-<task>`, that
   task (Q2), whose `target` is the author of the approve that names the delivered head, and whose
   state is `accepted` or `review`. A step subject such as `retro-build-1` is not `retro-build`, so a
   multi-step build's steps never record onto its request. `deliver` refuses nothing for it: no
   matching request, nothing recorded. It is written with a Lua script that edits the entry's JSON
   (never from a Go struct), checking state and target.

   The pass judges a `delivery` link by the **pull request**, not by the head's ancestry (Q2,
   squash merges): `gh pr view <pr> --json state,headRefOid,baseRefName` on the merge queue's
   hardened environment, once per candidate per pass. Merged means `state = MERGED`,
   `headRefOid = head`, `baseRefName = base`. In local mode (no PR), the recorded head is judged with
   `Delivery.Delivered`, which counts a squash by patch-id.

A request whose `review_more` is set (a step that is not the last) is never closed by either link,
whoever owns it.

**A multi-step build's guard** (foureyes' reject). A relaunched owner may still be working on a
later step. Four things bound it: the `delivery` link is recorded only on an **exact** subject, and
step subjects (`<task>-1`, `retro-build-1`) never match; `review --more` keeps a step's request open;
when `deliver` records onto a request in `accepted` (not `review`), it prints "recorded on request
`<task>`: its merge closes it once its owner is gone; if more steps follow, run agentbus request
review `<task>` --more"; and a close is reversible (§8.5) and always told to master. The case left —
a step delivered under the task's own subject, with no `--more`, by an owner relaunched since — is
closed, as the person decided for a relaunch (Q3), and master can reopen it in one command.

### 8.3 Closed when merged

The merge-queue pass adds to close on merge's candidates the `accepted` and `review` requests whose
owner is **gone** (§8.1), with a link of §8.2 that holds. A request whose owner is the same running
process, or is parked, is never closed: it is flagged (§8.4). For `review` requests the existing rule
is unchanged and also takes the `delivery` link.

`FencedRequestMerged` gets more checks, still one Lua call: the request's state is still the one
read; for a gone owner, its registration in `{p}:agents` is still the one read (`pid`, `pid_start`,
`ts`) or still absent; the link read is still the one recorded. It writes
`closed_on_merge = {head, base, via, pr, owner_gone}` (`via` = `branch` or `delivery`) and, as
today, done, `responded_at` from `TIME`, one `request-merged` event, no reply, no wake. The notice
of §6.2 or §8.4 names it, so master is told either way (Q1).

### 8.4 Otherwise flagged or blocked

- An open request (`accepted` or `review`) of a **lost** owner with no link that holds is
  **blocked**, never closed by guess: a fenced `FencedRequestOrphaned` sets `state = blocked`,
  `blocked_reason = "owner lost: <reason>"`, checking the same registration as §8.3. It writes **no**
  `blocked` cmd entry (the request's own block does, `bus/requests.go:425`): master hears of it in
  §6.2's notice or `popStranded`'s. The owner's `request accept` resumes it, as from any block.
- A request whose owner is parked, or runs (same process), and whose link merged, or which has only
  a name match (a verdict subject equal to the task, not recorded by `deliver`), is **flagged**: one
  directive to master per request, `FencedCmdOnce`, mark `merged-open:<task>`: "`<task>` (`<state>`,
  `<owner>` `<presence>`): its PR #`<n>` merged; close it (agentbus request done …) or say why it
  stays open." A `requested` request is left as it is.

### 8.5 Reopen

`agentbus request reopen <task> --reason <text>`, by master or the person (`--as <person>`): a
request `done` with `closed_on_merge` goes back to the state it was closed from (`accepted` or
`review`); `closed_on_merge` moves into `request.reopened = {closed_on_merge, by, at, reason}`; the
WIP limit does not refuse it (a resume). Refused on any other `done` request, on a missing body, and
on a request reopened since.

## 9. A lost or stopped agent that owes nothing is retired by rule

`popRetirePass` judges, besides pops, every **optional boot** role (tier `boot`, not `required`)
with no live terminal and no park record. One that owes nothing (§3.1, item 5 included and known) is
ended by the existing fenced path after `dispatch.pop_retire_after` (its terminal is already
closed, so `End` closes no pane: it only unregisters; the 2026-11-13 decision note, which names pops,
is updated with the build): no row, consumer group kept, so
a later `pop` or `up` starts where it left off. Its reports, verdicts and threads stay readable.
The end is logged in the dispatch log and appended to the audit stream.

Never applied to a required role, master, the pilot or the person (a required role's or master's
disappearance is lost and stays shown); to a parked agent; or to a snapshot with no `tier` (older
than v0.20). One that owes something stays lost until §8 has closed or blocked what it owes and its
losable work is gone; the next pass then ends it.

## 10. What the person sees

- **busmon Agents.** `parked` is muted, "since `<age>`, resumes on demand". `stopped` is grey, in
  the "no state" group. `lost` is red, group 1, its reason first:
  1. `holder build <key> gone` or `restarted`;
  2. `required: self-repair relaunches it`, or, while `dispatch.self_repair` is off,
     `required: agentbus up brings it back` (foureyes' nit; the same in §6.2's bring-back line);
  3. `owns <task> (<state>)`, the first two, `+N`;
  4. `unpushed <branch>` or `stash`;
  5. `<n> message(s) waiting`, `an open ask`, `a gate`, or `a hand-off not taken`;
  6. `unknown: <item>`.
- **busmon Board.** A request whose owner is lost shows `owner lost` in its alert column; one closed
  with `owner_gone` shows it in its detail, and one reopened shows its `reopened` reason.
- **`agentbus agents`.** The same words; `--json` adds `presence` and `presence_reason`.
- **health.** `holder_restarted` names each build's agents with their presence (§6.1). A new check,
  **`agents-lost`** (Handler `dispatch`, every minute, Warn, In hand), lists every lost agent with its
  reason, those lost without a holder event too (a client crash, a pane closed by hand). Parked and
  stopped never appear as a fault.

## 11. Build steps

One PR each, from `origin/dev`, in this order.

1. **Presence** (§3, §10): `bus.Presence`, owes items 1-4, the words, `agents --json`,
   `agents-lost`. Item 5 reads unknown, and no park record exists yet.
2. **Losable work** (§3.1 item 5): the pass's stash read and `{p}:losable`.
3. **Holder gone** (§6.1, health only).
4. **Orphan requests** (§8): `accepted_by`, `deliver`'s `request.delivered`, the PR judgement, the
   new candidates, `FencedRequestMerged`'s checks, `FencedRequestOrphaned`, the flag, `request
   reopen`; then the notices (§6.2, `popStranded`).
5. **Parking** (§4.1-§4.3): `agentbus park`, `ParkIntent`, the dispatcher's stop, the skills' line.
6. **Wake on demand** (§5), after the person's Q7; with Q7 = B, only the escalation.
7. **Auto-park and the holder switch** (§4.4, §7), after Q8 and Q9.
8. **Retire by rule** (§9).

**Tests:**
- §3: a table over presence (live; parked; a park record of another process; no terminal and owes
  nothing → stopped; owes a request, a stash, an unpushed branch → lost; required → lost; holder not
  read; a stale message only → stopped; an unknown item → lost).
- §4.2: each refusal (accepted request, actionable entry, dirty tree and unpushed branch on a
  disposable repository, no report, no session); `ParkIntent` refused when a request became accepted
  in between.
- §4.3/§5, against the test broker (`AGENTBUS_TEST_REDIS_URL` only) and a fake holder: the stop
  closes only that process's terminal; an obligation relaunches a parked agent once, never while
  paused or held; a failed proof keeps the record and tells master once.
- §6: a build that no longer answers reports its agents with their presence; a planned switch with
  all parked wakes nobody; master lost notifies the person once.
- §8, on a disposable repository and a fake `gh`: `deliver` records the link only on an exact
  subject and the approve's author; a merged PR with the recorded head closes a gone owner's request
  (`via = delivery`); a squash-merged PR closes it; the same request with its owner running or parked
  is flagged, not closed; a step subject records nothing, so a relaunched owner (new pid, same
  session) working on step 2 keeps its request open; `deliver` onto an `accepted` request prints the
  `--more` hint; an unverified accept records no `accepted_by`; `review_more` never closes; no link blocks
  it with no cmd entry; a respawn between read and write writes nothing; a lost lease is
  `NOTHOLDER`; `reopen` restores the state and refuses a plain `done`.
- §9: a stopped optional boot role is ended; a required one, master, a parked one, one with a stash,
  and one with no tier are not.

## 12. The person's decisions

### 12.1 Answered (ask `1795649139554-0`, typed, 2026-11-26)

- **Q1** close automatically, only on a certain link (the request's own PR as recorded at delivery);
  a link resting on the name alone, or a multi-step request, is flagged; record the reason and let
  master reopen in one command; a parked owner is never gone. → §8.2-§8.5.
- **Q2** the verdict registry, the subject exactly the task or `pr-<task>`; judge the PR merged, not
  the sha's ancestry (squash); better, `deliver` records the request-to-PR link. → §8.2.
- **Q3** another process counts as gone (relaunch, resume); `/clear` does not; parked is not gone.
  → §8.1.
- **Q4** retire by rule, pops and optional boot roles, never required, master or the pilot; "owes
  nothing" strict (requests, gate, unpushed commits or stash, pending hand-off); history kept and
  logged. → §3.1, §9.
- **Q5** wake master only for an unplanned loss that leaves agents lost or affects requests; health
  records every loss; if master was on the lost holder, tell the person meanwhile. → §6.
- **Q6** red means someone must act; three displays besides running: parked, stopped, lost;
  unpushed commits and stash owe; count only actionable, non-stale messages. → §3.

foureyes' reject of r1 (`dn4oi37vwmn6`): a running, relaunched owner's request closed by a name
match. Answered in §8.2: the pass closes only on a link recorded on the request at delivery, on an
exact subject; a name match is flagged; a multi-step build is guarded by step subjects, `--more`,
`deliver`'s hint, and reopen. foureyes' alternative, "a relaunch is the same owner" (lineage), is
**not** taken: the person's typed Q3 answer counts a relaunch or a resume as gone. Its nits are taken
(§8.1, §9, §10, §11 tests).

### 12.2 Second ask (what r2 raised)

**Q7. Réveil à la demande d'un agent parqué.**
- **(A) recommandé :** le dispatcher le relance lui-même dans un nouveau terminal, sur sa session
  sauvegardée, puis tape la ligne de réveil. Cela amende la décision self-repair du 2026-11-09 et
  « un pop ne revient que par master ».
- **(B)** Le dispatcher prévient master, qui le relance (`pop --resume`). Aucune décision amendée.

**Q8. Parcage automatique après inactivité (`dispatch.idle_park_after`, votre réglage).**
- **(A) recommandé :** les pops après 1 h ; les rôles de démarrage jamais par défaut ; master jamais.
- **(B)** Aucun par défaut : vous le réglez rôle par rôle.

**Q9. Changement de holder (mise à jour).**
- **(A) recommandé :** chaque agent (sauf master) sur l'ancien holder reçoit l'ordre de se parquer et
  reprend sur le nouveau à la demande ; l'ancien holder se vide seul.
- **(B)** Rien de changé : vous lancez `holder drain`.
