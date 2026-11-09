# Self-repair watcher: dead agents relaunched without an AI (design, for the person's review)

Status: **draft, not built.** The person asked on 2026-11-09, through master (task
`reload-failure-cause`), for a watcher that repairs the system, its tabs and its network losses
and relaunches agents, with no AI in the loop. This spec proposes one. Part 8 lists the decisions
that are the person's; nothing here is to be built before those answers.

## 1. Why

Today a dead agent comes back only through a person or an agent:

- **The person** runs `agentbus up <p> --resume` after a reboot or a crash.
- **Sentinel** (an AI, woken by the dispatcher) may restart a *service*, but never relaunches an
  agent. It reloads one only after a client update, and only through `agentbus reload`.
- **Master** (an AI) runs `agentbus reload` by hand when sentinel's fails, as on 2026-11-09 20:24
  (foureyes: the old client was gone and nothing was relaunched; master recovered it).

Each path needs an AI's turn or the person at the keyboard. If master itself dies, or its provider
is held, a dead agent stays dead: the dispatcher escalates `identity mismatch` and waits. A
deterministic watcher closes that gap for the cases where the right action is certain. Every
other case it reports.

## 2. What it is, and what it is not

- **One loop of the dispatch service** (the project's runner, later the host dispatcher's), not a
  new daemon. The services decision ([2026-10-14](../../memory/decisions/2026-10-14-project-services.md))
  rules out a supervisor of our own. systemd or launchd already keeps the dispatcher alive and
  restarts it on a crash (its restart budget), so the watcher inherits that supervision.
- **Fenced like every dispatcher write.** It acts only while its runner holds the `{p}:dispatch`
  lease. Every record it writes goes through a Lua script that checks the token, with Redis `TIME`.
  herdr actions (split a pane, run the command) cannot be fenced: what keeps two relaunchers apart
  is the role's intent (4.1, "One relauncher at a time"), not the lease.
- **Rules, not judgement.** Each repair has a fixed trigger, preconditions, an action, a proof and
  a budget. A case that matches no rule is reported, never guessed at.
- **Not a replacement for sentinel.** Sentinel keeps the daily review, the client updates, the
  model catalogue and anything that needs reading a log. The watcher takes only the repairs whose
  facts are measurable.
- **Never:** types into an existing pane, kills a live process, restarts herdr, the broker or
  Docker, deletes anything, edits configuration, runs repository code, or relaunches an agent that
  declared it is going away.

## 3. What it watches

| Area | Fact measured | Source (all exist today) |
|---|---|---|
| **Agent process** | the verified pid and start time are gone | `{p}:agents` + `/proc` (`dispatch.ProcStart`, `pidListedNoMore`) |
| **Tab** | the template tab is missing, or holds only plain shells | herdr `pane list`, `pane process-info` (`judgeTab`) |
| **herdr** | the herdr server answers, the project's workspace is open | `herdr-probe` health check, `workspace list` |
| **Broker** | Redis answers within its deadline | the runner's own connection |
| **Network** | the provider's quota reader is fresh (it reaches the provider every minute); a local model's endpoint answers | `quota-freshness`, `llm-endpoint` (local LLM only) checks |
| **Services** | dispatch / missions units are active | `services` check (already sentinel's repair) |
| **System** | a reboot or a resume from suspend (monotonic clock jump, boot id change) | `/proc/sys/kernel/random/boot_id`, wall vs monotonic clock |

## 4. The rules

### 4.1 R1: an agent's process is gone (the main repair)

**Trigger.** For one agent of the template, `snapLife` reads **dead** for its whole grace period
(default 60 s, measured by Redis `TIME`), on at least three reads.

**Preconditions.** All must hold, each read again right before acting:

1. **No declared departure, and no other relauncher.** There is no swap intent for the role
   (`{p}:swap:<role>`: a swap, a reload, a retire, or another self-repair), no suspend or pause, no
   hand-off clear in flight, and it is not a pop that was retired or is done (pops come back only
   through master). The watcher's **own** intent, recognised by its nonce, does not count: it is
   what holds the role during the repair (below).
2. **Its tab reads what a relaunch needs.** `judgeTab` returns `relaunch` or `create`, with the
   same rule `up --resume` and `reload` use (no agent detected, every pane a plain shell). A tab
   that reads `busy`, `alive` or `unknown` is waited on, bounded; it is never forced.
3. **herdr and the workspace are up.** The watcher never opens herdr or a workspace: that needs
   the person's terminal.
4. **The network can carry it.** Its provider's quota reader succeeded in the last 5 min (a
   local model: `llm-endpoint` is ok), and the
   provider is not held by the quota coordinator. A relaunch into a dead network or a held quota
   just dies again.
