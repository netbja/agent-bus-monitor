# A pop's end, automated: retire and unregister a pop once its work is done

Status: spec, answered (§10), being built (subject `pop-auto-retire-code`). Subject `pop-auto-retire`. Request: Bernard, 2026-11-02, after master
retired security by hand once its review was done: "cela peut être automatisé" (this can be
automated).

## 1. Why

A pop role (`tier = "pop"` in `roles.toml`: architect, deploy, security, docs, ui-grok, ui-kimi,
reviewer-codex, infra) is started for one piece of work and is meant to go once it is done. Today
its end is two manual steps:

1. `agentbus retire <role>` (`cmd/agentbus/retire.go`): checks open work keyed by its name
   (`openWork`: gates on either side, mission attempts), refuses a running turn without `--now`,
   writes the swap ledger's `begin`, closes the pane, waits for the pid to go, writes `retired`.
2. `agentbus agents forget <role>` (`Bus.ForgetAgent`): removes its snapshot, usage, wake record,
   armed, gate and receiver keys, and destroys its cmd consumer group.

`retire` does not do the second step. On 2026-11-02 the retired security stayed registered; its
consumer group kept a lag nobody would read, and health `cmd-delivery` (which judges the groups
of registered agents only, `deliveryEvidence`) failed until `agents forget` ran. Claude Code's
classifier refused that command to master; Bernard typed it himself.

So the work is two parts: a pop's end retires **and** unregisters it in one act (§4), and that
act is started without anyone remembering to (§3, §5).

## 2. When a pop is done

A pop is **done** when every one of these holds, read by the dispatch service on its pass:

| # | Condition | Read from |
|---|---|---|
| 1 | It is a pop: its verified identity records `tier = pop` | `AgentSnapshot.Tier`, new, set by the launcher with the identity (§6) |
| 2 | It owns no board task in a state other than `done` | `{p}:board` |
| 3 | No gate holds it, and no gate it opened is open | `Bus.ReviewsOf` (as `openWork`) |
| 4 | No mission attempt is in progress on it | `missionsOf` (as `openWork`) |
| 5 | It has no open ask | `{p}:asks` `open:<agent>` |
| 6 | Nothing waits for it on the bus: no pending and no unread entry | `Obligation` (pending + lag) |
| 7 | It is not armed, and no wake is being typed into its pane | `{p}:armed:<agent>`, `{p}:wake:<agent>` |
| 8 | No swap or reload intent holds it, and no valid hand-off waits for a `/clear` | `{p}:swap:<role>`, `{p}:handoff` |
| 9 | It declared `idle` or `done` at least `dispatch.pop_retire_after` ago (default 15 min), and declared nothing since | `{p}:agents` (the declared state and its time, never a turn flip: `DeclTS`) |
| 10 | A fresh observation of its pane (`Observation.Fresh`) sees it ready, not busy, not on a prompt, **not focused** | `{p}:observed` |
| 11 | The project is not paused, and the pop's provider is not held | `{p}:pause`, `{p}:quota` hold mark |

Any condition unknown (a failed read, no fresh observation) is **not done**: the pass decides
nothing for that pop. The judgement is a pure function (`dispatch/popdone.go`, `PopDone(facts)
(done bool, reason string)`), like `Decide`, with the first failing condition as its reason, so
busmon and the log can say why a pop is kept. The facts are gathered by one reader,
`dispatch.ReadPopFacts(ctx, b, role)`, used by the dispatch pass **and** by `retire --forget` (§4),
so the act re-judges exactly what the pass judged, never a subset.

Condition 1 is the fail-safe reading for pops launched before this ships: they carry no `Tier`,
so they are never judged and `retire --forget` refuses them; they end by hand, as today.

The **episode** of a done judgement is condition 9's declaration time (`DeclTS`, Unix ms). A pop
that declares anything after it, or works, is in a new episode.

Condition 10's focus rule is what keeps the end from surprising the person: a pane the person has
in focus is a pane they are reading, so it stays until they look away.

## 3. Who ends it

Three options were weighed:

| | Who acts | For | Against |
|---|---|---|---|
| **A** (chosen) | The dispatcher sees the pop done and sends master **one waking directive**; master runs `agentbus retire <role> --forget --episode <ms>` | No new automated pane action: the dispatcher stays the writer of a wake line and a `/clear` only (decision 2026-10-10, lifecycle-4). The act re-judges the whole of §2 itself and forgets atomically (§4). Master can decline with a reason. | Needs master awake; a held master delays the end (§7) |
| B | The dispatcher closes the pane itself | No round trip | A new class of automated act on panes, which the event-driven dispatch decision limits to typing a wake line; a decision note and its own review would be needed |
| C | The pop retires itself at the end of its task | Knows best when it is done | A process cannot prove its own end: `retire` refuses its own pane on purpose, and closing it from inside kills the command mid-way |

