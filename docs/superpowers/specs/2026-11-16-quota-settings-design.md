# Quota thresholds as busmon settings: design (P1-10)

Status: draft for the person's answers (§8). No code yet.
Asked by: the person's reliability plan (directive 1794830959056-0, item 10), through master.
Base: origin/dev `e6d9cfcc`.

The person's three requests:

- **(a)** An editable "start nothing new above" threshold, separate from the warning threshold, plus
  `quota_hold`, per project. They need validation, and the services must be restarted or reloaded
  for them.
- **(b)** The drain shown with its reason, the current usage, and the reset as a date and a time.
  It said "13:59" for tomorrow's 13:59.
- **(c)** `agentbus run reset` does nothing while usage is above the threshold: the drain comes back
  silently. It must say so. The person also needs an explicit "override the drain until <time>"
  action in busmon.

## 1. What exists today

Read from the code at the base, with the lines that do it.

### 1.1 The settings

`config/keys.go:240-264` holds the registry rows:

| Key | Env | Default | What it does |
|---|---|---|---|
| `dispatch.quota` | `AGENT_BUS_QUOTA` | on | the whole quota policy |
| `dispatch.quota_warn` | `AGENT_BUS_QUOTA_WARN` | 85 | warn **and** drain (§1.2) |
| `dispatch.quota_hold` | `AGENT_BUS_QUOTA_HOLD` | 95 | hold the provider's agents; pause the project if it is master's |
| `dispatch.quota_max_age` | `AGENT_BUS_QUOTA_MAX_AGE` | 30m | an older reading is unknown for the policy |
| `dispatch.quota_long_hold` | `AGENT_BUS_QUOTA_LONG_HOLD` | 2h | a hold longer than this asks master for a decision |

- **Validation:** both thresholds are a positive percent (`filePositivePercent`). Nothing checks
  `warn < hold`.
- **Where they are read:** `serviceQuota` (`cmd/agentbus/quotacmd.go:195-212`) reads them **once,
  when the dispatch service starts**, through `config.Machine`: the env first, then `config.toml`.
  A change applies only after the dispatch service restarts.
- **Per project:** the only per-project source is the service env file (`Paths.EnvFile(project)`,
  `service/env.go`), which `up` writes and the unit loads. It sits above `config.toml`. No screen
  edits it.
- **busmon:** the keys are in the Services group (`config/group.go:30-32`), but the Settings page
  shows only `servicesOnPage` (`cmd/busmon/settings_page.go:89`), and the quota keys are not on it.
  They are reachable only by editing `config.toml` or the env file by hand.

### 1.2 What `quota_warn` triggers

`dispatch/quota.go:134-164`. For each provider in use, for each usable window at or above
`WarnPct`, three things happen:

1. **Agents with work are warned:** `quotaWarnAgents`, run-budget §4. Once per window and agent,
   each agent on that provider holding an accepted task or `working` gets "finish your step,
   commit … start nothing new".
2. **The run drains:** if the provider is master's (`coord`), `quotaDrainRun` calls
   `FencedRunDrain(why = quota:<p>)`. The run enters `draining`. Master and the person are told
   once per drain episode (`quotaDrainTell`, marks `run-drain` / `run-drain-person`, episode
   `<run start>:<drains>`).
3. **Master is warned:** once per window (`warn:<p>:<window>` mark) master gets the directive
   `Quota <p> 88% (five_hour, resets 13:59): finish what is in flight, start nothing new.`

So today "warn" and "start nothing new" are **one threshold**.

**The drain ends** (`FencedRunQuotaRelease`) when the provider is not held and every usable window
is under `WarnPct` (`warnClear`, quota.go:224 and :402), or at the hold's release (`release`,
quota.go:312).

**`quota_hold`** (quota.go:166-238) holds the provider. Its agents are not woken. If it is
master's provider, the project pauses until the reset plus 2 minutes (`FencedQuotaPause`).

### 1.3 The times shown

