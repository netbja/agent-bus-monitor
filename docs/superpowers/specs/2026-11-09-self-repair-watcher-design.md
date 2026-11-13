# Self-repair watcher: dead agents relaunched without an AI (design, for the person's review)

Status: **answered, not built.** The person asked on 2026-11-09, through master (task
`reload-failure-cause`), for a watcher that repairs the system, its tabs and its network losses
and relaunches agents, with no AI in the loop. They answered part 8 the same day (task
`self-repair-answers`); the text below follows the answers, and part 7 is the build order. The
new authority is recorded as a decision:
[self-repair pane writer](../../memory/decisions/2026-11-09-self-repair-pane-writer.md).

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
  restarts it on a crash (its restart budget), so the watcher inherits that supervision. **Today**
  the units are never enabled, and only `agentbus up` or `service start` starts a project, so the
  watcher runs only once the person has started it. The person's answer (Q8) amends that decision:
  starting at login becomes an **option**, off by default, for systemd and launchd, without
  linger. It is decided but not built (part 7, step 9). Until it is built, or while the option is
  off, the watcher does nothing after a reboot until the project is started (4.4).
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
(default 60 s, measured by Redis `TIME`), on at least three reads. `snapLife` has one life rule
with reload, restore and pop-retire: a pid is gone only by `pidListedNoMore` (PR 388), and
`TestSnapLifeReadsAGonePidDead` guards it against the real `ProcStart` on a reaped pid (part 6).

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
   just dies again. The provider judged is the one of the role's **own** profile, the one a
   relaunch runs, never the snapshot's. A role on a quota fallback (`{p}:fallback`) is only
   reported to master, never relaunched: the relaunch would run its own profile, and bringing it
   back is master's `swap` (foureyes, step 7b).
5. **A session to resume.** Every client is relaunched **resuming** its proved session (the
   person, Q4): Claude Code (`--resume <id>`, as today), and Codex, Kimi and Gemini once their
   session ids are captured and proved (part 7, step 5). Never a fresh start. A client with no
   resume at all (Grok, to be verified in step 5) is only reported to master; the watcher never
   relaunches it.
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

**Action.** The core of what `agentbus reload` runs after its kill (`relaunchRole`): judge the tab,
split a **new pane** from the tab's pane, run the template command in it, and close the old pane
once the new agent is proven. Nothing is typed into an existing pane.

`relaunchRole` cannot be called from the service as it is: it first refuses to run outside a herdr
pane (`HERDR_ENV=1`), and the dispatch service is a systemd or launchd unit, not a pane. The split
is therefore:

- **a core** in a package (template tab, `judgeTab`, the settle loop, the split, the run, the
  proof, the close), taking its herdr runner and its lock as arguments, with no environment check;
- **the CLI entry** (`reload`, `up --resume`), which keeps the `HERDR_ENV=1` guard and `up`'s lock,
  then calls the core;
- **the service entry**, which reaches herdr the way the dispatcher already does (`dispatch.Herdr`,
  the herdr binary on the session named by `HERDR_SESSION` / `HERDR_SOCKET_PATH` from the services'
  env file, `service/env.go`), takes `up`'s project lock too (so it never splits a tab while an `up`
  runs), and calls the core. It refuses when the env file names no herdr session.

