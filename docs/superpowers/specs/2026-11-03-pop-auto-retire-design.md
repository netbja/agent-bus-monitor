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
busmon and the log can say why a pop is kept.

Condition 10's focus rule is what keeps the end from surprising the person: a pane the person has
in focus is a pane they are reading, so it stays until they look away.

## 3. Who ends it

Three options were weighed:

| | Who acts | For | Against |
|---|---|---|---|
| **A** (proposed) | The dispatcher sees the pop done and sends master **one waking directive**; master runs `agentbus retire <role> --forget` | No new automated pane action: the dispatcher stays the writer of a wake line and a `/clear` only (decision 2026-10-10, lifecycle-4). `retire`'s own checks run again at the moment of the act. Master can decline with a reason. | Needs master awake; a held master delays the end (§7) |
| B | The dispatcher closes the pane itself | No round trip | A new class of automated act on panes, which the event-driven dispatch decision limits to typing a wake line; a decision note and its own review would be needed |
| C | The pop retires itself at the end of its task | Knows best when it is done | A process cannot prove its own end: `retire` refuses its own pane on purpose, and closing it from inside kills the command mid-way |

**A is proposed.** The directive (from `dispatch`, `FencedCmdOnce`, mark `popdone:<agent>` =
episode, the episode being the declared state's time of condition 9, so a pop that works again
and idles again is a new episode):

```
security has finished: no task, gate, ask or message left, idle 15m. End it: agentbus retire security --forget --reason "work done"
```

Master's tool rules allow exactly that command for each pop role (§6): no `--now`, no other flag,
no other role. If master declines (the person wants the pop kept), it says so with `agentbus
report`, and the mark keeps the directive from coming back until the next episode.

If master cannot act (the escalation rule `dispatch.CannotAct`: master's provider held, its tab
blocked), nothing is sent and nothing is closed; the pop stays until master can act. A pop that
idles does not cost a turn.

## 4. One act: `retire --forget`

`agentbus retire <role> --forget` keeps every check and step of `retire`, and once the pid is
proven gone and the `retired` ledger entry is written, calls `Bus.ForgetAgent(role)`:

- after the proof only, so a refused or failed retire never forgets a live agent;
- `agents forget`'s own refusals (armed, a status within 10 min) do not apply there: the process
  is proven gone, its armed key is stale, and forget deletes it;
- the ledger's `retired` entry gains `forgot: true` (`agentbus swaps` prints `retired … forgot`);
- a forget that fails after a successful retire is said, with the command to finish
  (`agentbus agents forget <role> --force`), and exits 1: the pane is closed, the registration is not.

`--forget` is refused on a role whose recorded tier is not `pop`: a boot role comes back with
`agentbus up` and keeps its consumer group's cursor. Plain `retire` is unchanged.

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
pop role without `approval = "person"`, `Bash(agentbus retire <role> --forget --reason *)` as an
allow. No rule for boot roles, none with `--now`. Defence in depth, not a boundary: the client
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

Enter opens the agent's page with an `Action:` row, `e` ends it (the same `retire --forget`, run
as the person, `--as`).

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
  not done; a turn flip never counts as condition 9's declaration.
- The pass: one directive per episode (`FencedCmdOnce`), none while master cannot act, none for
  an `approval = "person"` pop, none for a boot role; a new episode after the pop works again.
- `retire --forget`: forgets only after the pid is gone; a failed close forgets nothing; a forget
  failure after the close exits 1 with the command; refused on a boot role; the ledger's
  `forgot`.
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