5. **A session to resume, or permission to start fresh.** Claude Code resumes its proved session
   (`--resume <id>`). A client with no resumable session (Codex today) is relaunched fresh **only
   if the person allowed it** (part 8, Q4): its conversation is lost.
6. **Budget left** (4.5).

**One relauncher at a time.** Before anything else, R1 takes the role's intent: `BeginSwap` with a
new kind `self-repair` and a nonce, in a script that **also checks the dispatch lease token** (a
fenced variant of today's `SET NX PX`). It holds the intent through the proof and ends it after
(`EndSwap`, also fenced, and only if the nonce is still its own). Right before the split it reads
the intent again: still its own nonce and still the lease holder, or it stops with nothing done.
The intent's TTL (`SwapTTL`) outlives a lost runner, so a dispatcher that takes the lease over
mid-repair sees the intent and waits for its expiry; it never relaunches next to it.

Today `up --resume` and `relaunchRole` read **no** intent (`start.go`, `relaunch.go`), so a person
or master running `up --resume` could split a second pane and resume the same session twice. This
spec therefore also changes them: `up --resume` skips a role whose intent is held (`skipped
(self-repair in progress)`), and `reload` and `swap` are already refused by `BeginSwap`'s `NX`
(`ErrSwapInProgress`). The dispatcher's existing rules for an intent (no prompt, no clear) apply to
a self-repair unchanged.

**Action.** The same code `agentbus reload` uses after its kill: `relaunchRole` (the template
command, in a **new pane** split from the tab's pane; the old pane is closed once the new agent is
proven). That code moves from `cmd/agentbus` into a package the dispatcher can import. Nothing is
typed into an existing pane.

**Proof.** A new pid, alive, in the new pane, on the same client, provider and profile, within
60 s (`replaced`, reload's rule). Then a ledger entry `self-repair relaunched <agent>: pid A -> B,
pane P -> Q`, one line to master, and the ordinary wake for any message waiting.

**Failure.** No proof in 60 s: one escalation to master and the person (desktop notification),
with the recovery line reload prints today (`agent-launch <role> <p> --resume <id> in pane …`).
No retry for that episode.

**Master.** Proposed: master is relaunched like any other agent, first when several are dead, as it
is the one that could otherwise recover the rest. The relaunched master does not inherit the pilot
lease the dead one held; it takes it the usual way once the old lease expires. This is a new
authority: part 8, Q6.

### 4.2 R2: a template tab is missing

The tab of a template role was closed (a person's click, a herdr crash restoring the workspace
without it). With R1's preconditions, `judgeTab` reads `create`, and the watcher creates the tab
and relaunches into it, exactly as `up --resume` does. A tab the person closed **on purpose** is
indistinguishable from an accident; that is part 8, Q3.

### 4.3 R3: network lost, then back

The watcher never "repairs" a network. While the endpoint fails it **holds R1** (precondition 4)
and says so once (`self-repair: holding relaunches, <provider> unreachable since HH:MM`). When the
network returns, agents whose processes survived need nothing: their next wake works. Agents that
died meanwhile go through R1 then, oldest first, one at a time.

### 4.4 R4: after a reboot or a resume from suspend

On a new boot id the dispatcher starts under systemd at login, but herdr and its workspace need
the person's terminal. The watcher:

- **workspace not open:** relaunches nothing, and posts one notice: `agent-bus-monitor: the
  workspace is not open; run agentbus up agent-bus-monitor --resume` (desktop, plus the person's
  inbox in busmon);
- **workspace open** (herdr restored it): R1 and R2 apply as usual. Panes herdr restored with a
  live client are recovered by the dispatcher's existing rule (codex-identity-recovery), not
  relaunched.

On a resume from suspend (a monotonic jump, the same boot id), R1's grace restarts from the resume,
so a long sleep never counts as an agent "dead for an hour".

### 4.5 Budgets and the kill switch

- **Per agent:** one relaunch per 30 min, three per 24 h. A fourth death in a day is a crash loop:
  escalate it, relaunch nothing more for it until the person or master clears it
  (`agentbus self-repair reset <agent>`). Giving master that power is part 8, Q5.
- **Per project:** at most one relaunch in flight, then a 2-min pause before the next agent.
- **Switch:** `dispatch.self_repair = off | shadow | on` in `config.toml`, overridable with
  `AGENT_BUS_SELF_REPAIR`; both are registered in `config/keys.go`, the one settings registry, like
  every other setting (so is the budget, `dispatch.self_repair_budget`). **`shadow`** records what it would do (health check `self-repair`, a
  `would relaunch …` ledger line) and acts on nothing. Part 8, Q1 asks for the default.

## 5. What the person and the team see

- **health `self-repair`:** `ok`; `warn` while R1 is held (network, quota, a tab not settling) or
  in shadow mode with a would-be action; `fail` on a failed proof or a spent budget, which wakes
  sentinel as any fail does. Sentinel only annotates it and tells master; it never retries.
- **Ledger:** each action in the swap ledger (`agentbus swaps`) as a `self-repair` kind, with its
  reason. Master's next turn reads it like a reload.
- **busmon:** the agent's row shows `relaunched by self-repair HH:MM` for an hour.
- **Report:** one line to master per relaunch: `self-repair: foureyes relaunched (pid 241 -> 902,
  resumed 58b5f18b), it was dead 70 s`.

## 6. Correctness rules it must follow (learned the hard way)

- **One life rule.** "Dead" is `pidListedNoMore`, the rule reload, restore and pop-retire use.
  The 2026-11-09 failure came from a second, drifted rule (`snapLife` matched `os.ErrNotExist`,
  which `ProcStart` stopped returning on 2026-11-03; fixed in `fix/relaunch-dead-pid`). Tests fake
  the error `ProcStart` really returns, and one test runs the real `ProcStart`.
- **Dead and gone are different from unknown.** No `/proc` (macOS), a failed read, a pid with no
  start time: unknown, and unknown never relaunches. On macOS the watcher therefore relaunches
  nothing until a process identity exists there.
- **The tab, not just the pid.** A dead pid with a tab still showing the client is waited on
  (bounded, as `relaunchRole`'s settle loop does), never relaunched next to it.
- **Every decision is a pure function** (`decideRepair(facts) -> action`), tested by table, the
  way `Decide` and `decideRelaunch` are. The loop only gathers facts and applies the action.

## 7. Delivery, in steps (after the person's answers)

1. **Move the relaunch code** (`relaunchRole`, `judgeTab`, `decideRelaunch`, `replaced`) from
   `cmd/agentbus` into a package. No behaviour change; `reload` and `up` use it from there.
2. **The intent:** the fenced `self-repair` kind of `BeginSwap`/`EndSwap` (lease token and nonce),
   and `up --resume` skipping a role whose intent is held. Useful alone: today two people (or a
   person and master) running `up --resume` at once can already race.
3. **Shadow mode:** facts, `decideRepair`, health `self-repair`, ledger `would relaunch`. Run it a
   week on this project and compare with what master did by hand.
4. **R1 and R2 on,** with budgets and the proof. Live check: kill an idle agent's client
   (SIGTERM) and watch it come back in a new pane.
5. **R3 and R4** (hold on network loss, the reboot notice).

## 8. Questions for the person

1. **Default mode.** `shadow` first, then `on` after a week of matching master's own actions
   (recommended), or `on` at once, or `off` until asked?
2. **A new pane writer.** Today the dispatcher types only the wake line and `/clear`. R1 runs the
   template command in a **new pane it creates** (as `up` and `reload` do when a person or sentinel
   runs them). It never types into an existing pane. This touches three decisions, each to be
   amended in writing, not worked around:
   - the [event-driven dispatch decision](../../memory/decisions/2026-10-10-event-driven-dispatch.md),
     whose revisit clause says that a second automated pane writer *breaks* its rule rather than
     extending it: the watcher is not a second writer (it is the dispatcher), but the dispatcher
     would write a third thing;
   - the [agent lifecycle decision](../../memory/decisions/2026-10-28-agent-lifecycle.md), which
     makes reload sentinel's job: a self-repair is a relaunch without a kill, by the dispatcher;
   - the project rule in `CLAUDE.md` ("types only the fixed wake line and `/clear`").

   Do you accept this as a third exception, recorded as a decision? If not, the watcher can only
   *propose* (shadow mode forever), and the person or master runs the relaunch.
3. **A tab closed on purpose.** Should a missing tab be recreated (R2), or only reported? Closing a
   tab is also how a person stops an agent, so recommended: report only, unless the agent's
   snapshot still says it is expected (not retired, not suspended).
4. **Clients that cannot resume** (Codex, Grok today): relaunch fresh (the conversation is lost,
   its board task and messages remain), or report only (recommended)?
5. **Budgets:** are one per 30 min and three per day per agent right? And may **master** clear a
   spent budget (`agentbus self-repair reset <agent>`), or only the person (`--as <person>`, like a
   reload under `propose`)?
6. **Master relaunched by the watcher.** Should a dead master be relaunched first (recommended: it
   recovers everything else), or always left to the person, since master holds the pilot lease and
   drives the other panes?

## 9. Not in this spec

- Restarting herdr, the broker, Docker or the network: the person's machine, the person's acts.
- Stalled turns (a live process that does nothing): the existing `quiet-agent`, `runaway` and
  `team-stall` checks.
- Distributed hosts: the watcher acts only on the host whose `/proc` holds the agent.