**Proof.** A new pid, alive, in a new pane, on the same client and provider, within 60 s
(`replaced`, reload's rule). That is all `replaced` proves: the snapshot carries the model but not
the profile's name, so two profiles of one client and provider are not told apart. The watcher
relaunches the role's own profile from `roles.toml` (it never picks one), and the gap is reload's
today too; closing it (the launcher recording its profile in the snapshot) is a separate step, not
part of this spec. Then a ledger entry `self-repair relaunched <agent>: pid A -> B,
pane P -> Q`, one line to master, and the ordinary wake for any message waiting.

**Failure.** No proof in 60 s: one escalation to master, and a desktop notification to the
person (Q7),
with the recovery line reload prints today (`agent-launch <role> <p> --resume <id> in pane …`).
No retry for that episode.

**Master.** Relaunched **first** when several are dead (the person, Q6: the system must run on its
own), since it is the one that could otherwise recover the rest. The relaunched master does not
inherit the pilot lease the dead one held; it takes it the usual way once the old lease expires.

### 4.2 R2: a template tab is missing

The tab of a template role was closed (a person's click, a herdr crash restoring the workspace
without it). For a **required** role (the person, Q3), with R1's preconditions, `judgeTab` reads
`create`, and the watcher creates the tab and relaunches into it, exactly as `up --resume` does.
For an optional role, it only reports the missing tab to master.

- **Required** is a new key of a role in `roles.toml`, `required = true` (absent: false). `roles
  doctor` shows it, and `required` on a pop is refused (a pop comes back through master only).
  The shipped templates mark the project's standing roles required.
- **A required agent is stopped on purpose by `retire` or `suspend`**, never by closing its tab:
  those declare the departure (precondition 1), closing a tab does not.

### 4.3 R3: network lost, then back

The watcher never "repairs" a network. While the endpoint fails it **holds R1** (precondition 4)
and says so once (`self-repair: holding relaunches, <provider> unreachable since HH:MM`). When the
network returns, agents whose processes survived need nothing: their next wake works. Agents that
died meanwhile go through R1 then, oldest first, one at a time.

### 4.4 R4: after a reboot or a resume from suspend

**After a reboot the watcher is not running.** The services are never started at login (part 2),
so nothing watches until the person runs `agentbus up <p>` (which relaunches the agents itself) or
`service start`. R4 then covers only what follows a start:

- **started with the workspace closed** (`service start` alone): relaunch nothing, and say once
  that the workspace is not open (`run agentbus up <p> --resume`), to master on the bus (Q7);
- **workspace open** (herdr restored it): R1 and R2 apply as usual. Panes herdr restored with a
  live client are recovered by the dispatcher's existing rule (codex-identity-recovery), not
  relaunched.

**Start at login** (the person, Q8) is an option, `services.start_at_login` (off by default),
settable in `config.toml` and busmon's settings screen (`C`). With it on, the project's services
start when the person logs in, and the watcher covers a reboot as soon as herdr opens the
workspace. **No linger**: without a session, herdr and the workspace are not open, so there would
be nothing to relaunch into. It goes through one Go interface, one backend per platform:

```go
// LoginStart enables or disables a project's services at the person's login.
type LoginStart interface {
	Enable(project string) error
	Disable(project string) error
	Enabled(project string) (bool, error)
}
```

- **Linux with systemd:** the units gain `[Install] WantedBy=default.target` only while the
  option is on, and `systemctl --user enable`; `disable` when it goes off.
- **macOS:** a LaunchAgent in `~/Library/LaunchAgents` with `RunAtLoad`. macOS has no `/proc`, so
  the watcher relaunches nothing there until a process identity exists for it (part 6).
- **Linux without systemd** (OpenRC, runit, …): **not covered**. The person suggested XDG
  autostart, but `agentbus service start` needs a service manager: without `systemd --user` it
  exits 1 and points to herdr tabs (`service.Local`). Running the dispatcher outside a manager is
  what the project services decision forbids ("an explicit fallback, never a hidden process", no
  `nohup`), and this amendment does not lift that (foureyes). Covering it needs a supervision
  story of its own (a user service of the system's init, or a second amendment to that
  decision). It is an open question for the person (part 8).
- **Windows:** not covered. One lead is WSL2 with systemd, woken by a scheduled task at logon.
  This is an open question for the person (part 8).

This amends the project services decision's former "never at login" into an option (see the
decision note); its "never a hidden process" rule stands.

On a resume from suspend (a monotonic jump, the same boot id), R1's grace restarts from the resume,
so a long sleep never counts as an agent "dead for an hour".

### 4.5 Budgets and the kill switch

- **Per agent:** one relaunch per 30 min, three per 24 h by default (the person, Q5). A fourth
  death in a day is a crash loop: escalate it, and relaunch nothing more for it until the person
  or master clears it with `agentbus self-repair reset <agent>`. Other agents are refused.
- **Settable:** `dispatch.self_repair_every` (30m) and `dispatch.self_repair_per_day` (3), in
  `config.toml` and busmon's settings screen (`C`).
- **Per project:** at most one relaunch in flight, then a 2-min pause before the next agent.
- **Switch:** `dispatch.self_repair = on | off`, **default `on`** (the person, Q1), overridable
  with `AGENT_BUS_SELF_REPAIR`, and shown on the settings screen.
