# Cleaning up agents (cleaning-up-agents)

Status: spec r1, for review (subject `cleaning-up-agents`). Not built. Base: origin/dev `49926f09`.
Author: architect, from master's directive (cmd `1795648670814-0`, task `cleaning-up-agents-spec`),
relayed as the person's decision and to be confirmed through the ask in §9. No new dependency.

## 1. The problem

The holder that hosts the agents' terminals stops or is replaced (an update, a crash). Agents
registered in its terminals stay on the board as if they still worked. Their accepted requests stay
`accepted` after their pull request merges. busmon shows every agent without a terminal the same
way, "no terminal", in red: a pop that finished and a coder that died mid-task look alike.

**The case in point.** docs ran in terminal `t21` of the v0.33.0 holder. That holder died. docs held
`agent-skill-needs-answer` accepted (request `1795621095391-0`, accepted 7 h ago). Its pull request,
#794, merged into `dev`, approved under subjects `agent-skill-needs-answer` and
`pr-agent-skill-needs-answer`, author `docs`. Today the request is still `accepted`. docs came back on
the v0.34.0 holder (`t15`) and left it open, as "pre-session": a new session does not close what an
earlier one accepted. Nothing else closes it.

The spec answers four things:
1. a holder that stops or is replaced: the agents still bound to it are detected, their open requests
   listed to master, and closed when their pull request merged (§4, §5);
2. a request whose pull request merged while its owner is gone is closed, or flagged, never left
   `accepted` (§5);
3. a role that is not running and is not expected to run shows as **not running**; a gone agent is
   retired by rule, not by hand (§3, §6);
4. busmon and health tell **not running** (expected) from **lost** (a fault) (§3, §7).

## 2. Today (code at `49926f09`)

- **An agent's terminal.** `{p}:agents` records `pane = "<build key>/<terminal>"`
  (`AgentSnapshot.Pane`, `bus/stream.go:75`), with the proved `pid`, `pid_start` and `boot_id`.
  `health.HolderPane` splits it (`health/holder.go:68`).
- **"no terminal".** `bus.ShownState` (`bus/shownstate.go:13`) shows `no terminal` when the agent
  has a pane, a holder of the project answered the read, and none holds a live terminal of it.
  busmon draws it red, group 1 (`stateLook`, `cmd/busmon/agents.go:235`), whatever the role and
  whatever it owes.