- **Format:** every quota message formats the reset as `Format("15:04")`: quota.go:157 (master's
  warn), :455 (the agents' warn) and :473 (the drain's detail).
- **Why "13:59" was wrong:** a `five_hour` window resets within 5 hours, but a `seven_day` window
  can reset tomorrow or in six days. So "resets 13:59" can mean a time that is a day or more away.
  That is the field report.
- **busmon:** the account cards use `resetClock` (`cmd/busmon/account_cards.go:110`). It shows
  `15:04` when the reset is within 24 h, else `Mon 15:04`. Tomorrow 13:59 seen at 14:30 today is
  within 24 h, so busmon also shows "13:59".
- **`agentbus run`** (cmd/agentbus/run.go:42-99) prints the state, `since`
  (`2006-01-02 15:04`), `why` (`quota:anthropic`) and "drain until the quota's release". It does
  not print the usage or the reset.
- **busmon does not read the run at all:** no `bus.Run` reader in `cmd/busmon`. A drain reaches
  the person only as the one-time notice `<p>: the run drains for anthropic's quota (88%,
  seven_day, resets 13:59): …`.

### 1.4 `run reset` during a quota drain

- **What it does:** `agentbus run reset` (run.go:24-37, `bus.RunReset`) deletes `{p}:run` and lifts
  a kind-run pause. It prints "the next merge-queue pass starts a new run".
- **What follows:** the next merge-queue pass creates a running run. The next quota pass finds the
  same window still at or above `quota_warn` and drains it again (quota.go:146-150). The new run
  has a new start, so a new episode, and the drain is told again through master's directive and
  the person's notice. Nothing at reset time says this will happen. To the person, the reset
  "does nothing".
- **No way out:** besides turning the whole policy off (`dispatch.quota = off`, restart) or raising
  `quota_warn` (restart), there is no way to run past the drain.

## 2. Design

The parts marked **(Qn)** depend on an answer in §8. The design gives the option this spec
recommends, never as decided.

### 2.1 Three thresholds, one per act (request a)

- **New key `dispatch.quota_drain`:** "start nothing new above", a percent, read by dispatch.
  - Default: equal to `dispatch.quota_warn`, so a person who sets nothing keeps today's behaviour.
    **(Q1: its default.)**
  - It alone triggers act 2 (the run drains) and act 1 (agents with work are told to finish their
    step). **(Q2: does act 1 go with the drain or with the warning?)**
- **`dispatch.quota_warn`** keeps act 3: master's one warning per window, before the drain.
- **`dispatch.quota_hold`** is unchanged.
- **Release:** `warnClear` becomes `drainClear`. A drained run returns to running when every usable
  window is under `quota_drain`, not under `quota_warn`.
- **Validation:**
  - In the registry, each key is a percent in (0, 100]. A file value outside that is refused, as
    today.
  - Across keys, `warn ≤ drain ≤ hold` must hold. This is checked by the busmon page before it
    writes (as `settingRefusal` does for `attach.prefix`) and by the dispatcher at start. A set
    that breaks the order is refused on the page, naming the two keys. If the dispatcher finds a
    broken order in an env file, it logs it and uses the defaults for the offending keys.
    **(Q3: is the order a rule, or may drain sit above hold?)**

### 2.2 Per project, and applied without a restart by hand (request a)

**Two facts constrain where a project's value can live.**

- **`up` rewrites the project's env file whole.** It builds the file from its own `AGENT_BUS_*`
  environment (`service.CollectEnv`, `envPrefixes`) and replaces the file with `WriteEnvFile`. It
  carries over only `AGENT_BUS_ESCALATE_TO` and the reload mode, each with its own code
  (`cmd/agentbus/up.go:431-448`). Any other key written there by someone else is gone after the
  next `up`. That is why `auto_clear` set in the env file needs `AGENT_BUS_AUTO_CLEAR=1 agentbus up`.
