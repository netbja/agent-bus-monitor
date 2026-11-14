- **A project pause is a bus fact, not a sleep.** `agentbus pause --for|--until|--until-reset`
  writes `{p}:pause` (`bus/pause.go`); the dispatcher's `paused` check at the top of each pass
  wakes and escalates nobody while it runs, and once due ends it with `FencedResume` — one script
  that checks the lease, that the record is unchanged and that Redis `TIME` is past the deadline,
  then deletes it and XADDs the resume directive to `notify`. `agentbus resume` (`Bus.Resume`) ends
  it early. A **suspend** (spec `docs/superpowers/specs/2026-11-03-suspend-resume-design.md`, `bus/suspend.go`)
  is a pause of `kind = "suspend"` (additive fields `kind`, `services`, `prev`, `host`, `boot`; `Until` = `at` +
  365 days so an older dispatcher never ends it): `Bus.Suspend` writes it in one script (an existing suspend left
  and returned, a timed pause kept as `prev`, times from Redis `TIME`); `FencedResume` never ends it, `SetPause`
  and `Resume` refuse it (`ErrSuspended`), `FencedQuotaPause` never takes it whatever its reason; `Unsuspend` and
  `SuspendCancel` (compare-and-swap on the raw record) put a still-due `prev` back with no directive, else delete
  it and send the notify target a `human=yes` resume directive. `reserveWake` refuses while any pause exists, in
  its own script after the lease check (`ErrPaused`), so a pass that read the project before a pause reserves
  and types nothing after it; a reservation made before stays visible as `prompting`/`prompted`. `agentbus suspend`
  (`suspend.go`, above the project-required exit like `backup`; the host's projects = templates the broker has, or
  `--project`; the person's act: `AGENT_BUS_AGENT` or a process under a registered agent's verified pid is refused,
  `agentAncestor`, and the shared denies are only the plain `agentbus suspend*` / `agentbus resume --all*`, since
  a `agentbus * suspend*` form would deny every message mentioning a suspend): per project under `upLock`, refused
  while a swap/reload/retire intent is held, then `Services` read and `Bus.Suspend`, the dispatch service stopped at
  once, the quiet rule (`suspendquiet.go`, pure: dormant and proved-gone agents ignored; a wake record `prompting`
  busy at any age until it leaves that state or the dispatcher is proved gone, then busy until a non-working
  declaration later than that boundary or two ready own inspections at or after it plus 5 s; else `EffectiveState`
  on the suspend's own inspections, and a busy pane; quiet = two rounds in a row, 2 s apart) up to `--wait`
  (`suspend.wait`, Services, 10 min; `--no-wait` = 0 still runs the two rounds). Timeout without `--force`, a
  signal (SIGINT, SIGTERM, SIGHUP) or repeated read errors cancel: the services the record marks active start again
  first, then `SuspendCancel`; a failed start keeps the suspend and says `agentbus resume` (also as a `for=person`
  notice). Then the snapshot's facts, the other services stopped, `<state>/agentbus/<p>/suspend.json` (0600, ids,
  states and counts only, the reason through `health.Redact`; `suspendsnap.go`). A suspended project without a
  current snapshot is completed by a rerun, `Services` never rewritten; `suspend --status` lists them. `agentbus resume` on a
  suspended project (`resume.go`; `Bus.Resume` answers `ErrSuspended`, `pauseDeps.Full` runs it; from a herdr pane with no
  `AGENT_BUS_AGENT` it is routed ahead of the identity guard, `suspendedResumeFirst`, only when the pause reads as a
  suspend): refused to an agent caller like suspend; the project's `upLock` held without waiting from the first read to
  the last write (the nested up does not take it again); the template's agents read first, an unreadable template doing
  nothing; the services the record's `Services` marks active start (else the snapshot's copy, else every
  installed kind), one that cannot be read or started keeping the suspend, `--partial` or not; then `agentbus up <p> --resume --no-dispatch --no-missions` inside the herdr session recorded in the
  services env file (`HERDR_ENV`/`HERDR_SESSION`/`HERDR_SOCKET_PATH` set as `start` does; a session not running is said
  with `herdr --session <s>`, services only, no wait), with `upDeps.KeepEnv`: **a resume keeps the project's env file as
  it is** (escalation target, reload policy and every hand-set `AGENT_BUS_*` key; only the herdr session and socket
  are the caller's), names the keys the caller's environment would have changed, never a value, and installs nothing
  on a file it cannot read (`keptEnv`; a plain `up` still takes the caller's environment); then up to 90 s for every
  required agent (`requiredAgents`: the template's agent tabs plus each non-pop agent the snapshot knows alive at the
  suspend, or with no snapshot each verified non-pop agent of the bus; dormant ones skipped) to have a live verified
  process (`alive`: pid with its recorded start); a missing one keeps the suspend unless `--partial`; `--yes` is taken
  and ignored (busmon passes it); then `Unsuspend` with
  one summary (agents back, missing, pops named with `agent-spawn <role> <p> --resume <session>`, never spawned, what was
  open at the suspend, the person's words), a `for=person` notice when the directive's target is missing, and the
  snapshot set aside as `suspend.<taken_at>.json` (5 kept). `agentbus resume --all [--dry-run] [--partial] [--yes]`
  (`resumeall.go`, above the project-required exit like `suspend`; asks on a terminal, exit 2 without one and without
  `--yes`; an agent caller refused, `refuseAgentCaller`): every project of the host, `resumeOne` taking its `upLock`
  before it reads the pause and holding it through either path (a suspend landing in between never gets the crash path),
  a suspended one through the above, any other through the crash path (`runCrashResume`, §7): its installed stopped services started,
  its dead required agents relaunched by `up --resume` only when its workspace is open in its recorded herdr session (a
  crash never opens a closed workspace: `agentbus up <p> --resume` is named), no pause touched, a summary on stdout
  (what could not be known, the open work the bus holds now, read with `readSnapshotFacts`) and to the escalation
  target as the person only when something was relaunched; `nothing to resume` when all runs. The workspace hook
  (`upHook`, `hookSuspended`; real deps `withSuspendHook`) reads the pause (2 s) and on a suspended project starts no
  service: with `suspend.auto_resume` (Services, Bool, off, `AGENT_BUS_SUSPEND_AUTO_RESUME`) on, and only when the
  record's host is this host and its boot is known and differs from the current one, with a recorded `identity.as`
  and the lock free (else `resume already running`), once per boot and suspend (`auto-resume.json` in the project's
  state dir, boot and the suspend's `at`, read and written under the lock, which is released before the spawn so the detached resume takes it without waiting; a later focus on that boot
  says the auto-resume did not complete and starts nothing; a mark it cannot write starts nothing), it starts `agentbus --project <p> resume --yes` detached
  (`hookResumeArgv`, never `--partial`; `spawnDetachedLog`, output in the project's `suspend.log`) with
  `AGENTBUS_RESUME_HOOK=1`, so every way such a resume leaves the project suspended (template or agents unreadable, a
  service not started, an agent missing, the lift failing: `keep` in `runSuspendedResume`) also writes a `for=person`
  notice; a pause it
  cannot read starts the services as before. busmon (step 4, `sleep.go`): the header reads a suspend as `suspended since HH:MM · by <name> · <reason>`
  (`pauseSeg`); `Z` (`ActSleep`, shell table) asks by the pause's kind, `Suspend <p>? … (up to <suspend.wait>) … y / n`
  or `Resume <p>? … y / n` (`sleepAsk`, suspend.wait read in a command), and `y` runs `agentbus --project <p>
  suspend|resume --yes` as a child (`realSleepRunner`: `agentbusBeside()`, its own session, output appended to the
  project's `suspend.log`, never a service started by busmon), its log's last line read every second as the notice
  until it exits (`sleepNotice`), then the outcome pushed as a notice; a second `Z` while it runs says so; the pause
  popup on a suspend says `suspended since` and its first button `Resume (services, agents)` runs the same resume (the
  draft waits). suspend.wait and suspend.auto_resume are on the C page with the Services keys.
  busmon shows a timed pause in the header and in the state column (v0.14.4, `stateLook`: an agent
  that declared working or idle reads `paused`, or `quota` when its provider is held; blocked,
  waiting and done are never covered; the detail keeps the declared state, the panel counts what
  is shown); `@all` targets only agents in `{p}:agents`
  (`agentState.registered`) and a human fan-out within a second reads as one `@all (N)` line
  (`sameFanOut`, display only: one entry per agent stays on the bus).
