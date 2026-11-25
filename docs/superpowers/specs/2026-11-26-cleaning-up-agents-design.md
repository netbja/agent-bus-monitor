# Cleaning up agents (cleaning-up-agents)

Status: spec r2, for review (subject `cleaning-up-agents`). Not built. Base: origin/dev `49926f09`.
r2: master's addition (cmd `1795648805663-0`): an agent lifecycle with a deliberate **parked**
state, auto-park on idle, wake on demand, orphan requests blocked when not closed, parking at a
holder switch. "not running" of r1 becomes **parked**; the questions are redone (§11).
Author: architect, from master's directives (cmd `1795648670814-0` and `1795648805663-0`, task
`cleaning-up-agents-spec`), both relayed as the person's decision and unverified: the ask in §11
confirms them. No new dependency.

## 1. The problem

The holder that hosts the agents' terminals stops or is replaced (an update, a crash). Agents
registered in its terminals stay on the board as if they still worked, and their accepted requests
stay `accepted` after their pull request merges. busmon shows every agent without a terminal the
same way, "no terminal" in red: a pop that has nothing to do and a coder that died mid-task look
alike. And an on-demand role either runs, idle, for hours, or is retired and must be popped back by
master.

**The case in point.** docs ran in terminal `t21` of the v0.33.0 holder, which died. docs held
`agent-skill-needs-answer` accepted (request `1795621095391-0`). Its pull request, #794, merged into
`dev`, approved under subjects `agent-skill-needs-answer` and `pr-agent-skill-needs-answer`, author
`docs`. The request is still `accepted`. docs came back on the v0.34.0 holder (`t15`) as a new
process and left it open as "pre-session". Nothing else closes it.

What this spec delivers:
1. **A lifecycle** with three states: **running**, **parked** (a deliberate stop, resumed on
   demand), **lost** (no process, not parked: a fault) (§3);
2. **Parking**: its preconditions, auto-park after an idle period, and wake on demand (§4, §5);
3. **A holder that stops or is replaced**: lost agents detected and told to master (§6); at a
   holder switch, agents park on the old holder and resume on the new one (§7);
4. **Orphan requests**: a lost owner's request is closed when its pull request merged, otherwise
   blocked and listed to master; never left `accepted` (§8);
5. a lost agent that owes nothing is **retired by rule** (§9); busmon and health show parked
   (normal) apart from lost (fault), and never "no terminal" for a parked role (§10).

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
  `agentbus holder drain` moves each idle agent to the current build by the reload protocol, one at
  a time; it is **never automatic**: the person runs it (`holderdrain/drain.go`).