- **`shadow`** records what the watcher would do (health check `self-repair`, a `would relaunch …`
  ledger line) and acts on nothing. It is allowed **only in development mode**. Outside it,
  config validation refuses `shadow` with the key named, and the settings screen does not offer
  it.
- **Development mode** (the project has none today): `AGENT_BUS_DEV=1` in the environment, or
  `dev = true` in `config.toml`. It is one boolean registered in `config/keys.go`, read once at
  start, and only ever unlocks extra values such as `shadow`. It never weakens a check. busmon
  shows `dev` in its header while it is on.

Every key above is registered in `config/keys.go`, the one settings registry, and appears on
busmon's settings screen (the person: the mode, the budgets and the login option all belong
there).

## 5. What the person and the team see

- **Notifications** (the person, Q7: as recommended). Today the dispatcher rings the desktop only
  for an agent blocked on its client's prompt (prompt-notify decision) and for a `/clear` without
  proof (agent-lifecycle decision). The watcher adds four events: a relaunch that failed its proof,
  a spent budget, the workspace not open after a start, and relaunches held by a lost network.
  Each goes to master on the bus; the desktop rings only for the first two.
- **health `self-repair`:** `ok`; `warn` while R1 is held (network, quota, a tab not settling) or
  in shadow mode with a would-be action; `fail` on a failed proof or a spent budget, which wakes
  sentinel as any fail does. Sentinel only annotates it and tells master; it never retries.
- **Ledger:** each action in the swap ledger (`agentbus swaps`) as a `self-repair` kind, with its
  reason. Master's next turn reads it like a reload.
- **busmon:** the agent's row shows `relaunched by self-repair HH:MM` for an hour.
- **Report:** one line to master per relaunch: `self-repair: foureyes relaunched (pid 241 -> 902,
  resumed 58b5f18b), it was dead 70 s`.

## 6. Correctness rules it must follow (learned the hard way)

- **One life rule.** "Dead" is `pidListedNoMore`, the rule reload, restore and pop-retire use, and
  `snapLife` (R1's trigger) delegates to it. The 2026-11-09 failure came from a second, drifted
  rule: `snapLife` matched `os.ErrNotExist`, which `ProcStart` stopped returning on 2026-11-03.
  PR 388 fixed it. Since then `snapLife` reads a gone pid through `pidListedNoMore`;
  `TestSnapLifeReadsAGonePidDead` runs the real `ProcStart` on a reaped pid, and the settle tests
  fake the error `ProcStart` really returns. The watcher adds no rule of its own.
- **Dead and gone are different from unknown.** No `/proc` (macOS), a failed read, a pid with no
  start time: unknown, and unknown never relaunches. On macOS the watcher therefore relaunches
  nothing until a process identity exists there.
- **The tab, not just the pid.** A dead pid with a tab still showing the client is waited on
  (bounded, as `relaunchRole`'s settle loop does), never relaunched next to it.
- **Every decision is a pure function** (`decideRepair(facts) -> action`), tested by table, the
  way `Decide` and `decideRelaunch` are. The loop only gathers facts and applies the action.

## 7. Delivery, in steps

Each step is its own PR from `origin/main`, reviewed by both reviewers. The order puts what is
useful alone first, and nothing acts before step 7.

1. **Split the relaunch code**: its core (`judgeTab`, `decideRelaunch`, the settle loop, the
   split and run, `replaced`, the close) into a package; the CLI entry keeps `HERDR_ENV=1` and
   `up`'s lock; a service entry reaches herdr through the services' env file (4.1). No behaviour
   change for `reload` and `up`.
   *As built (step 1):* the package `relaunch/` (`relaunch.go`: the judgement and `Open`;
   `role.go`: `Role`, `FindWorkspace`, `ProjectLock`, `CacheDir`, `ReadTemplate`, and the service
   entry `ServiceHerdr` plus `ServiceRole`). `cmd/agentbus` keeps `upTab` and `upTemplate` as
   aliases and `relaunchRole` as the CLI entry. `reload`'s and `up`'s tests pass unchanged against
   the moved code.
2. **The intent:** the fenced `self-repair` kind of `BeginSwap`/`EndSwap` (lease token and nonce),
   and `up --resume` skipping a role whose intent is held. Useful alone: today two people (or a
   person and master) running `up --resume` at once can already race.
   *As built (step 2):* `bus/selfrepair.go`: `SwapKindSelfRepair`; `BeginSelfRepair(token, role,
   nonce, ev)` checks the lease, the role's snapshot (as a reload's intent does) and `SET NX PX`,
   and logs the `begin` (kind `self-repair`) in one script; `SelfRepairHeld` (the check right
   before the split) and `EndSelfRepair` share one script: the lease, then the intent's kind and
   nonce, `ErrNotOwnIntent` for any other. `SwapIntentOf` reads one role's intent. `up` (with or
   without `--resume`, the dry run too) skips any held intent by kind, read per role right before
   it acts (`relaunch.Options.Held`, `upDeps.Intent`); `reload`, which holds its own intent, sets
   no `Held`.