- **The dispatcher's process environment is a start-time copy.** The unit loads the env file into
  the dispatcher's environment when it starts, and `config.Machine` reads the environment before
  `config.toml`. So a dispatcher that re-reads the files at each pass, but still asks
  `config.Machine(os.Getenv)`, keeps the start-time value: a change would never apply, and a value
  removed from the file would stay.

**Where a project's value lives (Q4).** Two options, with their real costs:

- **A. The project's service env file.**
  - `up` gets a list of quota keys it carries over from the old file, as it does for escalate and
    reload. A value busmon wrote survives `up`.
  - Precedence when the person's shell, at `up` time, carries `AGENT_BUS_QUOTA_WARN` and busmon
    wrote another value: the shell's value is what `up` writes today. **(Q4b: which wins, the
    shell's at `up` or the one set in busmon?)** This spec suggests busmon's: it is the value on
    screen.
  - Cost: a carry-over list to keep in step with the keys, a precedence rule at `up`, and a busmon
    write into a file `up` owns.
- **B. A `[project.<p>]` table in `config.toml`.**
  - A new resolver layer. For these keys, `config.toml`'s project table comes above its machine
    keys. `up` never touches `config.toml`, so nothing is carried over.
  - The shell's environment at `up` still reaches the env file and the dispatcher. For these keys
    the dispatcher reads files only (below), so the env file's value would be ignored unless the
    rule says otherwise. **(Q4b applies the same way: should an `AGENT_BUS_QUOTA_*` in the shell at
    `up` still win?)**
  - Cost: a new layer in `config` (Resolver with a project), and a project's values that outlive
    its deletion unless `projects delete` removes its table.

Recommendation: B, now that A's carry-over is written down. It is one file the person can read,
and no other command rewrites it. A stays a fair choice: it has no new resolver layer.

**Applying a change.**