- **Close on merge** (board-drift §3, #763) closes a request in **`review`** whose `request.branch`
  head is delivered (`internal/dispatchsvc/closeonmerge.go`, `FencedRequestMerged`,
  `bus/requestmerged.go:59`). An `accepted` request is never closed. A request with no branch is never
  judged: `agent-skill-needs-answer` has none, since docs delivers with `agentbus deliver --task`,
  whose task is a **plan** task (`cmd/agentbus/deliver.go:343`).
- **The merge queue already knows the merge.** Each pass judges the verdict ledger's approved heads
  within 72 h with `Delivery.Delivered` (`runDelivered`, `mergequeuechore.go:538`). A verdict carries
  `subject`, `author` and `head`: for #794, `agent-skill-needs-answer`, `docs`, `ec39b1f9`.
- **A stranded pop** (board-drift §6, #758): `popRetire` tells master once per episode when a
  **closed** pop (terminal and process gone, `f.Closed`, `dispatch/popfacts.go:121`) owns open board
  tasks, with its `pop --resume` line.
- **A pop's end.** The dispatcher ends a pop judged done, one closed by hand that owes nothing
  included, through the fenced `ForgetIfClearFenced` (`bus/forgetclear.go:153`), and closes its
  terminal. `forgetIfClear` holds on any owed thing: pending or unread addressed entries, an open ask,
  a board task not `done`. Only pops are judged (`s.Tier != "pop"`, `dispatch/popretire.go:116`).
- **A hand-off** is declared with `agentbus handoff <self> --report <id>`, recorded for the process
  (`Handoff{Report, Session, PID, PIDStart}`, `bus/handoff.go:27`); the dispatcher may type `/clear`
  after it.
- **Who relaunches.** Self-repair (decision 2026-11-09) relaunches a **required** boot role whose
  process died, in a pane it creates, with the template command and the resume session. Its limits:
  "relaunching an agent whose departure was declared (reload, swap, retire, suspend, pause,
  hand-off) breaks the rule instead of extending it"; a pop "comes back through master only"
  (`rolecfg/config.go:169`). The dispatcher otherwise types only the wake line (decision 2026-10-10)
  and `/clear`.
- **Not built from board-drift:** step 3 (`report --task/--done`), step 5 (`board drift`), step 6
  (answer guard). This spec depends on none of them.

## 3. The lifecycle

Three states, read by one reader's projection, `bus.Presence`, which replaces `bus.ShownState`. It
never rewrites the snapshot (`putSnapshot` stays the only writer of `{p}:agents`).

| Presence | When | Shown |
|---|---|---|
| **running** | a holder of the project answered and holds a live terminal of the agent; or no holder was read (another host, the holder down): the declared state shows, as today | its declared state |
| **parked** | a park record (§4.3) names this registration (same `pid`, `pid_start`), and no live terminal holds it | `parked`, muted, the "no state" group, with "since `<age>`" |
| **lost** | no live terminal, and no park record for it | `lost`, red, group 1, with the reason (§10) |

An agent retired (`retire --forget`, a pop's end, §9) has no row, as today. "no terminal" is no
longer shown anywhere.

`bus.Presence` is a pure function of the snapshot, the park record, and the holder read. A reader
that could not read the park record shows `lost` (fail closed: a fault shown, never hidden).

## 4. Parking

### 4.1 Who may park

Any role but master (the pilot and escalation target) and a client with no resume (no session id
captured: Grok, to verify), which can only be retired. A required boot role may be parked: parking is
a declared departure, so self-repair leaves it alone (as for `retire` or `suspend`).

### 4.2 `agentbus park <self> --report <id>`

The agent parks itself. The command checks, in this order, and refuses on the first that fails
(nothing written):

1. **No accepted request.** No tracked request targeted at it is `accepted`. One that is `blocked`
   (with its reason) or in `review` is allowed: a reviewer's verdict or an unblock wakes it (§5).
2. **Nothing waiting.** No addressed cmd entry pending or unread in its group: drain first.
3. **No uncommitted changes.** `git status --porcelain` is empty in its current directory's working
   tree, and in each worktree of that repository whose branch it holds open on the board (a claim's
   branch, a request's `request.branch`). The agent runs this, in its own directory; the dispatcher
   never runs git or repository code.
4. **A hand-off.** `--report <id>` names its own report: it is declared as the hand-off
   (`agentbus handoff`'s record), as today.
5. **A session to resume.** Its proved `ident_session` and its `{p}:resume` record agree; that
   session is what wake on demand resumes.

Then one Lua script, `ParkIntent`, writes `{p}:park` → `<role>` = `{session, profile, client,
build, report, pid, pid_start, at, by}` (`at` from Redis `TIME`; `by` = `self`, `idle`, `holder`),
checking again inside the script that no request targeted at it is `accepted` and that its
registration is still that process. The agent then declares `idle "parked"` and ends its turn.

### 4.3 The stop

The dispatcher's pass sees a park intent whose registration is still that process, idle, no turn
running, no wake being typed (the conditions `popEnd` already waits on). It closes the terminal, the
same act as a pop's end, and waits for the process to be gone. It then marks the record
`parked = true` with a fenced write (lease `DispatchKey`). The registration and the consumer group
stay; nothing is forgotten. The park record is the "parked" of §3.

A pass that finds the process still running a turn waits; an intent whose process is gone without
the close (it died first) is marked parked all the same: its hand-off and its clean tree were checked
before.

### 4.4 Auto-park after an idle period

A new registry setting, **`dispatch.idle_park_after`**, person-only (`PersonOnly`), in `config.toml`
(not `roles.toml`, whose new keys break an update from the previous release). Its value is a list
`role=duration,…`, like `start.team`; a role absent or `0` never auto-parks; master is refused at
validation. Default: §11 Q4.

When a role has been declared `idle` for its duration, nothing waits for it and no request of it is
`accepted`, the dispatcher sends it **one directive per idle episode** through `FencedCmdOnce`,
mark `park-ask:<role>`:

> Idle for 1h: park. Commit your work on its branch, write a hand-off report, then run:
> agentbus park `<role>` --report `<id>`. If you cannot (uncommitted work you must keep, a
> decision pending), say why in a report to master and stay.

The dispatcher delivers it with the wake line, as any directive. It never parks an agent itself:
the checks of §4.2 need the agent's own directory, and its hand-off is its own.

## 5. Wake on demand

The dispatcher's wake decision gets one rule before rule 7 ("pane gone", `dispatch/decide.go:224`):
an agent with an obligation (the same wake rules: an addressed entry with `wake` not `no`, a tracked
request) whose presence is **parked** is **relaunched**, not escalated:

1. **Holds first.** Nothing is relaunched while the project is paused or suspended, or its
   provider is held (quota or login, `providerFacts`); the obligation waits, as a wake does today.
2. **The relaunch** starts a **new terminal** on the **current** build with the role's launcher and
   `--resume <session>` on its recorded profile: what `agentbus pop <role> --resume <session>` does
   for a pop, and the template command with the resume session for a boot role, as self-repair's
   `relaunch.ServiceRole` does. One relaunch at a time per agent (the swap intent of `BeginSwap`).
3. **The proof** is self-repair's: the new process registers itself, with that session. On proof,
   the park record is deleted (fenced) and the normal wake line is typed into the new terminal once
   it is ready: a pane the dispatcher has just created.
4. **A failed proof** keeps the park record, tells master once (`FencedCmdOnce`, mark
   `park-wake:<role>`, the reason and the `pop --resume` line), and counts against self-repair's
   budget (1 per 30 min, 3 per 24 h per agent). A spent budget stops relaunches of that agent until
   master resets it, as today.

This **amends two decisions**, and needs the person's yes (§11 Q3): the self-repair decision
(2026-11-09), whose limit names a declared departure, and "a pop comes back through master only".
The amendment stays inside the existing exception: a pane the dispatcher has just created, the
role's own launch command, the wake line. It never types message text and never types into an
existing pane; the 2026-10-10 rule (the dispatcher is the one automated pane writer) stands. Without
the yes, a parked agent with an obligation is escalated to master with its `pop --resume` line, and
master brings it back.

## 6. A holder that stops: lost agents

### 6.1 Detection (health)

`holderRestarted` (`health/holder.go:130`) also covers a build that **no longer answers**: an
agent whose build key `K` has no holder that answers, whose proved process is gone, whose state is
not `done`, and which has **no park record**, is lost with it:

> holder build `<K>` gone: 2 agent(s) lost: coder, docs

beside today's "holder build `<K>` restarted …". Warn, In hand. A parked agent is never counted.

### 6.2 The notice to master

One directive per lost holder episode, `FencedCmdOnce`, mark `holderlost:<K>` (episode `<K>@gone`,
or `<K>@<socket start>` for a restart). It wakes master and lists every agent lost with that build,
its open requests, and what §8 did to them, after §8's pass:

> Holder build v0.33.0.… gone. Agents lost with it:
> - docs: agent-skill-needs-answer (accepted): closed on merge, #794 head ec39b1f9 in dev;
>   readme-ops-guide-finish (requested): open. Bring docs back: agentbus --project
>   agent-bus-monitor pop docs --resume `<session>`
> - coder: nothing owed. Self-repair relaunches it (required).

`popStranded` (board-drift §6) skips a pop whose build key carries a `holderlost:<K>` mark, so
master is not told twice; it now applies to any tier but a required one (self-repair's).

## 7. A holder switch parks and resumes

At an update, the dispatcher of the new build finds agents running on an older build (the
holder-behind state it already reports). It sends each, master excepted, the park directive of §4.4
**at once**, whatever `idle_park_after` says (mark `park-ask:<role>@<K>`, one per build). An agent
that parks closes its old terminal; when the old holder holds no terminal, it exits and its build is
collected, as an emptied drain does today. The agent resumes on the **current** build at its next
obligation (§5).

An agent that cannot park (an accepted request, uncommitted work) keeps running on the old build
until it can; the directive is not repeated for that build. Master moves by `holder drain`, run by
the person, as today. With §11 Q5 = B, nothing is sent and the drain stays the person's.

## 8. Orphan requests

### 8.1 The owner is gone

A request's owner is **gone** when the process that accepted it no longer holds it:
- the owner is **lost** (§3); or
- it runs as **another process** than the one that accepted it. `request accept` now records
  `request.accepted_by = {pid, pid_start}` from the caller's verified snapshot (a resume from
  `review` or `blocked` records it again). A request accepted before this change has no
  `accepted_by`: only a lost owner makes it gone.

A parked owner is never gone: it holds no accepted request (§4.2). A `/clear` keeps the process.

### 8.2 What links a request to a merge

Either link suffices:
1. **branch**: `request.branch`, judged as close on merge judges it (board-drift §3.2);
2. **subject** (new): an **approved** head of the verdict ledger whose `subject` is the task or
   `pr-<task>` and whose `author` is the request's `target`, judged delivered by the same
   `runDelivered` call, same pass, same 72 h window. Nothing new is fetched or polled. The author
   condition keeps a spec's subject from closing another agent's request of the same name.

### 8.3 Closed when merged

The merge-queue pass adds to close on merge's candidates the `accepted` and `review` requests whose
owner is gone (§8.1), by link 1 or 2; and `review` requests with no branch, by link 2. An `accepted`
request whose owner runs is never closed (board-drift §3.1 stands).

`FencedRequestMerged` gets more checks, still one Lua call: the request's state is still the one
read; for a gone owner, its registration in `{p}:agents` is still the one read (`pid`, `pid_start`,
`ts`) or still absent, so a respawn between read and write writes nothing; for link 2, the head is
the approved head read. It writes `closed_on_merge = {head, base, via, owner_gone}` (`via` =
`branch` or `subject`), as today otherwise: done, `responded_at` from `TIME`, one `request-merged`
event, no reply, no wake. A `review_more` request of a gone owner closes: nobody would resume it.

### 8.4 Otherwise blocked

An open request (`accepted` or `review`) of a **lost** owner with no link that holds is **blocked**,
never closed by guess: a fenced `FencedRequestOrphaned` sets `state = blocked`,
`blocked_reason = "owner lost: <presence reason>"`, checking the same registration as §8.3. It writes
**no** `blocked` cmd entry (the request's own block does, `bus/requests.go:425`): master hears of it in
§6.2's notice, or in `popStranded`'s. When the owner comes back, `request accept` resumes it, as from
any block. A `requested` request is left as it is: nobody took it.

## 9. A lost agent that owes nothing is retired by rule

`popRetirePass` judges, besides pops, every **optional boot** role (tier `boot`, not `required`)
that is **lost**. One that owes nothing (`PopDone`, as a pop closed by hand) is ended by the existing
fenced path: no row, consumer group kept. Never applied to a required role, to master, the pilot or
the person, to a parked agent, or to a snapshot with no `tier` (older than v0.20). A lost agent that
owes something stays lost until §8 has closed or blocked what it owes; the next pass then ends it.

## 10. What the person sees

- **busmon Agents.** `parked` is muted, in the "no state" group, with "since `<age>`, woken on
  demand" in its detail. `lost` is red, group 1, its reason first:
  1. `holder build <key> gone` or `restarted`;
  2. `required: self-repair relaunches it`;
  3. `owns <task> (<state>)`, the first two, `+N`;
  4. `<n> message(s) waiting`, or `an open ask`.
- **busmon Board.** A request whose owner is lost shows `owner lost` in its alert column; one closed
  with `owner_gone` shows it in its detail.
- **`agentbus agents`.** The same words; `--json` adds `presence` and `presence_reason`.
- **health.** `holder_restarted` names the agents lost per build (§6.1). A new check, **`agents-lost`**
  (Handler `dispatch`, every minute, Warn, In hand), lists every lost agent with its reason, those
  lost without a holder event too (a client crash, a pane closed by hand). Parked never appears in
  health: it is not a fault.

## 11. Questions for the person (one ask, French)

**Q1. Une requête d'un agent perdu dont la PR a fusionné.**
- **(A) recommandé :** la fermer automatiquement, reliée par sa branche ou par le registre des verdicts
  (sujet = la tâche ou `pr-<tâche>`, auteur = la cible) ; sinon la bloquer et la lister à master.
- **(B)** La relier par sa branche seulement ; sinon la bloquer et la lister.
- **(C)** Ne jamais fermer : la bloquer et la lister à master.

**Q2. Quand le propriétaire d'une requête est-il « parti » ?**
- **(A) recommandé :** il est perdu, ou un autre processus que celui qui l'a acceptée tourne à sa place
  (relance) ; `/clear` ne compte pas. Couvre le cas docs de #794.
- **(B)** Seulement quand il est perdu.

**Q3. Réveil à la demande d'un agent parqué.**
- **(A) recommandé :** le dispatcher le relance lui-même dans un nouveau terminal, sur sa session
  sauvegardée, puis tape la ligne de réveil. Cela amende la décision self-repair du 2026-11-09 et
  « un pop ne revient que par master ».
- **(B)** Le dispatcher prévient master, qui le relance (`pop --resume`). Aucune décision amendée.

**Q4. Parcage automatique après inactivité (`dispatch.idle_park_after`, votre réglage).**
- **(A) recommandé :** les pops après 1 h ; les rôles de démarrage jamais par défaut ; master jamais.
- **(B)** Aucun par défaut : vous le réglez rôle par rôle.

**Q5. Changement de holder (mise à jour).**
- **(A) recommandé :** chaque agent (sauf master) sur l'ancien holder reçoit l'ordre de se parquer,
  et reprend sur le nouveau à la demande ; l'ancien holder se vide seul.
- **(B)** Rien de changé : vous lancez `holder drain`.

**Q6. Un agent perdu qui ne doit plus rien.**
- **(A) recommandé :** retiré par règle (comme un pop fermé à la main aujourd'hui), pops et rôles de
  démarrage optionnels ; jamais un rôle requis, master ni le pilote.
- **(B)** Les pops seulement, comme aujourd'hui.

## 12. Build steps

One PR each, from `origin/dev`, in this order.

1. **Presence** (§3, §10): `bus.Presence` replacing `ShownState`'s callers, the words, `agents
   --json`, the `agents-lost` check. Parked reads an absent park record until step 4.
2. **Holder gone** (§6.1).
3. **Orphan requests** (§8): `accepted_by`, the subject link, the new candidates,
   `FencedRequestMerged`'s checks, `FencedRequestOrphaned`; then the notices (§6.2, `popStranded`).
4. **Parking** (§4.1-§4.3): `agentbus park`, `ParkIntent`, the dispatcher's stop, the skills' line.
5. **Wake on demand** (§5), after the person's Q3; with Q3 = B, only the escalation.
6. **Auto-park and the holder switch** (§4.4, §7): the setting, the two directives.
7. **Retire by rule** (§9).

**Tests:**
- §3: a table over presence (live; parked; parked record of another process; closed with no record;
  holder not read; a park read that failed: lost).
- §4.2: each refusal (accepted request, unread entry, dirty tree on a disposable repository, no
  report, no session); `ParkIntent` refused when a request became accepted in between.
- §4.3/§5, against the test broker (`AGENTBUS_TEST_REDIS_URL` only) and a fake holder: the stop
  closes only that process's terminal; an obligation relaunches a parked agent once, never while
  paused or held; a failed proof keeps the record and tells master once.
- §6.1: a build that no longer answers reports its agents; a parked or `done` one is not counted.
- §8, on a disposable repository: an accepted request of a lost owner closes by subject; the same
  with the owner running stays open; another author's subject closes nothing; a new process (other
  `accepted_by`) closes it; no link blocks it with no cmd entry; a respawn between read and write
  writes nothing; a lost lease is `NOTHOLDER`.
- §9: a lost optional boot role that owes nothing is ended; a required one, master, a parked one and
  one with no tier are not.