3. **The settings:** development mode (`AGENT_BUS_DEV`, `dev`); `dispatch.self_repair` (`on` |
   `off`, `shadow` only in dev, refused otherwise); the two budgets; `services.start_at_login`
   (stored only, no backend yet). All in `config/keys.go` and on busmon's settings screen.
   *As built (step 3):* five keys in `config/keys.go`: `dev` (top-level, `AGENT_BUS_DEV` 1/0),
   `dispatch.self_repair` (`AGENT_BUS_SELF_REPAIR`), `dispatch.self_repair_every`
   (`AGENT_BUS_SELF_REPAIR_EVERY`, above 0), `dispatch.self_repair_per_day`
   (`AGENT_BUS_SELF_REPAIR_PER_DAY`, above 0) and `services.start_at_login` (the machine's file
   only, no env). `Key.DevOnly` lists the values taken only in dev: outside it, from the env or a
   file, the key resolves `Invalid` with `Resolved.Why` naming it, `agentbus config` prints the
   reason, and a file's line is a warning. A value its rule refuses is `Invalid` too, from the
   env or from the file (`Key.FailClosed`: `self_repair = false` never falls to the default). The
   watcher (steps 6 and 7) reads the switch through `config.SelfRepairMode`, which reads any
   refusal as `off`. `config.Set` and `Unset` learned a top-level key. busmon's C page shows
   the five in Services. It offers `shadow` only in dev and keeps a refused line editable, and the
   header says `dev`.
4. **`required` in `roles.toml`:** the key, its validation (refused on a pop), `roles doctor`, the
   shipped templates.
   *As built (step 4):* a role's `required` (schema 1 and 2, absent false). `rolecfg.Load`
   refuses a value other than true or false, and `required = true` on a pop (`required = false`
   on a pop is accepted). `Manifest.RoleRequired` reads it. `roles doctor` has a `REQ` column
   (`yes` or `-`) and `required` in `--json`. `examples/roles.mixed.toml` marks every boot role
   required (`TestExampleManifestMarksTheBootRolesRequired`). **Rollout (foureyes):** the
   shipped `roles.toml` gains `required = true` on its boot roles (master, coder, foureyes,
   sentinel) only in a follow-up, merged once a release with this parser is installed. Every
   session reads the main checkout's `roles.toml` with the installed `agentbus`, and an older
   parser refuses the unknown key, so no role could launch, relaunch or swap. The follow-up
   landed after agentbus v0.28.0-390-gcda8cf13 was installed: `roles.toml` marks its four boot
   roles, and `TestShippedManifestsMarkTheBootRolesRequired` checks both manifests. Nothing
   acts on the key before step 7.
5. **Every client resumes:** capture and prove the session id of Codex, Kimi and Gemini, as Claude
   Code's is today (the launcher's resume flag for each, the `SessionStart`-style proof); verify
   whether Grok has a resume at all. This is useful alone: `reload` and `up --resume` gain it too.