**A is chosen** (Bernard, §10). The directive (from `dispatch`, `FencedCmdOnce`, mark `popdone:<agent>` =
episode, so a pop that works again and idles again is a new episode):

```
security has finished: no task, gate, ask or message left, idle 15m. End it: agentbus retire security --forget --episode 1793647000000 --reason "work done"
```

The judgement is a turn old, or more, when master runs it: someone may have sent the pop a
message, assigned it a task, focused its pane, or the pop may have opened an ask. So the directive
is not a licence; `retire --forget` judges again, whole, at the act (§4), and the `--episode`
names what master was told, so a pop that idled again since is refused too.

Master's tool rules allow exactly that command for each pop role (§6): no `--now`, no other flag,
no other role. If master declines (the person wants the pop kept), it says so with `agentbus
report`, and the mark keeps the directive from coming back until the next episode.

If master cannot act (the escalation rule `dispatch.CannotAct`: master's provider held, its tab
blocked), nothing is sent and nothing is closed; the pop stays until master can act. A pop that
idles does not cost a turn.

## 4. One act: `retire --forget --episode <ms>`

`agentbus retire <role> --forget --episode <ms>` keeps every check and step of `retire`, and adds
three re-judgements (foureyes and reviewer-codex, on 60dd228: `retire`'s own checks, `openWork`
plus the running turn, are not conditions 2, 5, 6, 7, 10 and 11, and the dispatcher's judgement
can be stale by the time master acts):

