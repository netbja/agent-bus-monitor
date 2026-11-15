- **Quotas (v0.13): first-party readers behind one contract, policy in the dispatcher.**
  Package `quota/`: `Reading` (provider, windows: used %, reset, minutes; observed_at vs
  collected_at; unknown/unlimited) and readers — Claude first via the CLI's `get_usage` control
  request (claude-usage-probe, `quota/claudeprobe.go`, `quota.claude.probe`, on by default: `claude -p`
  in stream-json from an empty temp dir, `--setting-sources local --strict-mcp-config`, hooks off, no
  session kept, the account's `CLAUDE_CONFIG_DIR`, 20 s bound, process group killed after the answer;
  at most once per 5 min per account, a failure cached too, in `~/.cache/agentbus/quota-claude-usage-<hash>.json`;
  `five_hour`/`seven_day` → `5h`/`7d`, `weekly_scoped` → `7d-<model>`; Source `cli-usage`; the SDK
  calls the request experimental, so `rate_limits_available: false`, an unknown answer or a failure
  falls back, and per window the newer observation wins), then via `agentbus statusline` (Claude Code's
  `statusLine` stdin `rate_limits` merged into `~/.cache/agentbus/quota-claude.json`, plus a
  transcript backstop for a live rejection), Codex via `codex app-server` `account/rateLimits/read`
  (session logs as fallback), declarative file readers (Grok's billing log line is one, built in),
  an external command, unlimited; configured in `~/.config/agentbus/quota.toml`. No reader opens a
  credential. Log backstops (kimi-quota, `quota/kimi.go`, `quota/agy.go`): Kimi Code (`moonshot`)
  from `~/.kimi-code/sessions/*/*/logs/kimi-code.log` only (8 newest, 7 days, 256 KiB tail): a
  `5-hour`/`weekly` usage-limit hit is that window at 100 %, voided by any later `llm response`
  (`AvailableAt`); Kimi states no reset, so it is the honest upper bound newest hit (after the last answer)
  + the window's length, and a `billing cycle` hit (no length) stays unknown. agy (`google`) from
  `~/.gemini/antigravity-cli/log/cli-<date>_<time>.log` (glog, year from the name): `RESOURCE_EXHAUSTED
  (code 429) … Resets in <d>` holds until that stated reset; agy logs no proof of an answer, so
  nothing voids it earlier, and the quota may be per model: `kind = "off"` is the escape. The dispatch service (`serviceQuota` → `dispatch.QuotaConfig`) collects every minute,
  publishes `{p}:budget` (`windows` etc., additive) and acts in `dispatch/quota.go`: warn once per
  window and reset, hold a provider's agents (`{p}:quota` marks; no prompt, no escalation; quota-repeat: a reset read within
  60 s of a mark's is the same window, `quota.SameReset`, since OpenAI reports one window's reset a second apart from
  reading to reading, the stored value kept; the warn, hold, held-window and long-hold marks, the statusline merge and busmon's held-window mark and pace
  sample use it), pause
  for the coordinator's provider (`FencedQuotaPause`, never replacing another pause). Agents carry
  `client`/`provider` from the launcher's env. Unknown or aged (>30 min) readings never act; every
  write is fenced. A hold ends only when the window that set it is read under the threshold, renewed or past its reset (v0.15.3: an aged reading of it is not set aside for another, lower window). A limit hit read from a transcript is void once the API has answered since
  (v0.14.6: `servedAt`, `Reading.AvailableAt`): with no window to read, a recent answer ends the
  hold and the coordinator's quota pause at once (a reset granted early), and `FencedQuotaUnpause`
  sends master the resume directive like `FencedResume`. v0.15: when a hold ends (released,
  served again or reset passed), the provider's agents with an `accepted` board task or a declared
  `working` get a resume directive, and the escalation target one `wake=no` notice, each once per
  episode (`FencedCmdOnce`/`FencedNotifyOnce`, `bus/once.go`: entry and `resumed:<p>:<agent>` mark
  in one script; the episode is the hold's reset; written before the hold mark is deleted). The
  dispatcher's own quota pause is reconciled even when nobody uses its provider any more: if the
  coordinator now runs on a free provider it ends (`FencedQuotaUnpauseWhy`), on a held one it is
  replaced (`FencedQuotaRepause`, exact record, no resume). A hold whose reset is more than
  `LongHold` away (2 h, `AGENT_BUS_QUOTA_LONG_HOLD`, `0` = off) sends one message per episode
  (`longhold:<p>`): a directive to master naming the held agents with work in flight (master asks
  the person, then `agentbus swap`), or, for master's own provider, a `notify` for the person
  (busmon's pause segment adds "long pause, see Activity"). **`agentbus swap <role> --profile
  <p> [--now] [--force]`** (`cmd/agentbus/swap.go`) replaces an agent by its role on one of its
  `fallback_profiles` (`rolecfg`: schema 2, defined, not its own profile, another provider, at
  most 3; `agentbus roles fallbacks <role>`): everything is checked before anything is closed
  (profile, client on PATH, live verified identity, not its own pane, no open challenge on either
  side — `Bus.ReviewsOf` scans every gate — no mission attempt in progress, no running turn
  without `--now`), then the swap intent `{p}:swap:<role>` (`SET NX PX 180000`; the dispatcher
  neither prompts nor escalates nor resumes that agent), `herdr pane close`, the old pid gone,
  `agent-spawn <role> <p> --profile <p>` with `AGENT_LAUNCH_REQUIRE_BIND=1` (a failed bind is
  fatal), the proof (new pid alive, new pane, the profile's client and provider), and only then
  the resume of its work (`Bus.CmdOnce` on the episode's `resumed:` mark when its provider is
  held). A failure after the close prints the recovery (`agent-spawn <role> <project>`). busmon
  flags `swapping`. v0.15.1 (`bus/swaplog.go`): every swap is in the ledger `{p}:swaps` (`begin` in
  the same script as the intent, `done`/`back` with the fallback state `{p}:fallback` at the proof,
  `failed`/`warning` on every failure path, a `begin` without end and without intent read as
  interrupted and closed by the next swap); `swap --back` returns a role to its own profile and
  model (refused only when profile, client, provider and launched model all match); `agentbus
  retire <role>` closes one role's tab with the same checks (not `shutdown`, which is team-wide;
  pop-auto-retire, spec `docs/superpowers/specs/2026-11-03-pop-auto-retire-design.md`: the dispatch
  pass `popRetirePass` judges every pop (`AgentSnapshot.Tier` from the launcher's `AGENT_BUS_TIER`,
  set only with a proof) with the pure `dispatch.PopDone` over `ReadPopFacts`. Since pop-retire-auto
  (decision 2026-11-13-dispatcher-ends-done-pop) the dispatcher ends a done pop itself where the service
  reaches herdr (`withPopEnd`: a session named in the env, `relaunch.ServiceHerdr`): `popEnd`, the
  steps of `retire --forget` with every write fenced (`BeginAutoRetire`, `AutoRetireHeld`,
  `EndAutoRetire`, `FencedLogSwap`, `FencedRetireSwap`, `ForgetIfClearFenced`), by `dispatch`, never a
  boot role or a running turn; its herdr pane close is bounded (10 s, `serviceClosePane` over
  `relaunch.ServiceHerdrContext`, the herdr process group killed at the deadline), so a hung herdr
  never holds the pass; master gets one wake=no notice once ended (`PopEndedText`), the waking
  directive with the reason once it failed after the close (`PopEndFailedText`), nothing on a wait
  (a turn, a wake being typed, a re-judgement that no longer holds: the next pass judges again); one
  per episode (mark `popdone:<a>`). Without a herdr session it sends master one waking directive per
  idle episode (mark `popdone:<a>` = the declaration's `ts`); master runs
  `retire <role> --forget --episode <ms>`, which re-judges the whole predicate before its `begin`,
  after it and before the close, holds a `kind=retire` intent with a nonce (only that exact intent
  passes), then `Bus.ForgetIfClear`: one script that unregisters the pop only if the stored
  registration is still that pid, start and episode and nothing waits (pending, unread with no
  floor, open ask, owned board task not `done`, gate), and keeps its consumer group so a respawn
  reads on; busmon drops a row whose agent was registered and has left `{p}:agents` (unless an armed
  subscriber still listens), so a retired pop leaves the Agents panel at the next poll instead of
  showing its last state until it turns dormant, and the dispatcher's rule 6 stays quiet (no "no
  snapshot, escalating") for a role whose last ledger entry is a retire that forgot it
  (`Bus.RetiredForgotten`): its kept group waits for a respawn (retire-forget-roster); `dispatch.pop_retire`/`pop_retire_after` (on, 15 min), busmon `done Nm`/`retiring`,
  health `pop-done`; pop-closed-by-hand: a plain `retire` of a pop ends with the same `ForgetIfClear`
  under a retire intent (a boot role never), and a pop closed by hand, its verified process proved gone
  (`dispatch.ProcGone`: only `ErrNoProcess`, a pid readable `/proc` lists no more, or a recycled pid; any
  other read error proves nothing, so macOS never) and herdr knowing its pane no more (`PopFacts.Closed`), is
  judged by the rest of `PopDone` (not its state, hand-off or pane; its delay still) whatever it declared,
  master told with `PopClosedText`, which names a pending hand-off's report to read first, and `retire
  --forget` accepts it with nothing to close; an unreadable process refuses either retire);
  `agentbus swaps` lists the ledger (a reload's entries tagged `[reload]`). Lifecycle-3a: a reload
  (`agentbus reload`, part C) holds the same intent with `kind=reload` (`SwapIntent.Kind`,
  `SwapKindReload`), so every swap rule of the dispatcher applies unchanged; its `begin` carries the
  kind and is refused when the role has no `{p}:agents` snapshot (`ErrNoSnapshot`, in the intent's
  script: a deleted project is never revived, and tab mode, which has no services marker, still
  reloads), it ends with `reloaded` (`FinishReload`, never the fallback state), an interrupted one is
  closed as a failed reload (`closeInterrupted`), and busmon flags it `reloading` (`SwapIntents`).
  Self-repair step 2 (`bus/selfrepair.go`): the watcher's intent is the same key with
  `kind=self-repair` and a nonce, fenced by the dispatch lease token (`BeginSelfRepair`, refused
  `ErrNotHolder` without the lease, `ErrNoSnapshot` for a role with no snapshot, `ErrSwapInProgress`
  next to any intent); `SelfRepairHeld` re-checks nonce and lease before the split; `EndSelfRepair`
  deletes it only for the holder and its own nonce (`ErrNotOwnIntent`), and a lost runner's intent
  waits out `SwapTTL`. `up` (and `up --resume`) skips a role whose intent is held, of any kind:
  `coder: skipped (self-repair in progress)`; an unreadable intent is said and not checked, as an
  unreadable `{p}:agents` is.
  Self-repair step 3, the settings (`config/keys.go`, read by `agentbus config`, busmon's C page
  and, since step 6b, the dispatch service at its start): `dev` is development mode, a **top-level** key (`dev = true`, above every
  table; `config.Set` writes such a key after the last top-level one) or `AGENT_BUS_DEV` (1 on,
  0 off, anything else skipped); it only unlocks values, never weakens a check, and busmon's
  header says `dev` while it is on. `dispatch.self_repair` (`AGENT_BUS_SELF_REPAIR`, on | off |
  shadow, default on) has `Key.DevOnly = shadow`: outside dev, from any source, it resolves
  `Invalid` with `Resolved.Why` naming the key (`devChecked`); a file's refusal is also a
  warning of that file at its line (`Resolver.Warnings`). It is also `Key.FailClosed`: a file
  value its rule refuses (`self_repair = false`, a typo) resolves `Invalid` at its line instead
  of falling to the default `on` (foureyes). Read it only through
  `config.SelfRepairMode`, which reads a refused switch as `off`: `Get`'s value for an invalid
  key is the default, `on`. The budgets `dispatch.self_repair_every` (30m) and
  `dispatch.self_repair_per_day` (3) take only values above 0 (an env 0 is skipped).
  Self-repair step 6b, the shadow (`cmd/agentbus/selfrepairchore.go`, `dispatch/selfrepairchore.go`):
  the dispatch service reads the switch once at its start (`serviceSelfRepair`). Only `shadow`
  runs a pass, every 15 s while it holds the lease: the template's agent tabs, a death counted
  per episode (`pid:start`) from its first read (the grace is Redis `TIME`), herdr read only when
  one is dead (`relaunch.ServiceHerdr`, then `FindWorkspace`, `WorkspacePanes`, `JudgeTab`), then
  `selfrepair.Decide`, and one fenced `would-relaunch` per death (`LogWouldRelaunch`). Health
  `self-repair` is registered in every mode, the service's only (a foreground `agentbus dispatch`
  has no services env to reach herdr): `off` and `on` say so; shadow reports its last pass,
  unknown past 2 min without one. A would-relaunch is not an end: `Interrupted` looks past it.
  Self-repair step 7a, what acting needs: a self-repair ends with
  `FinishSelfRepair` (`relaunched`, the proof, or `failed`, kind `self-repair`), its entry and the
  end of its own intent in one script fenced on the lease and the nonce; an intent already expired
  still gets the entry. The budget is its own record, `{p}:selfrepair` (a record key: a crash loop
  must outlive a restore), never the ledger, whose reads see only its last 1000 entries
  (reviewer-codex): `attempts:<role>` (each begin's Redis `TIME`, appended by `BeginSelfRepair`'s
  script), `failed:<role>` (a failed finish's episode, appended by `FinishSelfRepair`'s: no retry
  for it, the newest 64 kept). Attempts are dropped by age, never by a count below a day's budget
  (`self_repair_per_day` takes any positive number, reviewer-codex): the begin's script keeps those
  of the last 24 h by Redis `TIME`, at most 4096 (a role can make 720 a day, one per 2-min project
  pause). And `crashloop:<role>` (`MarkCrashLoop`, fenced, set once
  so the escalation is sent once); `SelfRepairPast` reads them. `agentbus self-repair reset <agent>
  [--as <person>]` (`ResetSelfRepair`) deletes the three for a registered agent. Only master or a
  person runs it: the caller is the registered agent one of its ancestor processes is (verified pid
  and start), else the agent `AGENT_BUS_AGENT` names, and any other agent is refused, `--as` or not;
  with no agent, `--as` names the person (unproved, as reload's). `relaunch.Options.Fence`
  (`Deps.Fence` for `Role`) is read right before an agent's split or tab create, after its tab is
  judged: an error relaunches nothing for that role (`coder: not relaunched (…)`); the dry run
  never reads it, and `up` and `reload` set none.
  Self-repair step 7b, on mode acts (`cmd/agentbus/selfrepairact.go`): `on` (the default) runs the pass
  as shadow does, then applies the one decision it allows (master first, then the oldest death). A
  relaunch takes the role's intent (`BeginSelfRepair`, a fresh nonce; an intent already held skips it),
  runs the service entry (`relaunch.ServiceRole`: herdr on the services' env file's session, up's project
  lock, the template's tab) with the fence `SelfRepairHeld` read right before the split, then proves it
  for 60 s (a new pid alive by its start time, in a new pane, on the role's own profile's client and
  provider: reload's `replaced`), and ends it with `FinishSelfRepair`. Proved: `relaunched`, and one line
  to master (`self-repair: coder relaunched (pid A -> B, pane P -> Q, resumed <8 chars>), it was dead
  70s.`). The proof decides, the relaunch's error or not (`Role` can err after the new agent came up,
  its old pane not closed). Not proved (its tab never settled, herdr refused, the fence, no process): `failed`,
  one escalation to master with the recovery line (`agent-launch <role> <p> --resume <id>`), the desktop
  rung, and no retry for that death (`Decide` reads it `failed`). A spent day marks the crash loop
  (`MarkCrashLoop`): master told and the desktop rung once, nothing more until `agentbus self-repair
  reset`. A report (an optional role's missing tab, a client without a verified resume, no proved
  session) goes to master once per death, never acted on. Every message is `FencedCmdOnce` (mark
  `selfrepair-done|failed|budget|report:<role>`), and is sent until it is written (reviewer-codex): a
  relaunch's or a failure's message is recorded by its finish's own script (`FinishSelfRepairOwing`,
  `owed:<role>:<ledger id>` in `{p}:selfrepair`, one field per end, its own ledger entry's id, so a later
  end, of another death or of the same one after a reset, never replaces one still owed), delivered at each
  pass, oldest first, once per end (the ledger id is the send's episode), and acknowledged (`AckSelfRepairOwed`, fenced, only
  while unchanged) once written or found already written; of any age, a reset keeps it, and
  master being swapped holds it back without counting it written. A crash loop's is sent each pass while
  its mark stands (episode the mark's Redis `TIME`, `SelfRepairRecord.CrashLoopAt`). Only the send that
  writes rings the desktop. The pass runs the relaunch in its
  own goroutine (the dispatch chore), so a minute's proof never delays a wake. The death count stays in
  the service's memory: a dispatcher restart restarts a death's grace (conservative). busmon's row line
  (`relaunched by self-repair HH:MM`) is not built yet.
  `services.start_at_login` (off, the machine's file only; step 9, `service/login.go`): `agentbus up`
  writes its units for it and enables them after the install (`LoginStart.Enable`), or, off, disables
  an enabled project **before** the install (systemd disables by the `[Install]` section the new unit
  would lose), and one whose state `is-enabled` cannot say is disabled anyway; a disable that fails
  stops `up` before any unit is rewritten (`is-enabled` exits non-zero for `disabled`: its output is read); `agentbus service login` applies it to every installed project. systemd: the
  `[Install] WantedBy=default.target` suffix plus `systemctl --user enable` (never `--now`, never
  linger); `service remove` disables a unit `is-enabled` says starts at login, or cannot read. A project counts as
  enabled when ANY unit starts at login, so the option going off disables every unit (a half-failed enable, a unit
  disabled by hand). launchd: a copy of the job's plist in
  `~/Library/LaunchAgents`, kept in step by every install; a file there agentbus did not write
  (without the "Written by agentbus up" mark) is never changed or removed. No backend: `up` warns,
  `service login` exits 1 with "no service manager to start at login". The C page shows the five in Services (`servicesOnPage`), offers `shadow` only in dev
  (`settingRow.Choices`), and keeps a refused `shadow` line editable.
  Lifecycle-3b, `agentbus reload <role> [--fresh] [--dry-run] [--reason] [--as <person>]`
  (`cmd/agentbus/reload.go`): who may (sentinel any role but itself on an observed update, master
  last and only with no other pilot driver; master only sentinel; a person anyone by an explicit
  `--as`, which also passes the identity guard), `AGENT_BUS_RELOAD`, the registry's `sentinel.reload` (auto-reload, `service.ReloadPolicy`: caller's env, else the
  project's env file, per project so above the machine's file, else `config.toml`, else `on`; an unknown env value reads on, as always; `up --reload on|propose|off`; `propose` and `off` leave only a person's `--as`), a preflight that changes nothing (verified live
  identity, not its own pane, not on a fallback, `reloadOpenWork` = swap's `openWork` plus an
  `accepted` board task and, only with `--fresh`, its own open ask (a resume keeps the conversation and
  the ask is in Redis), declared idle, no wake typing, a proved Claude Code session unless `--fresh`,
  and `planRelaunch` before the kill: inside herdr, the workspace open, one template tab, or for a pop
  (`tier = "pop"`) agent-spawn and the caller in the project's workspace; a pop comes back through
  `agent-spawn <role> <p> [--resume <id>]` with the bind required (`spawnPop`), as swap does), the intent and a second readiness read, SIGTERM to the re-checked pid (never
  SIGKILL), `relaunchRole` (up's lock and relaunch, the role's tab only, resuming `IdentSession`; after the
  exit it re-reads the tab, bounded 20 s, until `judgeTab` — up's own rule — reads a relaunch, since herdr's
  view lags the exit, and a tab the relaunch skips is an error, not a minute of waiting for proof),
  the proof (`replaced`, plus after an update episode a `client_fp` hashing to the installed
  version), `reloaded`; every failure after the intent is a `failed` ledger entry with its
  recovery. Tool rules: sentinel `agentbus reload *`, master `agentbus reload sentinel`, and a shared
  deny on `agentbus reload *--as*` / `agentbus * reload *--as*` for every role (busmon-reload-deny),
  so an agent cannot skip the caller table or the `off`/`propose` opt-outs by naming a person.
  Defence in depth, not a boundary: the client matches command text (another spelling, a wrapper or
  a script runs it), and a person's own shell has no such rule.
  A fallback record keeps `held_episode`, and the release of that
  episode earns master one `back:<p>:<role>` notice; its own provider stays watched. The launched
  model (`AGENT_BUS_MODEL`) is part of the verified identity (`AgentSnapshot.Model`, set only with a
  proof). `agentbus roles doctor [--publish]` compares configured (roles.toml, the current profile),
  launched (bind, else the live process's `--model`) and used (Claude transcript, `usage.ReadUsedModel`)
  models; `--publish` leaves verdicts in `{p}:doctor` for busmon's `model≠` flag (only for the judged
  pid). Claude aliases (`opus`, `sonnet`, `haiku`) are the default in roles.toml; an alias launched as
  an id of its family is never drift. busmon flags `fallback <profile>`. v0.15.2, package `models/`:
  the machine's model table (`~/.cache/agentbus/models.json`, `agentbus models [--refresh]`, no project,
  no broker) read from each client's own free catalogue (Codex `$CODEX_HOME/models_cache.json` with
  its priority, visibility and `upgrade`; Claude Code's aliases plus the model keys of `.claude.json`,
  a *partial* catalogue, so an id missing from it is never `gone`; `grok models`; Kimi's
  `config.toml`; `agy models`, whose other-maker rows carry `family`). No reader opens a credential.
  Tiers (frontier, workhorse, fast, legacy) come from the sources' own words, else
  `~/.config/agentbus/models.toml` rules; a failed read keeps the last good rows, and only a
  successful read under 7 days judges. A schema-2 profile may set `tier` instead of `model`
  (never both): the launch resolves it (`rolecfg.ResolveTier`: the source's default row of that tier,
  else the first in source order) and exports `AGENT_BUS_MODEL_TIER`. The doctor adds `gone` and
  `newer` (only on stated evidence: `upgrade`, generation words, a models.toml family order).
  setup.sh refreshes the table at the end of an install (`AGENTBUS_SETUP_NO_MODELS=1` skips it; the
  installer tests set it); the sentinel refreshes it daily. busmon shows a gauge per provider and `held`. `refresh` no longer reads ccstatusline
  nor writes `{p}:budget` (the service replaces that hash as a whole).
  Model detection (spec 2026-11-08-model-detection-design.md): the table remembers (`seen`,
  `events` capped at 200, `aliases` per provider, `source_at`, `clients`); `agentbus models
  --changes [--since 7d]` lists the events newest first; `agentbus models check [--json]` judges
  every roles.toml profile and fallback, used or not (`ok`, `gone`, `newer`, `newer?` = a listed
  higher version not stated as a successor and never a failure, `not listed (partial)`,
  `untiered`, `unknown`; exit 3 on gone, newer, untiered); `roles doctor --agent <name>` shows one
  row. Under a role that pops (master), a `pops:` line lists the agent-spawn rules its session starts
  with (`rolecfg.SpawnRules`, from `LaunchRules`): `allow` roles pop without a prompt, `ask` roles with
  the person's yes (`approval = "person"`); a role in neither (not in roles.toml) is not covered, its
  spawn prompts. A role that pops with no other role to pop shows `pops: none`. `--json` has them as
  `pops.allow` / `pops.ask` (empty lists for `none`, no `pops` for a role that does not pop). Any `agentbus models` form refreshes a table whose last attempt is 24 h old first: under
  the table's lock (skipped when held), 10 s in all, a late reader keeping its rows and named in
  `refresh incomplete: <provider> <reason>`. The daily refresh no longer waits for services. A
  source older than `models.source_max_age` (7 d) withholds every verdict. The `model-catalogue`
  health check (hourly, handler sentinel) warns on a configured model gone, newer or newer?, an
  old source (with how to refresh it) or a read failing for more than 2 days, for the providers a
  profile uses; ok, its detail is the last 24 h's news (`new: xai grok-4.7 (2h) · anthropic opus
  now claude-opus-5-5`). The doctor task records Claude alias moves from fresh transcripts; a
  client update forces its provider's refresh once per installed version. An adapter may list its
  models (`models = [argv]` in `[adapters.<client>]`, `id<TAB>label` lines, docs/AGENT-PROFILES.md):
  only `agentbus models` (refresh, auto-refresh, check), run by a person or an agent, reads it.
  The dispatcher's model readers (the model-catalogue check, the daily refresh) are the built-in
  clients only (`dispatcherModelReaders`, `TestDispatcherReadsNoAdapter`): the argv is relative to
  roles.toml in the repository, and the dispatcher never runs repository code. An adapter's old
  source is named with `agentbus models --refresh` as its hint.
  Nothing writes roles.toml on its own (`TestNoProfileWriter`; `roles adopt` is the person's act). Kimi
  (the person, §6 Q3/Q5): every Kimi id is one tier (`frontier`, highspeed not told apart) and a tier
  profile on moonshot gets `kimi-code/kimi-for-coding` (`models.FamilyDefault`, before Kimi's own
  default); Kimi's `source_at` is the daily read's time, not `config.toml`'s mtime (`Env.Stat` is gone). The
  chain on a `model-catalogue` warn is in the skills: sentinel reports each finding once to master
  (its de-duplication key `profile|field|model|verdict|candidate` lives in its health note, rewritten
  each daily review with the keys still present, so a finding that returns is reported again),
  master turns it into an ask with the diff (`gone` on a role in use first, as urgent), and on a yes
  coder changes the line as a reviewed step. Agents read models from `agentbus models --json`, never
  from memory (AGENT-BUS-GUIDE, "Which models exist").
