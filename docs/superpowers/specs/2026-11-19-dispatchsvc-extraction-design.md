# The dispatch service's runtime leaves `cmd/agentbus` for `internal/dispatchsvc`

Status: spec, r3, for review (subject `dispatchsvc-extraction-spec`). Not built. Base: origin/dev
`60720b72`. Author: architect, from master's directive of 2026-11-19 relaying the person's go on
"group A" of the extraction map (`docs/evaluations/2026-11-18-cmd-extraction-map.md` on
`origin/ccr-b0a4afca-gd18ae`, not merged) and on architect's report `1795024304775-0`. No code in
this PR. Every number below was measured on the base with a type-checked reference graph of
`cmd/agentbus`; a number is re-measured before the step that relies on it.

## 1. Why, and what does not change

`cmd/agentbus` is one `main` package of 30,684 lines. About 6,400 of them are the dispatch service's
runtime: the health checks' readers, the stall checks, the chores (merge queue, notifications,
backup schedule, self-repair, history), the reboot judge. They print nothing and parse no argument.
Inside one package nothing stops a CLI command and a chore from calling each other, and the rule
"the dispatcher never runs repository code" is held by review alone.

The move buys two things, and only these:

1. **A wall the compiler holds.** The runtime can call the CLI only through what this spec names.
2. **A guard test** (§6) that fails when the package imports what it must not (the CLI, the
   screen, or a module package not on its list), or itself runs a program that is not on its
   list. Programs run *inside* the module packages it imports (quota's readers, backup's hook,
   the merge queue's forge CLIs, service's `systemctl`...) are not seen by the guard; a new path
   to one is, through the pinned import list. §6 says exactly what each reaches.

