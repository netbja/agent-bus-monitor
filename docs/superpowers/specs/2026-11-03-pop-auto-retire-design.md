# A pop's end, automated: retire and unregister a pop once its work is done

Status: spec, not built. Subject `pop-auto-retire`. Request: Bernard, 2026-11-02, after master
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
| **A** (proposed) | The dispatcher sees the pop done and sends master **one waking directive**; master runs `agentbus retire <role> --forget --episode <ms>` | No new automated pane action: the dispatcher stays the writer of a wake line and a `/clear` only (decision 2026-10-10, lifecycle-4). The act re-judges the whole of §2 itself and forgets atomically (§4). Master can decline with a reason. | Needs master awake; a held master delays the end (§7) |
| B | The dispatcher closes the pane itself | No round trip | A new class of automated act on panes, which the event-driven dispatch decision limits to typing a wake line; a decision note and its own review would be needed |
| C | The pop retires itself at the end of its task | Knows best when it is done | A process cannot prove its own end: `retire` refuses its own pane on purpose, and closing it from inside kills the command mid-way |

**A is proposed.** The directive (from `dispatch`, `FencedCmdOnce`, mark `popdone:<agent>` =
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
3. **After the pid is proven gone**: the forget is **one Lua script**, `Bus.ForgetIfClear(role)`,
   never a read followed by `ForgetAgent`, because a message can arrive between any read and the
   group's destruction. In one script it reads, for the role: the consumer group's pending count
   (`XPENDING`), the addressed waking entries after its `last-delivered-id` (`XRANGE` from there,
   `target == role` and `wake != no`, the rule `Obligation` uses), its open ask (`open:<role>` in
   `{p}:asks`), a board task it owns not `done` (`{p}:board`), and its gate key. If all are empty
   it deletes what `ForgetAgent` deletes and destroys the group, in the same script; if any is
   not, it changes nothing and returns what it found.

When the script finds something, the pane is closed but the registration and the group are
**kept**: `retire` prints the entry ids (or the ask, task, gate) and exits 1, the ledger's
`retired` entry says `forgot: false` with that reason, and the swap intent ends. The pop is then
a registered agent with a dead pid and a waiting message: the dispatcher escalates it to master as
it does for any agent whose pane is gone (`Decide` rule 7, `pane gone`, after `escalate_after`), and master spawns the role again, which
reads the message from the kept group. No entry is acknowledged or destroyed unread.

The window between the last check (2) and the close is not closed by a lock (a pane cannot be
closed inside a Redis script): a message that lands there closes a pop that now has work. That
costs a respawn, never the message, because (3) keeps the group.

Also:

- the forget happens after the proof only, so a refused or failed retire never forgets a live agent;
- `agents forget`'s own refusals (armed, a status within 10 min) do not apply there: the process
  is proven gone, its armed key is stale, and the script deletes it;
- the ledger's `retired` entry gains `forgot: true|false` (`agentbus swaps` prints
  `retired … forgot` or `retired … kept: <reason>`);
- a script error after a successful close is said, with the command to finish
  (`agentbus agents forget <role> --force`, after reading `agentbus subscribe --drain`'s view of
  what waits), and exits 1.

`--forget` is refused on a role whose recorded tier is not `pop` (or none, condition 1): a boot
role comes back with `agentbus up` and keeps its consumer group's cursor. `--forget` requires
`--episode`, and refuses `--now`. Plain `retire` is unchanged.

After it, `cmd-delivery` no longer sees the group (it is destroyed), busmon no longer lists the
agent, and a later `agent-spawn` of the same role registers it afresh.

## 5. What is never ended

- **Open work**, any of conditions 2 to 8.
- **The person's focus**: a focused pane (condition 10), whatever the rest says.
- **A pop that requires the person** (`approval = "person"`: deploy, infra): see §6 and open
  question 2. Proposed: never automatically; busmon offers the end to the person instead.
- **A boot role** (master, coder, foureyes, sentinel), whatever its state.
- **An agent with no verified identity**: no tier is recorded, and `retire` needs the pane.
- **During a pause or its provider's hold** (condition 11): an idle pop may be waiting for the
  hold to end with work it has not claimed yet.

## 6. Tier, approval and the person's yes

**Tier.** The launcher already exports the role's client and provider and records them with the
verified identity; it also exports `AGENT_BUS_TIER` (`boot` or `pop`, from `roles.toml`), kept as
`AgentSnapshot.Tier` with the proof, like the model. The dispatcher never reads `roles.toml`.

**The person's yes.** A pop with `approval = "person"` is popped only after the person says yes
(an ask rule on master's `agent-spawn`). Ending such a pop narrows what runs, so in principle the
yes to start covers the end. But those roles act on targets (deploy, infra), and the person may
want to look at the result in the pane before it closes. Proposed (open question 2): for these
roles the dispatcher sends no directive; busmon shows a For-you row instead, and the person ends
it from there.

**Tool rules** (`rolecfg/toolrules.go`, generated like the `agent-spawn` rules): master gets, per
pop role without `approval = "person"`, `Bash(agentbus retire <role> --forget --episode * --reason *)`
as an allow. No rule for boot roles, none with `--now`. Defence in depth, not a boundary: the client
matches command text.

## 7. What busmon and health show

**Agents row.** A pop the dispatcher judges done carries the flag `done Nm` (muted) while its
directive waits for master; a pop kept for a reason that is not open work (focus, pause, hold)
shows nothing new.

80 cells:

```
│  ○ security  idle     18m  —   done 18m    review sent: PR 259 re-check      │
```

**The notice.** The swap ledger's `retired` entry (already read every fifth tick) gives the quiet
footer line: `security retired (work done)`.

**For you** (approval = person pops only, §6):

```
│  ◆ end    deploy    finished: no task, gate or message left, idle 15m     15m│
```

Enter opens the agent's page with an `Action:` row, `e` ends it (the same `retire --forget
--episode <ms>` with the episode the row was judged on, run as the person, `--as`; refused the same
way when the pop moved on).

**Health.** `cmd-delivery` stays ok after the end (the group is gone). A new warn, `pop-done`
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
- The pass: one directive per episode (`FencedCmdOnce`), none while master cannot act, none for
  an `approval = "person"` pop, none for a boot role; a new episode after the pop works again.
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
  owned task not `done` and an open gate each hold it; all empty destroys the group and deletes
  the keys `ForgetAgent` deletes.
- Tool rules: master's allow per pop role without person approval, none for boot roles,
  none with `--now` (`TestShippedManifestRules`).
- busmon: the `done` flag, the For-you row and its `e`, the notice line.
- Health: `cmd-delivery` ok after the end; `pop-done` warns after twice the delay.

## 10. Open questions for Bernard

1. **Who ends it?** A (proposed): the dispatcher tells master, master runs `retire --forget`. Or B:
   the dispatcher closes the pane itself, with no round trip through master, which needs its own
   decision note since today it only types a wake line and `/clear`.
2. **Pops that need your yes (deploy, infra)**: ended only by you from busmon (proposed), or
   automatically like the others, since your yes to start covers the end?
3. **The idle delay**: 15 minutes after its last declared idle (proposed), or another value?
4. **On by default?** `on` (proposed, since you asked for it), or `off` until you turn it on?