- **What the dispatcher reads, for these four keys only** (`dispatch.quota_warn`, `_drain`,
  `_hold`, `_max_age`):
  - Option A: the project's env file and `config.toml`, read from disk.
  - Option B: `config.toml`'s project table, then its machine keys.
  - **The process environment is not consulted** for these keys. The file read at each refresh
    replaces it, so a key removed from the file is gone (busmon's `D`), and the start-time copy
    never wins.
  - Every other key keeps today's resolution.
- **When:** the quota policy runs at every reconcile, but readings are collected every `q.Every`
  (60 s, `quotaPass`). The thresholds are re-read **with the readings**, at the same refresh, so
  two small files are read once a minute, not at every tick. A change applies within a minute with
  no restart, like the self-repair and reload settings, which are already "read at each call".
- **A file that cannot be read** (missing, unparsable): the last thresholds read stay in force and
  the dispatcher logs it once per episode. A dispatcher that has never read one uses the
  registry's defaults.
- **(Q5):** or should busmon restart the project's dispatch service after a save instead? That is
  simpler in the dispatcher (none of the above), but a restart takes the lease away for a few
  seconds, and with A the restart alone still loses the value at the next `up`.

**busmon page.** A "Quota" group on the Settings page, for the current project:

- **Rows:** "Warn master at", "Start nothing new above", "Hold agents at", each a percent.
- **Source:** each row shows where its value comes from (`project`, `machine`, `default`).
  `D` removes the project's own value.
- **The project's own values:**
  - When a project's value is set, the machine's value is shown beside it.
  - Editing writes the project's value. A separate row edits the machine's default.
    **(Q6: does the page edit the machine default too, or only the project's value?)**

### 2.3 The drain shown (request b)

- **Dates in every quota message.** The reset is written with a date whenever it is not today,
  using one helper (`quota.ResetLabel(t, now)`) for both the dispatcher's messages and busmon's
  cards:
  - today: `today 13:59`;
  - tomorrow: `tomorrow 13:59`;
  - further: `Wed 18 Nov 13:59`.

  It applies to quota.go:157, :455 and :473, and to `resetClock`. **(Q7: the exact format;
  English day names.)**
- **busmon shows the run:**
  - **Header:** when the run is `draining` or `stopped`, a line under the project header, e.g.
    `Run draining · anthropic seven_day 88% ≥ 85% (start nothing new) · resets tomorrow 13:59 ·
    since 14:02`.
  - **Agents page:** the same line, with what is in flight (the list `runInFlight` builds) when it
    is draining for a limit.
  - **Source:** `{p}:run` plus `{p}:budget`, read on busmon's existing refresh. No new broker key.
- **`agentbus run`** prints the same facts: the provider, the window, the usage, the threshold, and
  the reset with its date.

### 2.4 `run reset` says what will happen, and an override (request c)

**`run reset`.** Before deleting the run, `run reset` reads `{p}:budget` for master's provider. If
a usable window is at or above `quota_drain`, it still resets (it is the person's act), and prints:

```
run reset: a new run starts, but anthropic's seven_day window is at 88% (start nothing new above 85%):
it will drain again at once. To run anyway: agentbus run override --until <time>
```

**The override.** A new person-only command, `agentbus run override --until <time>`, and the
matching busmon action on the run line (`o`, with a time field). An agent environment is refused,
as `run reset` is.

- **Record:** it writes `override_until` (Unix ms) and `override_by` into `{p}:run`, unfenced like
  `run reset`. It is the person's act, not the dispatcher's.
- **Its effect:** while `now < override_until`, the quota pass does not drain the run for a quota
  (act 2), and a run already drained for a quota returns to running.
- **Unchanged:**
  - master's warning (act 3) and the agents' notice (act 1) are still sent;
  - the hold at `quota_hold` still holds, **(Q8: or does the override pass the hold too?)**;
  - the run budget's own limits (duration, deliveries) are not overridden.
- **When it ends:** the next pass applies the thresholds again. The person gets one notice when it
  starts and one when it ends (marks `run-override` / `run-override-end`, episode =
  `override_until`).
- **Limits:**
  - `--until` must be in the future and at most the tightest window's reset. Past the reset there
    is no drain to override. **(Q9: any upper bound, e.g. 24 h?)**
  - `--until` takes `HH:MM` (today, or tomorrow if passed), `tomorrow HH:MM`, or a duration (`2h`).
- **`agentbus run override --clear`** ends it early.

## 3. What does not change

- The hold at `quota_hold`, the project pause for master's provider, the long-hold question, the
  screen hold of run-budget §4, and every fenced write.
- The run budget's duration and delivery limits, and `run.drain_max`.
- `dispatch.quota = off` still turns the whole policy off.

## 4. Steps (one PR each, each gated by foureyes)

1. **Dates:** `quota.ResetLabel`, used in the three dispatcher messages, `resetClock` and
   `agentbus quota`. No setting changes. This is request (b)'s field bug alone.
2. **`dispatch.quota_drain`:** split from `quota_warn` (§2.1), the order validation, and the
   dispatcher reading the keys at each pass (§2.2, after Q4/Q5).
3. **`run reset`:** the warning, then `run override` and its record, the dispatcher honouring it,
   and the notices (§2.4).
4. **busmon:** the run line (§2.3) and the Quota settings group with the override action
   (§2.2, §2.4).

## 5. Tests

- **Dates (step 1):**
  - `ResetLabel` today, tomorrow, in 3 days, across midnight and across a DST change (a fixed
    zone). This is the field case: tomorrow's 13:59 seen at 14:30 reads `tomorrow 13:59`.
  - The dispatcher's three messages carry the date. busmon's card shows it.
- **Thresholds (step 2):**
  - **Between warn and drain:** at 86% with warn 80, drain 90, master is warned, the run is not
    drained and no agent is told to stop.
  - **At drain:** at 91% it drains.
  - **Release:** the drained run returns to running under 90%, not under 80%.
  - **Default:** with `quota_drain` unset, it behaves as today (one threshold), so the existing
    quota tests pass unchanged.
- **Order:** the page refuses warn 90 / drain 80 and writes nothing. A dispatcher started with a
  broken order logs it and uses the defaults.
- **Applied live:**
  - The project's value changed on disk between two refreshes is applied on the second, with no
    restart.
  - The machine value applies when the project sets none.
  - A value removed from the file is gone at the next refresh, even though the dispatcher's
    process environment still holds it from its start (the field case foureyes raised).
  - The process environment's start-time `AGENT_BUS_QUOTA_DRAIN` never wins over a newer file
    value.
  - An unreadable file keeps the last values and logs once.
  - The thresholds are read at the readings' refresh, not at every reconcile tick (a counted
    reader).
- **Survives `up`:** busmon sets drain 90 for the project, `agentbus up <p>` runs, and drain is
  still 90. With A, this tests the carry-over list. With B, it tests that `up` writes no
  `config.toml`.
- **Precedence at `up` (after Q4b):** a shell carrying `AGENT_BUS_QUOTA_DRAIN=70` at `up`, against
  busmon's 90, gives the answer the person chose.
- **`run reset` (step 3):** above the drain threshold it prints the warning and the override
  command; under it, today's line. Either way the run is reset.
- **Override:**
  - While active, a pass at 88% does not drain, and a quota-drained run returns to running.
  - At `override_until` the next pass drains again, and the person is told once at each end.
  - `--until` in the past or past the reset is refused. An agent environment is refused.
  - `--clear` ends it.
- **busmon (step 4):**
  - The run line appears for draining and stopped runs and is absent for running ones. Goldens
    cover light and dark.
  - The Quota group writes the project's value (A: its env file; B: its `config.toml` table), shows
    the source of each value, and `D` removes the project's value.
- Every dispatcher write stays fenced: a lost lease writes nothing.

## 6. Interplay

- **Swap:** a swapped master (coordinator on another provider) drains on its new provider's
  threshold. That is `coord` as today.
- **Pause and suspend:** a paused or suspended project is not drained or released by the override.
  The override writes only `{p}:run`.
- **Several projects:** each project's dispatcher reads its own values (A: its env file; B: its
  table). A machine value changed in busmon applies to every project that sets none, each at its
  next refresh.

## 7. Not in scope

- Per-agent or per-provider thresholds. One set per project, applied to every provider; the person
  can ask for per-provider values later.
- Changing how readings are collected (`quota.claude.probe`, readers).

## 8. Questions for the person

1. **Drain default:** what is the default of "start nothing new above" (`quota_drain`)? Equal to
   the warning (85, today's behaviour), or another value (e.g. warn 80, drain 90)?
2. **Which threshold tells agents to finish:** when agents with work are told "finish your step,
   start nothing new", should that happen at the warning threshold or at the drain threshold?
   Today it fires at the warning threshold. If the drain defaults to the warning (Q1), both answers
   keep today's behaviour until you set them apart.
3. **Order:** must the order always be warning ≤ drain ≤ hold, refused otherwise? Or may drain sit
   above hold?
4. **Where a project's values live** (§2.2 gives the costs):
   - A. the project's service env file. `agentbus up` rewrites that file, so it would need a list
     of quota keys to carry over;
   - B. a `[project.<name>]` table in `config.toml`, which nothing else rewrites (recommended).

   4b. **Shell or busmon:** when your shell carries an `AGENT_BUS_QUOTA_*` variable as you run
   `agentbus up`, and busmon holds another value for the project, which one wins? The suggestion
   is busmon's, the one on screen.
5. **How a change applies:** should the dispatcher re-read the thresholds from the files once a
   minute, with the quota readings, so a change applies with no restart (recommended)? Or should
   busmon restart the project's dispatch service after a save?
6. **Machine default on the page:** should the Settings page also edit the machine-wide default
   (every project without its own value), or only the current project's values?
7. **Date format:** is `today 13:59` / `tomorrow 13:59` / `Wed 18 Nov 13:59` right?
8. **Override and the hold:** should the override pass only the drain (recommended: the hold at
   `quota_hold` still holds), or the hold too?
9. **Override length:** is there an upper bound beyond "at most until the window's reset", e.g.
   24 hours?