6. **Facts and decision, acting on nothing:** `decideRepair(facts)` as a pure function, health
   `self-repair`, and the ledger's `would relaunch`. In development mode, `shadow` runs it
   against this project for a while.
   *As built (step 6a, the judgement):* `selfrepair/decide.go`: `Decide(Facts) []Decision`, one
   decision per agent (`none`, `unknown`, `wait`, `skip`, `hold`, `report`, `budget`, `relaunch`,
   `create`) with its reason, at most one `Act` per pass (master first, then the oldest death;
   none while a relaunch is in flight or within the 2-min project pause). R1's preconditions in
   the spec's order: alive and unknown first (unknown never acts), `off`, the grace (60 s, three
   reads), a declared departure or another intent, a role on a quota fallback (reported), herdr and
   the workspace (step 8: before the tab, which is judged only through them), the tab
   (`relaunch`/`create` go on, an optional `missing` tab is reported, any other verdict is waited
   on), the own profile's provider (fresh, not held), a resume-verified client with a proved session (else reported,
   never a fresh start), then the budget (a crash loop or the day's count escalates, the spacing
   waits). `Health(mode, decisions)`: ok when off or nothing to repair (a report stays ok); warn
   for the shadow's would-be act, a hold or a tab not settling; fail for a crash loop, but never
   in shadow (it acts on nothing, so it spends no budget). `bus.LogWouldRelaunch`: the shadow's
   ledger entry (`would-relaunch`, kind `self-repair`, the death episode), fenced on the dispatch
   lease; `WouldRelaunchLogged` finds an episode's entry, so a death is recorded once. Step 6b
   wires the loop (gathering the facts, the health check, the shadow run) and the help text.
   *As built (step 6b, the shadow run):* the dispatch service reads the switch once at its start
   (`serviceSelfRepair`, `cmd/agentbus/selfrepairchore.go`); only `shadow` runs a pass, a
   dispatch chore every 15 s while it holds the lease (`dispatch.SelfRepairConfig`, beside the
   reconciliation, so a slow herdr never delays a wake). A pass reads the template's agent tabs,
   the snapshots, the pause, the quota marks (holds, hand-off clears, pops told done), the
   provider readings, the stored checks (`llm-endpoint` for a local model) and each role's
   intent; it counts a death per episode (`pid:start`) from its first read, Redis `TIME`; it
   reads herdr only when an agent is dead, the way the service entry of a relaunch does
   (`relaunch.ServiceHerdr`, `FindWorkspace`, `WorkspacePanes`, `JudgeTab`, create for a
   required role), any failure a hold. `Decide`, then one fenced `would-relaunch` per death,
   kind `self-repair`, `by dispatch`. A failed read fails the pass, logged once: no facts, no
   decision. Relaunches and the crash loop stay empty: only step 7 relaunches and records them.
   Health `self-repair` (catalogue order 31, master's) is registered in every mode: `off` and
   `on` (nothing relaunches yet) say so, shadow reports its last pass, unknown after 2 min
   without one. The foreground `agentbus dispatch` has no services env to reach herdr, so it
   runs neither. `Interrupted`, and `agentbus swaps`' interrupted mark, look past
   would-relaunch entries (foureyes), and busmon's help says shadow records now and on
   relaunches nothing yet; the budgets are read, but with no relaunch recorded neither changes a
   shadow decision. The death count lives in the service's memory: a dispatcher restart starts
   a death's grace again (conservative; step 7 keeps it in mind).
7. **R1 and R2 on** (default `on`): the relaunch, the proof, budgets and `self-repair reset`,
   master first, required tabs recreated and optional ones reported, the notifications. Live
   check: kill an idle agent's client (SIGTERM) and watch it come back, resumed, in a new pane.
   *As built (step 7a, what acting needs; nothing acts yet):* `bus/selfrepair.go`:
   `FinishSelfRepair` ends a self-repair with `relaunched` (the proof) or `failed`, kind
   `self-repair`, and ends its own intent in the same script, fenced on the lease and the nonce
   (an expired intent still gets its entry). The budget's past is its own record,
   `{p}:selfrepair`, not the ledger, whose reads are bounded to its last 1000 entries
   (reviewer-codex): the begin's script appends the attempt (a failed proof spends the budget
   too), a failed finish's appends its episode, and `MarkCrashLoop` (fenced, set once so the
   escalation is sent once) marks the crash loop; `SelfRepairPast` reads them.
   `agentbus self-repair reset <agent> [--as <person>]` (`ResetSelfRepair`) clears all three;
   only master or a person runs it, the caller read from its process ancestry (a registered
   agent's verified process), else its environment, and any other agent refused. `Decide` gains `failed`: this death's relaunch failed its
   proof, escalated, no retry for the episode until a reset; health fails on it, as on a crash
   loop. The pass fills `Relaunches`, `CrashLoop`, `ProofFailed` and the project's
   `LastRelaunch` (any role's attempt, for the 2-min pause) from it, so a shadow pass already
   decides with the budgets. `relaunch.Options.Fence` (`Deps.Fence`) is read right before the
   split or the tab create, after the judgement: it is where step 7b checks `SelfRepairHeld`.
   Step 7b turns `on` into acting: `BeginSelfRepair`, `ServiceRole` with the fence, the proof,
   the finish, the crash-loop mark, and the notifications.
   *As built (step 7b, on acts):* `cmd/agentbus/selfrepairact.go`. On mode (the default) runs the
   pass, then applies its one allowed decision: the intent (`BeginSelfRepair`, a fresh nonce), the
   service entry (`relaunch.ServiceRole`) with the fence `SelfRepairHeld`, a 60 s proof (reload's
   `replaced`, against the role's own profile from `roles.toml`), `FinishSelfRepair` relaunched or
   failed. Master gets one line per relaunch; a failure (no proof, or the relaunch's own error) is
   escalated with the recovery line and rings the desktop, once, never retried for that death; a
   spent day marks the crash loop and does the same. Reports (an optional tab missing, a client
   without a verified resume, no proved session) reach master once per death. All messages are
   fenced, once per episode. Not built in 7b: busmon's `relaunched by self-repair` row line, R3's
   held-network notice and R4's workspace-not-open notice (step 8). The live kill check is the
   person's.
8. **R3 and R4:** hold relaunches while the network is lost; the notice when the workspace is not
   open after a start. *Built:* a hold carries its kind (`Decision.Hold`: herdr, workspace, quota,
   network; a network hold its provider and last good reading); master is told once per provider
   and last good reading (`self-repair: holding relaunches, <provider> unreachable since HH:MM`),
   and once per spell of a closed workspace (`run agentbus up <p> --resume`), fenced
   (FencedCmdOnce). Herdr and the workspace are judged before the tab: with the workspace closed
   the tab cannot be judged, which had left every dead agent waiting on "tab unknown".
9. **Start at login:** the `LoginStart` interface and its two backends (systemd, macOS
   LaunchAgent), wired to the step 3 option. Elsewhere, enabling the option is refused with the
   reason ("no service manager to start at login").
10. **macOS process identity** (later), so R1 can act where there is no `/proc`.

## 8. The person's answers (2026-11-09)

1. **Default mode:** `on`. `shadow` only in development mode, refused outside it (4.5).
2. **A new pane writer:** accepted. It is a written decision amending the event-driven dispatch
   decision, the agent lifecycle decision and `CLAUDE.md`'s rule
   ([self-repair pane writer](../../memory/decisions/2026-11-09-self-repair-pane-writer.md)).
3. **A closed tab:** a required agent (`required = true`) is recreated, an optional one only
   reported; a required agent is stopped by `retire` or `suspend` (4.2).
4. **Clients and resume:** every client resumes, with its session id captured and proved; a
   client with no resume (Grok, to verify) is only reported (4.1, precondition 5; step 5).
5. **Budgets:** 1 per 30 min and 3 per 24 h by default, settable in `config.toml` and busmon's
   settings screen; master may reset a spent budget (4.5).
6. **Master:** relaunched first (4.1).
7. **Notifications:** as recommended (part 5).
8. **Start at login:** an option, off by default, one backend per platform, no linger (4.4).

The person also noted that R1's trigger used `snapLife` while part 6 required one life rule. The
two now agree: `snapLife` delegates to `pidListedNoMore` (PR 388), and a test guards it (4.1,
part 6).

**Still open, for the person** (4.4):

- start at login on **Windows**. The lead is WSL2 with systemd, woken by a scheduled task at
  logon;
- start at login on a **Linux without systemd**. The suggested XDG autostart cannot start the
  services, because `agentbus service start` needs a manager, and running the dispatcher outside
  one is what the services decision forbids. Covering it needs a supervision story (the init
  system's own user services, or an amendment to that decision).

## 9. Not in this spec

- Restarting herdr, the broker, Docker or the network: the person's machine, the person's acts.
- Stalled turns (a live process that does nothing): the existing `quiet-agent`, `runaway` and
  `team-stall` checks.
- Distributed hosts: the watcher acts only on the host whose `/proc` holds the agent.