- **Holder health.** `EvaluateHolder` warns `holder_restarted` when a holder that **answers** started
  after an agent's last proof in one of its terminals and that agent's process is gone
  (`holderRestarted`, `health/holder.go:130`). An agent whose build key **no longer answers at all**
  (the old build's holder died, or was drained and stopped) is left out: "a build that does not
  answer: the fail's business" (`health/holder_test.go:91`), and the fail fires only when the
  **current** build does not answer. So docs on the dead v0.33.0 holder raised nothing.
- **Close on merge** (board-drift §3, built in #763). The merge queue's pass closes a request in
  **`review`** whose `request.branch` head is delivered (`internal/dispatchsvc/closeonmerge.go`,
  `FencedRequestMerged`, `bus/requestmerged.go:59`). An `accepted` request is never closed: its owner
  may be resuming a multi-step build. A request with no branch is never judged.
  `agent-skill-needs-answer` has no branch: docs delivers with `agentbus deliver --task`, whose task
  is a **plan** task (`cmd/agentbus/deliver.go:343`), not a board request.
- **The merge queue already knows the merge.** Each pass judges the approved heads of the verdict
  ledger within 72 h with `Delivery.Delivered` (`runDelivered`, `mergequeuechore.go:538`). A verdict
  carries `subject`, `author` and `head`: for #794, subject `agent-skill-needs-answer`, author
  `docs`, head `ec39b1f9`.
- **A stranded pop** (board-drift §6, built in #758). `popRetire` sends master one directive per
  episode when a **closed** pop (`f.Closed`: terminal gone and process gone, `dispatch/popfacts.go:121`)
  is kept because it owns open board tasks (`PopStrandedMark`). It lists the tasks and the
  `pop --resume` line. It closes nothing.
- **Retiring by rule.** The dispatcher ends a pop judged done (`PopDone`), including one closed by
  hand that owes nothing, through the fenced `ForgetIfClearFenced` (`bus/forgetclear.go:153`).
  `forgetIfClear` holds on any owed thing: pending or unread addressed entries, an open ask, a board
  task that is not `done`. Only pops are judged (`s.Tier != "pop"`, `dispatch/popretire.go:116`).
  A boot role closed by hand stays registered forever.
- **Roles.** `tier` is `boot` or `pop`. A boot role may be `required` (self-repair recreates its
  closed tab); a pop never is (`rolecfg/config.go:163`). An optional boot role's closed tab is only
  reported.
- **Not built from board-drift:** step 3 (`report --task/--done`), step 5 (`board drift`), step 6
  (answer guard). This spec reuses none of them; §5.4 says where it meets step 5.

## 3. Words: running, not running, lost

One reader's projection, `bus.Presence`, replaces `bus.ShownState`. It never rewrites the snapshot
(`putSnapshot` stays the only writer of `{p}:agents`).

| Presence | When | Shown |
|---|---|---|
| **running** | a holder of the project answered and holds a live terminal of the agent; or no holder was read (another host, the holder down): the declared state shows, as today | its declared state |
| **not running** | no live terminal, and the agent **owes nothing** and is **not required** | `not running`, muted, the "no state" group |
| **lost** | no live terminal, and the agent **owes something** or is **required** | `lost`, red, group 1, with the reason |

**Owes** has one definition, the one `forgetIfClear` already holds on, read by the readers:
- a board task it owns that is not `done` (`BoardOwes`), or a tracked request targeted at it in
  `requested`, `accepted`, `review` or `blocked`;
- its own open ask;
- addressed cmd entries pending or unread in its group (busmon's queue count).

A reader that cannot read one of these judges **lost** (fail closed: a fault shown, never hidden).

`lost` names its reason in the detail and in `agentbus agents`, first match:
1. `holder build <key> gone` (its build key answers no more) or `holder build <key> restarted`;
2. `required: self-repair relaunches it`;
3. `owns <task> (<state>)`, the first two, `+N`;
4. `<n> message(s) waiting`, or `an open ask`.

A pop that finished (declared `done`, or its `popdone` mark set for this episode) and owes nothing is
`not running`. A pop retired and forgotten has no row at all, as today.

## 4. A holder that stops or is replaced

### 4.1 Detection (health)

`holderRestarted` (`health/holder.go:130`) covers the second case too: an agent whose build key
`K` has **no** holder that answers (no socket, or a socket that refuses), whose proved process is gone
(`HolderProof.Alive` false), and whose state is not `done`. The warn's part reads:

> holder build `<K>` gone: 2 agent(s) lost: coder, docs

beside today's "holder build `<K>` restarted …: N agent(s) lost". Its status stays Warn, In hand.
Nothing else changes in `EvaluateHolder`: the current build that does not answer is still a fail.
`WithHolderAgents` already keeps exactly the agents needed; only the build loop changes, so that a key
found in `e.Agents` but not among the answering builds is reported once as gone.

### 4.2 The notice to master

The dispatcher sends master **one directive per lost holder episode** through the existing
`FencedCmdOnce`, under the mark `holderlost:<K>`. Its episode is `<K>@gone` for a build that answers
no more, `<K>@<socket start>` for a restarted one. It wakes master (a holder lost is master's to act
on), and lists every agent of that build that §4.1 judged lost, each with its open requests and what
the pass did to them:

> Holder build v0.33.0.… gone. Agents lost with it:
> - docs: agent-skill-needs-answer (accepted): closed on merge, #794 head ec39b1f9 in dev;
>   readme-ops-guide-finish (requested): open, nobody holds it. Bring docs back: agentbus --project
>   agent-bus-monitor pop docs --resume <session>
> - coder: nothing owed. Self-repair relaunches it (required).

The bring-back line is the one `popStranded` already builds (`{p}:resume`, else plain `pop <role>`)
for a pop; "self-repair relaunches it" for a required role; `agentbus up` for an optional boot role.

The notice is sent **after** §5's closes in the same pass, so it states their outcome. A pop named in
a `holderlost` notice gets no second notice from `popStranded` for the same episode: `popStranded`
skips a pop whose pane's build key carries a `holderlost:<K>` mark. No Lua change: the mark is read
with the other quota marks the pass already reads.

## 5. A request whose owner is gone

### 5.1 The owner is gone

A request's **owner is gone** when the process that accepted it no longer holds it:
- its terminal is closed: `f.Closed` as `popfacts` computes it, now for any tier (no live terminal,
  and the proved process gone); or
- the agent runs as **another process** than the one that accepted it. For this, `request accept`
  records `request.accepted_by = {pid, pid_start}` from the caller's verified snapshot (a resume from
  `review` records it again). A request accepted before this change has no `accepted_by`: only a
  closed terminal judges its owner gone.

The second case is the case in point: docs came back as a new process and saw the request as
"pre-session". A `/clear` keeps the process, so it never makes an owner gone.

### 5.2 What links a request to a merge

Two links, either suffices:
1. **branch**: `request.branch`, judged as close on merge judges it today (tip or recorded commit,
   `review_base`; board-drift §3.2);
2. **subject** (new): an **approved** head in the verdict ledger whose `subject` is the task or
   `pr-<task>` and whose `author` is the request's `target`, judged delivered by the same
   `runDelivered` call, in the same pass, within the same 72 h window. Nothing new is fetched or
   polled. The author condition keeps a spec's subject from closing another agent's build request of
   the same name.

### 5.3 The close

The pass extends close on merge's candidates:
- `review` requests, as today (link 1), plus link 2 when they have no branch;
- **`accepted` and `review` requests whose owner is gone** (§5.1), by link 1 or link 2.

An `accepted` request whose owner is **running** is still never closed (board-drift §3.1 stands).

The write is `FencedRequestMerged` with two more checks and one more field, still one Lua call:
- the lease (`DispatchKey`), as today;
- the request's state is still the state read (`review` or `accepted`);
- for a gone owner: the target's registration in `{p}:agents` is still the one read (`pid`,
  `pid_start`, `ts`), or still absent. A respawn between read and write writes nothing, as in
  `forgetIfClear`;
- for link 1, the branch is still the one read; for link 2, the head is the approved head read.

It sets `state = done`, `request.closed_on_merge = {head, base, via, owner_gone}` (`via` = `branch` or
`subject`), `responded_at` from Redis `TIME`, `updated`. No reply, no wake (board-drift Q3); one
`request-merged` event, as today. busmon's task detail shows "closed on merge `<head>` into `<base>`
(owner gone)". A `review_more` request whose owner is gone is **not** sent back to `accepted`: nobody
would resume it. It closes, and §4.2's or §5.4's notice names it.

### 5.4 What is not closed is flagged

An open request of a gone owner with no link that holds (no branch, no matching subject, a head older
than 72 h, a link that cannot be judged) is never closed by guess. It is named:
- in §4.2's notice, when its owner went with a holder;
- in `popStranded`'s notice (board-drift §6), now sent for **any** tier but a required one, since a
  required role is relaunched by self-repair;
- in `board drift` when board-drift step 5 is built: its finding 1 gains "owner gone".

## 6. A gone agent is retired by rule

`popRetirePass` judges, besides pops, every **optional boot** role (tier `boot`, not `required`)
whose terminal is **closed** (`f.Closed`). A closed agent that owes nothing (`PopDone` as for a pop
closed by hand) is ended by the existing fenced `End` path, as `retire --forget` does. It is **never**
applied to:
- a required role (self-repair's);
- master (the escalation target), the pilot, or the project's person;
- an agent whose snapshot has no `tier` (a registration older than v0.20): left as it is.

A closed agent that owes something is **lost** (§3), and §4.2 or §5.4 tells master. Its row stays
until what it owes is closed, then the next pass ends it. The consumer group is kept, as today, so a
later `pop` or `up` starts where it left off.

## 7. What the person sees

- **busmon Agents.** `lost` is red, group 1, with its reason (§3). `not running` is muted, in the
  "no state" group, so it sinks below working agents. The dormant filter (`a`) is unchanged.
- **busmon Board.** A request whose owner is lost shows `owner lost` in its alert column, beside the
  board-drift alerts; a request closed with `owner_gone` shows it in its detail.
- **`agentbus agents`.** The same words, the reason after the state.
- **health.** `holder_restarted` (§4.1) names lost agents per holder build. A new check
  **`agents-lost`** (Handler `dispatch`, every minute, Warn, In hand) lists every lost agent with its
  reason, including those not lost with a holder (a client crash, a pane closed by hand while owing).
  `not running` never appears in health: it is not a fault. The check rings nobody: master is told by
  §4.2 or §5.4, and the warn is in busmon's health view.

## 8. Build steps

One PR each, from `origin/dev`, in this order.

1. **Presence** (§3, §7 busmon and `agents`): `bus.Presence` replacing `ShownState`'s callers, the
   owes reading, the two words, the `agents-lost` check. Pure functions, tested without Redis.
2. **Holder gone** (§4.1): `holderRestarted` reports a build that answers no more.
3. **Owner gone and the subject link** (§5): `request accept` records `accepted_by`; the pass's new
   candidates; `FencedRequestMerged`'s added checks and fields; busmon's detail.
4. **The notices** (§4.2, §5.4): the `holderlost` directive, `popStranded` for any non-required tier,
   and the skip.
5. **Retire by rule** (§6): `popRetirePass` judges closed optional boot roles.

**Tests:**
- §3: a table over presence (live, closed and owes, closed and owes nothing, required, holder not
  read, a read that failed: lost).
- §4.1: an agent of a build that no longer answers is reported gone; one of a build that answers
  and started before its proof is not; a `done` agent is not.
- §5, on a disposable repository and the test broker (`AGENTBUS_TEST_REDIS_URL` only): an accepted
  request of a closed owner with an approved, delivered head under its subject closes with `via =
  subject`; the same with the owner running stays open; another author's subject closes nothing; a new
  process (other `accepted_by`) closes it; a respawn between read and write writes nothing; a lost
  lease is `NOTHOLDER`.
- §4.2: one notice per holder episode, with the closes' outcome; `popStranded` silent for a pop it
  named.
- §6: a closed optional boot role that owes nothing is ended; a required one, master, and one with no
  tier are not.

## 9. Questions for the person (one ask, French)

**Q1. Une requête acceptée dont le propriétaire est parti, et dont la PR a fusionné.**
- **(A) recommandé :** la fermer automatiquement (`closed_on_merge`, `owner_gone`), et le dire à master
  dans la notice. Un build en plusieurs étapes dont la 1re étape porte le nom de la tâche serait fermé
  trop tôt ; master le renvoie.
- **(B)** Ne jamais la fermer : la signaler à master seulement, qui ferme à la main.

**Q2. Comment relier une requête à sa fusion quand elle n'a pas de branche.**
- **(A) recommandé :** par le registre des verdicts : sujet = la tâche ou `pr-<tâche>`, auteur = la
  cible de la requête, tête approuvée livrée dans `dev`.
- **(B)** Par la branche seulement : les agents doivent passer leur requête en `review --branch`
  avant de livrer.

**Q3. Quand le propriétaire est-il « parti » ?**
- **(A) recommandé :** son terminal est fermé, ou un autre processus a pris sa place (relance, reprise) ;
  `/clear` ne compte pas.
- **(B)** Seulement quand son terminal est fermé. Le cas docs de #794 (revenu comme nouveau processus)
  resterait ouvert.

**Q4. Retirer automatiquement un agent disparu qui ne doit plus rien.**
- **(A) recommandé :** les pops (déjà) et les rôles de démarrage optionnels ; jamais un rôle requis,
  master ni le pilote.
- **(B)** Les pops seulement, comme aujourd'hui.

**Q5. Un holder perdu.**
- **(A) recommandé :** une directive à master par holder perdu, qui le réveille, avec les agents perdus,
  leurs requêtes et ce qui a été fermé.
- **(B)** Le bilan de santé seulement, sans réveiller master.

**Q6. Quand un agent sans terminal est-il « perdu » (une panne) plutôt que « arrêté » ?**
- **(A) recommandé :** quand il doit encore quelque chose (requête, question, messages) ou qu'il est
  requis. Sinon « arrêté », en gris.
- **(B)** Dès que son terminal disparaît sans qu'il ait déclaré `done`.