1. **Before anything** (before the swap ledger's `begin`): `ReadPopFacts` then `PopDone`, the
   whole predicate of §2, and the snapshot's `DeclTS` equal to `--episode` with the state still
   `idle` or `done`. Not done, or another episode: refused with `PopDone`'s reason, exit 1,
   nothing written but a `retire-refused` line in the log (stderr), nothing closed.
2. **After the `begin`** (where `retire` already repeats `openWork`), and once more **right before
   `ClosePane`**: the same predicate and episode again, the last one reading a fresh observation
   so that a pane the person has just focused is not closed. A change is a `failed` ledger entry
   (`step: popdone`, the reason), the intent ended, nothing closed.

   From the `begin` on, `{p}:swap:<role>` holds retire's **own** intent, which condition 8 would
   read as "a swap holds it" (reviewer-codex, on 9a95873). So `retire --forget` writes an intent
   it can recognise exactly: `SwapIntent` gains `Kind = "retire"` (`SwapKindRetire`, beside
   `SwapKindReload`; the dispatcher's swap rules apply unchanged, as for a reload) and `Nonce`, 16
   random bytes in hex drawn by this run. The run keeps the intent's exact JSON as it wrote it, and
   `ReadPopFacts` takes it as `OwnIntent`: condition 8 passes on a stored intent **byte-equal** to
   `OwnIntent`, and fails on any other (a swap, a reload, another retire, or this role's intent
   rewritten), on no intent where one is expected (it lapsed: `PX 180000`), and on a valid
   hand-off whatever the intent. Check 1, before the `begin`, passes no `OwnIntent`, so any
   intent there refuses. The ledger's `begin` and `retired` entries are unchanged (`kind` stays
   empty for a retire, as today).
3. **After the pid is proven gone**: the forget is **one Lua script**, `Bus.ForgetIfClear(role)`,
   never a read followed by a delete, because work can arrive between any read and the delete. In
   one script it checks that `{p}:swap:<role>` still holds retire's own intent, byte-equal to
   `OwnIntent` (else it changes nothing), then reads, for the role: the consumer group's pending count (`XPENDING`),
   the addressed waking entries after its `last-delivered-id` (`XRANGE` from there,
   `target == role` and `wake != no`, the rule `Obligation` uses), its open ask (`open:<role>` in
   `{p}:asks`), a board task it owns not `done` (`{p}:board`), and its gate key. If all are empty
   it deletes the **registration** (snapshot, usage, wake, armed, receiver and gate keys) in the
   same script; if any is not, it changes nothing and returns what it found.

   It **keeps the consumer group and its cursor** (foureyes, on 9b0b46c). Destroying it would make
   the next spawn of the role replay its handled messages: `EnsureGroup` recreates a missing group
   at `now - 24h` and `subscribe --drain` has no floor, so a pop forgotten and spawned again the
   same day (reviewer-codex, security) would drain every directive, reply and challenge of the
   last day, the ones its previous instance already handled included. Kept, the group is what
   `cmd-delivery` no longer judges (it reads registered agents only, A9), so the health failure
   that started this is gone all the same; a later message to the role still makes an obligation
   the dispatcher sees and escalates (`Decide` rule 6, `no snapshot`); and a later spawn reads on
   from the cursor. `agents forget` (busmon's `d`) keeps destroying the group, as today.

   **Mission attempts** (condition 4, reviewer-codex on 9b0b46c) are not read by the script:
   they cannot start in the window. Every attempt starts with the conductor's `FencedRequest`,
   whose script refuses with `SWAPPING` while `{p}:swap:<target>` exists, and the conductor's
   challenges (`fencedChallenge`) the same; retire holds that intent from its `begin` (after
   which check 2 has read the missions, condition 4) until after the script, which checks it
   still holds. An attempt already running is caught by checks 1 and 2, and it cannot move on
   without the agent (a delivery is the agent's own act). The request also writes an owned board
   task in the same script, which the script reads, so even a request sent another way holds it.

When the script finds something, the pane is closed but the registration is **kept** (the group
always is): `retire` prints the entry ids (or the ask, task, gate) and exits 1, the ledger's
`retired` entry says `forgot: false` with that reason, and the swap intent ends. The pop is then
a registered agent with a dead pid and a waiting message: the dispatcher escalates it to master as
it does for any agent whose pane is gone (`Decide` rule 7, `pane gone`, after `escalate_after`),
and master spawns the role again, which reads the message from the kept group. No entry is
acknowledged or destroyed unread.

The window between the last check (2) and the close is not closed by a lock (a pane cannot be
closed inside a Redis script): a message that lands there closes a pop that now has work. That
costs a respawn, never the message, because (3) keeps the group.

Also:

- a slow close (the pid wait plus the script past the intent's `SwapTTL`, 3 min) lets the intent
  lapse: the script then refuses and the registration is kept, the safe outcome, said like any
  other refusal (foureyes on 781a10b);
- the forget happens after the proof only, so a refused or failed retire never forgets a live agent;
- `agents forget`'s own refusals (armed, a status within 10 min) do not apply there: the process
  is proven gone, its armed key is stale, and the script deletes it;
- the ledger's `retired` entry gains `forgot: true|false` (`agentbus swaps` prints
  `retired … forgot` or `retired … kept: <reason>`);
- a script error after a successful close is said and exits 1; the registration stays, so the
  pop reads as a dead agent and is escalated like one; the person may then finish with
  `agentbus agents forget <role> --force` (which also destroys the group).

`--forget` is refused on a role whose recorded tier is not `pop` (or none, condition 1): a boot
role comes back with `agentbus up` and keeps its consumer group's cursor. `--forget` requires
`--episode`, and refuses `--now`. Plain `retire` is unchanged.

After it, `cmd-delivery` no longer judges the group (the role is not registered), busmon no
longer lists the agent, and a later `agent-spawn` of the same role registers it afresh and reads on
from the kept cursor, never replaying what the previous instance handled.

## 5. What is never ended

- **Open work**, any of conditions 2 to 8.
- **The person's focus**: a focused pane (condition 10), whatever the rest says.
- **A boot role** (master, coder, foureyes, sentinel), whatever its state.
- **An agent with no verified identity**: no tier is recorded, and `retire` needs the pane.
- **During a pause or its provider's hold** (condition 11): an idle pop may be waiting for the
  hold to end with work it has not claimed yet.

## 6. Tier, approval and the person's yes

**Tier.** The launcher already exports the role's client and provider and records them with the
verified identity; it also exports `AGENT_BUS_TIER` (`boot` or `pop`, from `roles.toml`), kept as
`AgentSnapshot.Tier` with the proof, like the model. The dispatcher never reads `roles.toml`.

**The person's yes.** A pop with `approval = "person"` is popped only after the person says yes
(an ask rule on master's `agent-spawn`). That yes covers its end too (Bernard, §10 question 2):
deploy and infra are judged and ended like every other pop. The person who wants to read the
result in the pane keeps it by focusing it (condition 10).

**Tool rules** (`rolecfg/toolrules.go`, generated like the `agent-spawn` rules): master gets, per
pop role, `Bash(agentbus retire <role> --forget --episode * --reason *)` as an allow, whatever its
`approval`. No rule for boot roles, none with `--now`. Defence in depth, not a boundary: the client
matches command text.

## 7. What busmon and health show

**Agents row.** A pop the dispatcher judges done carries the flag `done Nm` (muted) while its
directive waits for master; a pop kept for a reason that is not open work (focus, pause, hold)
shows nothing new.

80 cells:

```
│  ○ security  idle     18m  —   done 18m    review sent: PR 259 re-check      │
```

While `retire --forget` holds its intent (kind `retire`), the pop is flagged `retiring`, not
`swapping` (busmon reads the intent's kind as it does for `reloading`; foureyes on 781a10b).

**The notice.** The swap ledger's `retired` entry (already read every fifth tick) gives the quiet
footer line: `security retired (work done)`.

No For-you row: every pop is ended through master (§6).

**Health.** `cmd-delivery` stays ok after the end (the role is no longer registered). A new warn, `pop-done`
(in hand, master), when a pop has been done for twice `pop_retire_after` and is still there: the
directive was not acted on. It never fails, so it never wakes sentinel.

## 8. Settings

| Key | Default | Meaning |
|---|---|---|
| `dispatch.pop_retire` | `on` | the dispatcher judges pops and sends master the end; `off` = today |
| `dispatch.pop_retire_after` | `15m` | condition 9's idle time; `0` = off |

Both in the registry (`config/keys.go`, group Services), env `AGENT_BUS_POP_RETIRE` and
`AGENT_BUS_POP_RETIRE_AFTER`, read by the dispatch service through `config.Machine`.

## 9. Tests (the plan)

- `PopDone`: a table, one row per condition failing alone, the reason named; unknown facts are
  not done; a turn flip never counts as condition 9's declaration; no `Tier` is not done.
- The pass: one directive per episode (`FencedCmdOnce`), none while master cannot act, one for
  an `approval = "person"` pop like any other, none for a boot role; a new episode after the pop works again.
- `retire --forget`: forgets only after the pid is gone; a failed close forgets nothing; a forget
  failure after the close exits 1 with the command; refused on a boot role, on no tier, without
  `--episode`, with `--now`; the ledger's `forgot`.
- Staleness (the reviewers' cases), each against the predicate, not only `openWork`: a directive
  sent to the pop after the judgement, a board task assigned, an ask opened, the pane focused, a
  pause, a provider hold, a new idle declaration (another episode): refused before the `begin`,
  nothing closed. The same changes injected after the `begin` and right before `ClosePane`
  (through `swapDeps` hooks): a `failed` entry, nothing closed.
- `ForgetIfClear` (Redis, opt-in): a waking directive sent between the kill and the forget means
  no forget, the group and the entry kept, readable by a new consumer of that group; a `wake=no`
  notice does not hold it; a pending (delivered, unacknowledged) entry holds it; an open ask, an
  owned task not `done` and an open gate each hold it; no swap intent (ended or another's) holds
  it; all empty deletes the registration keys and keeps the group with its last-delivered id.
- Own intent (reviewer-codex): after the `begin`, with retire's own intent stored, checks 2 and
  the script pass condition 8 and the retire completes; the same intent with another `Nonce`, a
  swap's or a reload's intent, a lapsed intent, or a valid hand-off fails condition 8 (`PopDone`
  table rows, plus one end-to-end case through `swapDeps` that rewrites the intent between the
  `begin` and `ClosePane`: a `failed` entry, nothing closed); check 1 with any intent present
  refuses.
- No replay (foureyes): directives handled by the pop, then `retire --forget`, then a spawn of the
  role (`EnsureGroup` through its first status): `subscribe --drain` prints nothing from before the
  retire; a directive sent after the retire is printed.
- No mission start in the window (reviewer-codex): with retire's intent held, `FencedRequest` and
  the conductor's challenge to the role are refused `SWAPPING`, and the script still finds the
  intent; a conductor pass between the kill and the script starts no attempt.
- Tool rules: master's allow per pop role (person approval included), none for boot roles,
  none with `--now` (`TestShippedManifestRules`).
- busmon: the `done` flag, the `retiring` flag, the notice line.
- Health: `cmd-delivery` ok after the end; `pop-done` warns after twice the delay.

## 10. Bernard's answers (2026-11-02, through master)

1. **Who ends it?** A: the dispatcher tells master, master runs `retire --forget`; the dispatcher
   types and closes nothing new.
2. **Pops that need the person's yes (deploy, infra)**: ended automatically like the others; the
   yes to pop covers the end.
3. **The idle delay**: 15 minutes.
4. **On by default**: yes.