The move fixes no bug and changes no behaviour. The one bug found while writing this spec (the
service's escalation target never reaches the stall checks and self-repair, §4 P0) is fixed in its
own PR before the move, never inside it. The move is not a refactoring of the runtime: no function
is rewritten, renamed for taste, split or merged. Both binaries keep their paths
(`./cmd/agentbus`, `./cmd/busmon`), so the installer, the build id and the release check are
untouched. No stream, key, setting or `subscribe` field changes. `config/keys.go` is not edited.

## 2. The cut

### 2.1 What moves (29 files, 6,354 lines, in the move PR; `deliverbase` leaves earlier, in P7)

`healthchecks` `stallchecks` `selfrepairchore` `mergequeuechore` `selfrepairact` `notifyfacts`
`reboot` `notifychore` `modelcatalogue` `runbudget` `redislatest` `githubcheck` `llmcheck`
`diskcheck` `idlemaster` `backupschedule` `reviewqueue` `healthchores` `idleimplementer`
`repocheck` `backupcheck` `popdonecheck` `notifycheck` `holdernotice` `dispatchhost` `loginexpiry`
`historyservice` `plandrift` `holderhealth`, plus `deliverbase` (to `forge/`, in a prerequisite,
§4).

With them, eleven functions that today sit in files that stay:

- from `servicecmd.go`: `serviceDispatchConfig`, `serviceReloadPolicy`;
- from `quotacmd.go`: `serviceQuota`, `quotaThresholds`, `serviceContext`, `serviceCompacted`,
  `serviceTokens`, `servicePrompt`, `serviceClear`, `servicePopRetire`, `desktopNotify`.

A prerequisite (§4, P8) first gathers these eleven into one file, so the move PR moves whole files
only.

### 2.2 What the map put in group A and stays in `cmd/agentbus`

| File | Why it stays |
|---|---|
| `servicerun.go` | It supervises both services, dispatch and missions. It is the body of `agentbus service run`, not a part of the dispatcher's configuration |
| `holderprobe.go` | `serviceRun`'s holder probe; nothing in the runtime uses it |
| `runrecord.go` | `up` and `suspend` write the run record too; it is a thin wrapper over `lifecycle/` |
| `popend.go` | The dispatcher's end of a pop is the same act as `retire --forget`: it shares `swapDeps` and the retire tests' fixtures. It crosses the wall as an injected function (§3) |

`hostrun.go` (the relay's runner) stays as well: it runs the CLI's own commands, and its closure
reaches 185 declarations of `up`, `suspend`, `swap` and `reload`.

**The rule behind the table: what judges moves; what acts on the team through a CLI command stays
and is injected.** Three acts are injected: the relay's runner, the pop's end, and the base
configuration. The guard test does not see inside them; they are CLI commands, reviewed as such.

### 2.3 Measured coupling, after the prerequisites

- The moving code needs **34 symbols** from the rest of `cmd/agentbus` today. §4 gives each a home.
  After §4 it needs none: the three injected acts arrive through `Deps`.
- The rest of `cmd/agentbus` needs **10 symbols** from the moving code. They become the four
  exported functions of §3.
- No moving file has an `init`. Their package-level variables are constants in practice (compiled
  patterns, error values) and two test seams (`dailyModelsRefresh`, `clientUpdateRefresh`), which
  move with their tests.

## 3. The interface of `internal/dispatchsvc`

Five exported identifiers. A sixth needs architect's yes in the PR.

```go
package dispatchsvc

// Deps is what the runtime takes from its caller.
type Deps struct {
	Bus     *bus.Bus
	Getenv  func(string) string
	Paths   service.Paths
	Workdir string          // the unit's working directory: the project's repository
	Boot    string          // this boot's id (lifecycle)
	Log     io.Writer

	Base    dispatch.Config                 // dispatch.DefaultConfig with the person's name set
	HostOps dispatch.HostOpsConfig          // the relay's runner: CLI commands
	PopEnd  func(dispatch.PopRetireConfig, dispatch.Runtime) dispatch.PopRetireConfig
	Manager service.Manager                 // the service manager the health checks read

	// SelfRepairSkew moves the time each self-repair read got from Redis
	// TIME. Tests only: nil in production.
	SelfRepairSkew func(time.Time) time.Time
}

// Config fills cfg, which the caller owns and keeps, with the dispatch service's
// whole configuration, and returns its runtime. cfg starts as d.Base.
func Config(d Deps, cfg *dispatch.Config) (*dispatch.HostRuntime, error)

// ForegroundConfig adds to cfg, in place, what the foreground agentbus dispatch
// runs: the pop's end, the health checks, the reload policy.
func ForegroundConfig(d Deps, cfg *dispatch.Config, rt dispatch.Runtime)

// Runtime is the dispatcher's runtime on the project's holder.
func Runtime(project string, getenv func(string) string) (*dispatch.HostRuntime, error)

// MergeQueueLive derives the merge queue now, as the chore does (merge-queue --live).
func MergeQueueLive(ctx context.Context, b *bus.Bus, getenv func(string) string) (bus.MergeQueueRecord, bool)
```

- `Config` is today's `serviceDispatchConfig`, its body unchanged but for where its inputs come
  from. `cmd/agentbus` passes `os.Getenv`, its `dispatchBaseConfig()`, `hostOpsConfig(...)`, a
  closure over `withPopEnd`, `runBootID()` and `os.Getwd()`. The values are the ones read today.
- `ForegroundConfig` is the six lines `runDispatch` runs today (dispatchcmd.go 44-50). It keeps the
  foreground command from importing three functions by name.
- **Both take `*dispatch.Config`, never a value.** The health checks and self-repair keep
  `&cfg.EscalateTo` (`realHealthConfig`, `newStallReader`, `serviceSelfRepair`): the escalation
  target is read when a check fires, and the caller sets it after building the configuration
  (`runDispatch` parses `--escalate-to` after line 46; `RunDispatch` sets it from
  `service run --escalate-to` or `AGENT_BUS_ESCALATE_TO`). A configuration copied after those
  pointers are taken loses every later write. The pointer must be into the config the caller keeps
  and hands to the loop. A rule for any later field read through a pointer: same.
- **`Manager` and `SelfRepairSkew` were added in P8 (architect, for the two end-to-end tests that
  stay in `cmd/agentbus` by rule 3 of §5).** They are fields, so the exported identifiers are still
  five.
  - `Manager` is real dependency injection. The health checks always took the service manager as
    a parameter (`realHealthChecks`); `realHealthConfig` detected it itself. The caller now detects
    it, beside `Paths`, and passes it: the same `service.Detect` call, so production behaviour is
    unchanged. The holder end-to-end test passes its world's fake manager and reads the "holder"
    check from `Config` by id.
  - `SelfRepairSkew` is a test seam. It adjusts the Redis `TIME` the self-repair read already got;
    nil leaves it. It never replaces the read: persisted times come from Redis `TIME`, and a
    replacement clock would break that rule. The release test shifts the grace with it, 25 s per
    read once its agent is killed. `TestNoProductionSelfRepairSkew` fails if a non-test file of the
    module sets it.
- **Host was dropped in P8: nothing in the package read it.** The caller still uses the broker's
  host to build `HostOps` (`hostOpsConfig(b, project, host)`). If the move finds a reader, the
  field comes back with that reader named.
- `Runtime` is `dispatchRuntime`, called by `main`, `suspend` and `swap`.
- `MergeQueueLive` replaces the two symbols `merge-queue --live` reaches into (the chore's
  constructor and its `derive`). `false` is today's "no working directory".
- **`Getenv` is passed, never completed.** Thirteen moving files read the process directly
  (`os.Getenv`, `os.Environ`, `os.Getwd`, `os.UserHomeDir`, `os.Hostname`: 34 calls, 18 in
  `healthchecks.go`). The move leaves every one of them
  as it is. Routing them through `Deps.Getenv` is a behaviour change; it is out of scope (§9).
- Error modes, ordering and timings are those of the functions moved. `Config` returns the error
  `dispatchRuntime` returns today, and nothing else.

## 4. Prerequisite PRs

Each is one PR from `origin/dev` into `dev`, with both reviews, mergeable in any order unless
stated. Each moves code as it is and updates its callers; the tests of the moved code move with
it. None needs the quiet window: they are small, and a conflict is a rebase of a few lines.

| PR | To | What (from) | Lines |
|---|---|---|---|
| P0 | in place | **A bug fix, first** (below) | ~30 + tests |
| P1 | `relaunch/` | `replaced` (swap.go), `resumeChoice` (upteam.go), `recoveryHint` (recoveryhint.go) | ~50 |
| P1 | `mission/` | `missionsOf` (swap.go): it reads `mission.Store` and nothing else | 23 |
| P2 | `lifecycle/` | `hostBootID` (main.go) | 4 |
| P2 | `service/` | the constant `gitWaitState` (servicerun.go) | 1 |
| P3 | `holder/` | `holderReady`, `holderTermList`, `holderTerms` (holdercmd.go); `holderAnswersAny` (holderhealth.go, used by `servicecmd.go`) | ~60 |
| P4 | `modelcheck/` (new; see below) | the doctor's rows: `doctorRows`, `doctorRow`, `doctorDeps`, `realDoctorDeps`, the `claudeAlias` pattern and their closure (doctor.go, ownprofile.go); the models check: `modelsCheckReport` and its closure (modelcheck.go) | ~470 |
| P4 | `models/` | `loadModelsConfig`, `modelsGetenv` (modelscmd.go); `refreshTable`, `lockTable`, `refreshModelsUnderLock` (healthchores.go, used by `agentbus models`) | ~80 |
| P5 | `backup/` | `makeArchive`, `archiveJob`, `backupDeps`, `realBackupDeps`, `hostProjects`, `backupLastPath`, `sortedUnique` (backupcmd.go) | ~140 |
| P6 | `usage/` | `collectAgentUsage` and its two helpers (budget.go) | ~50 |
| P6 | `peer/` | `endpointFPKey`, `endpointHost` (peer.go) | 14 |
| P6 | `mergequeue/` | `mergeQueueInput` (mergequeuecmd.go) | 7 |
| P6 | `notify/` | `desktopRun`, `notifyStateDir` (notificationscmd.go), `runQuiet` (quotacmd.go). `desktopRun` and `runQuiet` run a program (`exec.CommandContext(ctx, bin, args...)`): once in `notify/` they leave the guard's sight, and §6 lists `notify` as a package that runs programs | ~25 |
| P6 | `internal/textfmt` | `runDuration` (run.go); the package comes from the small items, or this PR creates it with this one function | 10 |
| P7 | `forge/` | `deliverbase.go` whole (`projectDeliveryBase`, used by `configcmd.go`, `missions.go` and the merge-queue chore) | 22 |
| P8 | in place | the last reshaping inside `cmd/agentbus`, below | ~300 moved within the package |

**P0, the escalation target of the service (a behaviour change, so not part of the move).** On
the base, `serviceDispatchConfig` (servicecmd.go 720) builds a local `cfg`, hands `&cfg.EscalateTo`
to `realHealthConfig` (744) and `serviceSelfRepair` (746), and returns `cfg` by value. Its caller
`RunDispatch` (servicecmd.go 783-792) then sets `cfg.EscalateTo` on the returned copy. So with
`agentbus service run --escalate-to coordinator`, or `AGENT_BUS_ESCALATE_TO=coordinator`, the
loop's own notices go to `coordinator` while the stall checks and self-repair still escalate to
the default `master`. The foreground `agentbus dispatch --escalate-to` is right today, only because
`realHealthConfig` there points into `runDispatch`'s own variable.

The fix: `serviceDispatchConfig` fills a `*dispatch.Config` its caller owns, and `RunDispatch`
sets `EscalateTo` on that same value before `superviseDispatch`. Tests:

- the service path with an escalation target `coordinator`: the stall reader's and self-repair's
  target read `coordinator` (fails on the base);
- the foreground path: `--escalate-to coordinator` given after the health config is built is the
  stall reader's target (it passes on the base; it pins the pointer identity the move must keep).

Mutants: return the config by value again; take the pointer of a copy. Both must fail a test.
The move's P8 and the move PR keep these tests on the `cmd/agentbus` side (rule 3 of §5).

`manifestPath` (roles.go) needs no PR: `rolecfg.ManifestPath` exists; P8 points the moving files at
it if they differ in nothing, and says so if they do.

**P4 is the one prerequisite with weight.** It is two PRs: P4a, the models half (#626), and P4b,
the doctor's rows and the models check. Their CLI halves (`runDoctor`, `runModelsCheck`, the
printing) stay.

**P4b does not go to `rolecfg`, as this table first said: that is an import cycle.** `doctorRows`
calls `ownProfile`, which decodes the project's template into `relaunch.Template`, and `relaunch`
imports `rolecfg`. So `ownProfile`, `tabLaunch` and `launchProfile` go to `relaunch`, beside
`Template`. The doctor's rows and the models check go to a new leaf package, `modelcheck/`, which
imports `rolecfg`, `relaunch`, `models`, `usage` and `dispatch`; none of them imports it. Leaving
package main forces two changes, both decided with master:

- `realDoctorDeps` takes `getenv` and the models settings' `getenv`. They used to be the
  environment and `cmd/agentbus`'s test hook `modelsGetenv`, as in P4a.
- The test hook `launchRisk` becomes a `DoctorDeps` field, so there is no exported mutable variable.

`doctorRow.usedAt` is read by the moving `modelcatalogue.go`, so it becomes `UsedAt` with
`json:"-"`, which keeps the same JSON.

**P8, the reshaping in place, is the last prerequisite and needs P0-P7 merged.** Still inside
`package main`, where every test compiles before and after:

1. The eleven functions of §2.1 are gathered in one new file, unchanged.
2. `serviceDispatchConfig` takes a struct shaped as §3's `Deps` and the caller's
   `*dispatch.Config` (P0 already made it a pointer); `runDispatch` calls a `foregroundConfig`
   function on `&cfg`, before its flag loop, as today; `merge-queue --live` calls a `mergeQueueLive` function; `main`,
   `suspend` and `swap` keep calling `dispatchRuntime`. The four future exports exist under their
   unexported names.
3. `serviceRebootJudge` receives the boot id as an argument instead of calling `runBootID`.
4. `withPopEnd` (quotacmd.go 331, which stays) and `popEnd` (popend.go) are reached through the
   struct's `PopEnd` field only.
5. Every test function is put on its side of the cut (§5). A test file that holds both kinds is
   split in two files, its functions unchanged.
6. The reference graph is run again. **P8 is done when the moving files need nothing from the
   staying ones but the struct's fields, and the staying ones need only the four functions.** The
   PR states both counts.

After P8 the move is `git mv`, a package clause, imports and four capital letters.

## 5. Tests

169 test files, 879 tests in `./cmd/agentbus` on the base. 51 files touch the moving code.

- **37 files move whole**: the 26 named after a moving file whose tests reach nothing else, and 11
  with other names that test only moving code (`loginhold`, `stalewait`, `notifystalewait`,
  `healthchecks_config`, `healthdelivery`, `brokerpersistence`, `holderpace`, `quotathresholds`,
  `rebootselfrepair`, `resumeprofile`, `e2e_holder_restart`).
- **14 files are mixed** and are sorted function by function in P8: `backupschedule`,
  `config_readers`, `e2e_release`, `holderhealth`, `mergequeuecmd`, `modelcatalogue`, `modelscmd`,
  `personguard`, `quotacmd`, `quotacmd_context`, `quotadateformat`, `reload`, `runbudget`,
  `servicehostops`.
- **25 fixtures** are defined in a staying test file and used by a moving test (`backupWorld`,
  `configFixture`, `deliverWorld`, `fakeManager`, `modelsServer`, `withBoot`, `withConfig`,
  `dialMain`, `countingEnv` and their methods).

The rules, in this order:

1. **A test follows the code it tests.**
2. **A fixture used on one side only goes to that side.** Several of the 25 follow their code in
   P1-P7 (`backupWorld` with the archive, the models fixtures with the check).
3. **A test that needs both sides stays in `cmd/agentbus` and goes through the four functions.**
   P8 rewrites its set-up to do so while it still compiles against the old code; its assertions
   are not touched. `e2e_release` is the main one.
4. **No fixture is copied.** A fixture both sides still need after rules 1-3 is listed in P8's PR
   with its users. Architect decides each: a small package under `internal/`, or the test moves.
5. **No export for a test.** If a staying test can only pass with a sixth exported identifier, it
   is named in P8's PR and architect decides.

## 6. The guard test

One test in `internal/dispatchsvc`, in the move PR. It parses the package's non-test files, as
`config/literals_test.go` does for the module, and fails on any of:

1. **An import of the CLI or the screen**: any path under `cmd/`, and `bubbletea`, `lipgloss`,
   `bubbles` (tcell is gone from `go.mod` with remove-tcell; if not, it is on the list too).
2. **An import of `mission`'s runner side.** The package may read mission records; it never calls
   what runs a mission's tests. The test names the forbidden functions (`mission.Run` at least).
3. **`os/exec` in a file that is not on the list.** The list is the files that import it on the day
   of the move (eight on the base: `healthchecks`, `mergequeuechore`, `githubcheck`, `diskcheck`,
   `repocheck`, `plandrift`, `notifychore`, `selfrepairchore`).
4. **A program that is not on the list.** Every call to `exec.Command` or `exec.CommandContext` has,
   as its program, a string literal of the allowed set, or one of the variables the test names with
   the reason each is safe. The allowed set is what the code runs on the day of the move (`git`
   and `docker` on the base, and the probes' programs the coder lists from the code). A call whose
   program the test cannot read fails. Three calls take their program from a parameter on the
   base, and the test names each as a **runner**: `runProbe` (githubcheck.go 153),
   the default `Output` of the disk check (diskcheck.go 35-38), and the `--version` probe
   (healthchecks.go 801). For a runner, the test checks every call site in the package instead:
   the program argument there is a literal of the allowed set, or the test fails.
5. **A shell.** A program that is a shell or runs one (`sh`, `bash`, `dash`, `zsh`, `env`, by
   name or as a path ending in one), whatever its arguments. A shell is never on the allowed set
   of rule 4, so this rule only makes the failure message say why. `-c` is not banned as such:
   `git -c core.fsmonitor=false -c core.pager=cat` (`gitSafe`, plandrift.go 83) is Git
   configuration and is the safe form.

6. **A module import not on the list.** The package's non-test imports from this module
   (`github.com/netbja/agent-bus-monitor/...`) are pinned to the list of the day of the move. A
   new one fails the test, so a new path to a program through another package is reviewed, not
   silent. The list marks the packages that run programs themselves, with what each runs (from
   foureyes's review of r1, on the base; the coder re-reads it on the move's base):

   | Package | Runs |
   |---|---|
   | `quota` | a configured usage reader through `sh -c` (generic.go), the Codex and Claude probes |
   | `backup` | the after-hook (after.go), the secrets helper (secrets.go) |
   | `mergequeue` | `git` and the forge CLIs (forge.go, git.go, delivered.go) |
   | `service` | `systemctl` / `launchctl` |
   | `holder` | the holder and its terminals |
   | `models` | the model readers (readers.go) |
   | `loginexpiry` | the keychain reader (keychain.go) |
   | `rolecfg` | the launch and Grok hooks (launch.go, grokhooks.go) |
   | `notify` | the desktop notifier (`desktopRun`, `runQuiet`, after P6) |

   Adding a package to the list, or a program to what one runs, is a change reviewed against the
   rule "the dispatcher never runs repository code"; the guard's comment says so.

The guard also has positive cases, which must pass: `plandrift.go`'s `gitRead` with `gitSafe`,
the `docker ps` probe, and the `--version` probe whose program is a variable (healthchecks.go 801)
with the reason the test names for it.

The PR shows the test failing five times, on a branch not pushed: with an import of
`cmd/agentbus`, with `exec.Command("make")`, with `exec.Command("sh", "-c", "true")`, and with
`os/exec` imported in a new file, and with a new import of a module package not on the list (`internal/scrub`).

What the guard does not prove, stated in its comment: what the imported packages of rule 6 run
(it pins *which* packages, not their insides; the table above is the review's record), and that
`git` runs no repository code (hooks, `core.fsmonitor`). `plandrift.go` passes `gitSafe`; the other `git` callers do not all pass it.
That is today's behaviour and it is out of scope (§9).

## 7. The move PR

One PR, written in one sitting, inside the quiet window (§8), from the `origin/dev` head of the
moment the window opens.

**Allowed changes, and no other:**

- `git mv` of each moving file and test file to `internal/dispatchsvc/`, whole;
- the `package` clause and the import blocks;
- the four functions and `Deps` take their exported names; their callers in `cmd/agentbus` follow;
- the guard test and a `doc.go` of a few lines (what the package is, the rule of §2.2);
- `docs/architecture.md`: the package in the list; one operator-notes fragment.

**What both reviewers check, and the PR states with its output:**

1. **The same tests exist.** Before, on the window's base:
   `go test ./cmd/agentbus -list '.*'`, the `Test`, `Fuzz`, `Example` and `Benchmark` lines sorted.
   After: the same for `./cmd/agentbus` and `./internal/dispatchsvc`, concatenated and sorted.
   `diff` prints nothing but the guard test's name.
2. **The same tests run.** With `AGENTBUS_TEST_REDIS_URL` on a disposable broker, flushed first:
   `go test -json` before and after gives the same set of passed tests and the same set of skipped
   tests, name by name. A test that passed before and is skipped after is a failure of the move.
3. **The whole suite is green**: `go build ./... && go vet ./...`, `go test ./... -count=1` on the
   disposable broker, every `FAIL` line read, and `go test ./internal/dispatchsvc -count=20`.
4. **The files moved, they were not rewritten.** `git diff -M90% --stat` shows each moving file as
   a rename. The hunks of `git diff -M90%` outside the allowed changes are none.
5. **The CLI did not change.** `agentbus help`, and `agentbus help <cmd>` for `dispatch`, `service`,
   `merge-queue`, `models`, `health`, built before and after, are identical.
6. **The reference graph**: `internal/dispatchsvc` imports nothing under `cmd/`; `cmd/agentbus`
   uses five identifiers of it.

Then, on the installed build, by master: the binaries reinstalled, the holder and the dispatch
service restarted, `agentbus dispatch --status` prints `dispatch up`, `agentbus health` shows every
check with a status, busmon's Services page shows the dispatcher `ready`, and one agent is woken by
a directive. A failure there is a revert of the one commit, not a fix forward.

## 8. The quiet window

The move rebases badly: every edit to a moving file made meanwhile must be redone by hand. So the
move does not rebase. It is done in a window, or done again.

**Before master opens the window:**

- P1-P8 are merged and `dev` is green on the disposable broker.
- No open PR and no branch a coder still works on touches `cmd/agentbus` or the prerequisites'
  destinations. Master lists them:
  `git diff --name-only origin/dev...<branch> -- cmd/agentbus` for each unmerged branch. Each is
  merged first, or its owner agrees to rebase after the move.
- `agentbus quota`: the coder's and both reviewers' providers have room for the sitting. A window
  that would cross a quota reset is not opened.
- The person has said yes to the hour.

**While it is open, master holds:**

- No PR that touches `cmd/agentbus` or `internal/dispatchsvc` is opened, pushed for review or
  merged. Master says so to every agent on the bus, and the person is asked to merge nothing of
  the kind (an ask in For you).
- Work elsewhere goes on: `cmd/busmon`, `holder/`, `bus/`, documents, specs.
- One coder does the move. Both reviewers review it as soon as it is challenged, before anything
  else in their queue.
- No release, no promotion of `main`.

**It closes** when the PR is merged and the live check of §7 has passed, or when it is abandoned.

**Abandon, do not rebase.** If `dev` must take a fix in `cmd/agentbus` while the window is open, or
the PR is not approved in the sitting, the branch is dropped and the move is done again later from
the new head. After P8 the move is mechanical; doing it twice costs an hour.

**After it closes**, an older branch that touched a moved file rebases through Git's rename
detection, since each file moved whole. Its owner runs the full suite after the rebase.

## 9. Out of scope

- Any change to what a check, a chore or the judge does.
- Routing the direct `os.Getenv` reads through `Deps.Getenv`; one `git` runner with `gitSafe` for
  every caller. Both are worth a spec of their own once the wall stands.
- `servicerun.go`, `popend.go`, `hostrun.go` and the team's commands (`up`, `swap`, `suspend`,
  `reload`, `retire`): they stay in `cmd/agentbus`.
- A dispatcher binary of its own, and the host dispatcher (decision 2026-10-26, not built).
- `cmd/busmon`, and the three small items the person also agreed to (one atomic write, the text
  helpers, tcell): separate tasks, no dependency on this spec except `internal/textfmt` in P6.

## 10. Risks

| Risk | What holds it |
|---|---|
| A test stops running, silently | §7 checks 1 and 2: the lists and the pass/skip sets, name by name, on a real test broker |
| Behaviour drifts in a "move" | P8 does the reshaping while everything is one package and every test still compiles; the move PR allows five kinds of change and the diff is read with rename detection |
| A branch in flight loses edits | The window; whole-file moves; abandon instead of rebase |
| The exported surface grows | Five identifiers; a sixth needs architect's yes |
| A pointer into the config dangles after the move | `Config` and `ForegroundConfig` take `*dispatch.Config`; P0's tests pin the escalation target on both paths |
| The guard gives false comfort | It claims only direct exec and the import list (§1, §6); rule 6 pins the module imports, and its table names what each imported package runs; the injected acts are named in §2.2 |
| The installed build breaks | Master's live check, and a one-commit revert |
| P4 is larger than it looks | It may be two PRs; nothing else depends on its order |

## 11. Questions for the person

None. The window's hour is master's to ask when P8 is merged.
