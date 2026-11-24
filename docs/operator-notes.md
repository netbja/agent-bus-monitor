# Operator notes

The "Things that bite" section of [CLAUDE.md](../CLAUDE.md), moved here unchanged on 2026-11-06
(claude-md-split). The architecture is in [architecture.md](architecture.md). This file is
assembled at each release from [operator-notes.d/](operator-notes.d/) (`agentbus mem notes`): a
change adds its own fragment there, never an edit here; the newest notes are in the fragments.

### Things that bite

- **The conductor is the only process that runs repository code, and the only writer of mission
  state.** `agentbus missions` runs the missions' test commands (arbitrary code from agents' commits,
  as any CI does) and every write to `{p}:change:*`/`{p}:missions:*` goes through a fenced script in
  `bus/conductorkeys.go`; the CLI (`agentbus mission load|retry|reassign|skip|abort`) only queues operations on
  `{p}:missionops` and waits for the conductor's answer. Never add an unfenced mission write, and
  never let the dispatcher run code. The conductor writes only to `change/<change>` (validated at
  load) and never pushes or touches `main`.
- **The role scripts run `agentbus roles`, not Python.** `scripts/lib/roles.sh`, `agent-launch`,
  `link-role-skills.sh` and `bootstrap`'s template call `${AGENTBUS_BIN:-$REPO/agentbus} roles …`
  (package `rolecfg/`: manifest, launch, skills, template — the port of the removed
  `role_config.py`; its block in `main()` sits above the project-required exit, like `mem`). The
  repo binary must be built first (`./setup.sh install`); `tests/run.sh` and `tests/lib.sh` build one.
  `agent-launch` `exec`s `agentbus`, which binds and then `syscall.Exec`s the client: the pid
  never changes, which is what `bind` relies on. Outputs are kept byte-identical to the Python
  (dry runs, adapter spec JSON via `pyJSON`, template); `tests/roles_scripts_test.sh` covers the
  scripts end to end.
  `coder2` (role-coder2, the person 2026-11-08) is a pop role, `agent-spawn coder2 <p>`: coder's
  profile, skills and deny on `deliver`, held equal by `rolecfg/coder2_test.go`; `roles/coder2.md`
  adds the rules for two implementers (its own worktree per task from `origin/main`, never the
  shared tree, never a package another task has in flight: read the board, the requests and the
  worktrees first). Nothing new enforces the split: the board's claim refuses a task another owner
  holds and only a request's target accepts it (`bus/twocoders_test.go` names both coders,
  concurrent claims included). `roles reviewers` still reads coder's provider: coder2 shares it.
  Specialist roles (spec `docs/superpowers/specs/2026-10-21-specialist-roles-design.md`):
  `roles.toml` is schema 2 since then (profiles; `rolecfg/migration_test.go` keeps every launch
  equal to the last schema-1 manifest, `rolecfg/testdata/roles.v1.toml`);
  `examples/roles.specialists.toml` holds six pop roles with prompts in `roles/`. Verbs:
  `roles reviewers` (models of other providers than coder's that can review; never the author's
  own maker served elsewhere), `roles skill-users <skill>...` (whom to tell when skills change),
  `roles draft <role> --task …` (an inert draft in `.agent-bus/drafts`, skills from the launcher's
  roots, least privileged) and `roles adopt <role> --ask <id>` (needs the project's bus, like
  `doctor`: only after an `adopt` answer not given by the asker; appends to the manifest after
  checking it loads, never overwrites a prompt). `infra` is a pop role (Ansible, Terraform/OpenTofu,
  IT automation), adopted (#187) from the reviewed draft `examples/drafts/infra/` with `roles adopt infra
  --ask <id> --from examples/drafts`: manual profile, approval person; allowed only exact syntax/lint invocations, no wildcard
  (`terraform|tofu fmt -check [-recursive]`, `validate`, `tflint [--recursive]`, `ansible-lint
  --offline`, `yamllint .`); everything else prompts in the pane, and asked as well: what evaluates
  providers or data sources, touches a target or downloads (`plan`, `init`, `refresh`, `get`, `test`, `ansible-playbook --check`/`--diff`,
  ad-hoc `ansible`, `ansible-pull`, `ansible-galaxy`, `tflint --init`) and every change (apply,
  destroy, import, taint, state surgery, force-unlock, workspace) and every `ansible-playbook` run,
  one `terraform *<verb>*` pattern per tool
  so `-chdir=` forms match; denied: state, plan
  and tfvars files, vault passwords, cloud credentials, `output`/`show`/`console`/`state pull|show`,
  `env` (`rolecfg/infra_test.go`, a command table per class; it also holds the manifest to the
  draft's entry byte for byte and `roles/infra.md` to the draft's prompt). A role popped only through
  the person (deploy, infra) is an ask in master's rules; `TestShippedManifestRules` and the
  migration test derive that from `approval`.
- **`setup.sh` is the installer; tests of it must stub `docker` and `herdr`.** `install`,
  `update`, `uninstall`, `status`. Since installer-default it is a thin script that builds
  `agentbus` and runs `agentbus setup` (package `setup/`, every external command through
  `setup.Runner`); the bash installer stays one release as `scripts/setup-legacy.sh`, the fallback
  (`tests/installer_legacy_test.sh` runs the same suite on it), removed in the next, and run only
  when invoked by name (the thin `setup.sh` never execs it); `--force` only warns and is ignored, in
  both installers, and is dropped with the bash one. It stamps `buildinfo.Version` (from
  `git describe` when it is a `v*` tag, else `dev`) and `buildinfo.Repo` via ldflags; `agentbus up`
  finds `scripts/bootstrap` through `AGENTBUS_REPO`, then `buildinfo.Repo`, then the binary's
  directory. A copy of the repository uses the same compose project and container name as the live
  broker, so an unstubbed `uninstall --yes` in a test stops the production Redis.
  `tests/installer_test.sh` puts fakes first on `PATH`; keep it that way. Since v0.20 setup
  links *this checkout* as the herdr plugin `netbja.agentbus` (`herdr plugin link`, the plugin
  root is the checkout, no download; its one `[[build]]` exits 1 with how to install, so
  `herdr plugin install` from herdr's plugin list registers nothing, `TestPluginBuildRefusesInstall`,
  plugin-listing); herdr-plus is no longer installed, and a
  herdr-plus an older setup installed is reported by `status`, never removed. setup owns only
  what it wrote: it unlinks only the registration it recorded, and replaces only its own,
  unedited key-binding block.
- **Templates live in agent-bus's own directory (v0.20).** `rolecfg.ProjectsDir`:
  `AGENTBUS_PROJECTS_DIR`, else `$XDG_CONFIG_HOME/agentbus/projects`, else
  `~/.config/agentbus/projects`; bash reaches it through `agentbus roles projects-dir` (handled
  before the manifest is loaded, so it needs no `roles.toml`). There is deliberately **no migration
  code** and no fallback to herdr-plus's directory: old templates are moved by hand, once.
  `tests/lib.sh` points `AGENTBUS_PROJECTS_DIR` at a temp dir, so a suite that forgets it never
  writes the real `~/.config`. `up` takes the herdr session from `--session`, `HERDR_SESSION`, then
  `HERDR_SOCKET_PATH` (`…/sessions/<name>/herdr.sock`, or `…/herdr/herdr.sock` for `default`), and refuses services when a socket path
  names none.
- **The herdr plugin (v0.20): `plugin` and `--pick` sit above the project-required exit.**
  `agentbus plugin projects|busmon|busmon-pane` and `busmon --pick` need no project (they answer
  "which project?"), so their blocks in `main()` stay above that exit, and `--pick` above
  `--reset`/`--delete` too. The picker (`projlist/`: templates in the projects directory x the
  broker's `bus.Projects`, state present/absent/`?`) writes nothing to Redis and **never
  provisions**: it only runs `agentbus up <p>` for a project the broker confirms; an absent one is
  `agentbus up <p> <path>` by hand. Calls to herdr go through `herdrBin`; the plugin's panes get
  `HERDR_SESSION`/`HERDR_SOCKET_PATH`/`HERDR_BIN_PATH` from herdr and the cwd is the plugin root
  (`./agentbus`, `./busmon`). Decision:
  `docs/memory/decisions/2026-10-19-herdr-plugin.md`.
- **Relaunch (v0.21) never types into a pane that already exists.** Whether a shell waits at its
  prompt cannot be proven from outside: a builtin `read` keeps the shell's argv, and `read -e` or
  an earlier `stty -icanon` even leave the terminal non-canonical (Codex's reviews, real PTYs).
  So `agentbus up <p>` on an open workspace (`relaunch.go`: `decideRelaunch`, a pure table) runs a
  dead agent's template command in a **new pane split from its tab** (`splitPane`), or in a new tab
  when the tab is gone. Once the relaunched agent is proven (its verified identity a new live
  pid bound in the new pane, read for up to 60 s), it closes the exact old pane the dead agent's
  snapshot recorded, if a re-read right before shows it still a plain shell; anything else leaves it
  and says why (`closeOldPanes`, decision `docs/memory/decisions/2026-10-27-relaunch-close.md`;
  busmon's tab, when it held one plain-shell pane, has that pane closed with no proof once the new
  pane runs busmon, if it is re-read a plain shell: `isBusmonTab`, `closeIfShell`, `CloseBusmon`,
  start-busmon-pane;
  master may run `agentbus up agent-bus-monitor --resume --dry-run` without a prompt, `roles.toml`;
  since the herdr exit the relaunch itself is the person's: the relay refuses `up` inside an agent's
  terminal). A tab's command counts as gone only when
  no live snapshot says otherwise (`snapLife`: pid + `pid_start`, Linux only), herdr sees no agent in
  any of its panes, and **every** pane of the tab is a plain interactive shell (`idleShell`, argv
  only: a classification, never a licence to type); anything running in any pane (maybe the agent
  relaunched before, under a name herdr does not detect) makes it `skipped (busy)`. Duplicate tab labels are
  `unknown`. The invariant test is `typedIntoExisting`. `upLock` serialises ups per project and
  fails closed. The workspace hook (`agentbus plugin workspace-event`, herdr's
  `workspace.created`/`workspace.focused`, the only `[[events]]` the manifest test allows) touches
  no pane: it only starts stopped services with `Manager.Start`, never `startServices`/`Install`,
  and never provisions (template + broker `present`). After a reboot, agents are herdr's native
  session restore's job (`pane report-agent … -- <resume argv>`, which runs in a shell herdr has
  just spawned). Since v0.21.1 every Claude Code launch carries, in its `--settings`, a
  `SessionStart` hook (`agentbus roles report-resume <agent-launch>`, `cmd/agentbus/reportresume.go`)
  that reports `env ROLES_TOML=… agent-launch <role> <p> [--profile <p>] --resume <id>` with
  `herdr pane report-agent-session --source agentbus --agent claude` from a detached sender (retried
  15 s while herdr answers `resume_not_accepted`, logged to `<state>/resume.log`): no state report, no
  authority, so `herdr:claude`'s raw `claude --resume` stays the fallback when herdr refuses. It also
  hands the new session to a detached `roles prove-session` with its ancestor chain read before it
  detaches (`dispatch.Ancestors`; lifecycle spec part D): `Bus.ProveSession` moves `IdentSession`
  only when the stored verified pid and start are in that chain and the hook's foreground stamp is newer
  than the stored `ident_session_at` (a new process resets it), so the session follows `/clear`
  (Linux and macOS, like every verified identity: `/proc`, or `sysctl kern.proc.pid` since self-repair step 10).
  The sender re-reads the stored identity and that its pid is alive with that start, but trusts the chain:
  a documented residual, since the bus is no boundary against the local user (as with `status`).
  Since herdr exit step 8 the same hook, inside herdr or not, also hands the session, the profile
  (`-` the role's own) and its chain to a detached `roles record-resume`: `Bus.RecordResume` writes
  `{p}:resume` (agent → `{session, profile, at}`, `at` from Redis `TIME`; never a command, security
  finding S1: a consumer rebuilds the argv with `rolecfg.ResumeArgv`, and `parseResume` drops any
  other field, refuses a profile that is no name, and reads a record without its `profile` key as
  none: an old-schema `{argv, session, at}` record would otherwise resume a swapped session under the
  role's own profile, reviewer-codex) only when one ancestor (at most 32) is the stored
  verified process, pid and start alike, in a `WATCH {p}:agents` transaction. The record outlives the
  process (a status, a hand-off, a relaunch leave it); only `forget` clears it, and the next proved
  session replaces it. No chain (a system with neither /proc nor sysctl's process table) records nothing. It is a backup record and a project kind. The hook
  never prints on stdout and always exits 0 (spec
  `docs/superpowers/specs/2026-10-21-launcher-resume-design.md`). Units always run `buildinfo.Bin` (setup's
  `$BIN_DIR/agentbus`) when it exists. `agentbus projects` and `agentbus identity` sit above the
  project-required exit. `missions` without a Git repository publishes `waiting-git` and waits.
  **Trust gate** (`trust.go`, specialist roles spec part B): when `up` opens a workspace whose
  folder a client (Claude Code, Codex) does not trust yet, it starts the first agent of that client
  alone, says to answer the client's own dialog in its terminal (or busmon's For you) and to run `agentbus up`
  again, and returns at once (P1-9, 2026-11-16: it used to poll for 10 min, holding the person's terminal); the
  next `up`, once the trust is recorded, starts that client's other agents and closes the busmon reminder. Its
  message names the terminal to answer in (A10). The readers (`claudeTrusted`, `codexTrusted`) read only the trust flag, keyed
  as Claude Code documents (`trustKey`: git root, a worktree's main checkout, else the folder) and
  return a bool, never file content (`~/.claude.json` can hold an API key). agentbus never writes
  those files. Nil `upDeps.Trusted` = no gate. **Agent CLIs** (installer-no-client): `rolecfg.Clients`
  is the launchable clients (manifest name, executable, name, official install page; in step with
  `builtinBinary`, `TestClientsMatchTheLauncher`); setup's step 5 scans them and, with none, warns
  with each install page (never a failure, nothing installed); `up`'s `checkClients`, right after the
  template is read and before any service or tab, warns about the agent tabs whose executable
  (`tabExecutable`, through the tab's manifest, split with `rolecfg.ShellSplit`) `lookAgentCLI` finds
  neither on PATH, nor in the usual per-user dirs, nor through the person's login shell
  (`shellProbe`: `$SHELL -lic 'command -v "$1"'`, fish's `$argv`, 3 s; only command -v's own non-zero status is
  `errCLIAbsent`, read from its own `AGENTBUS_PROBE_DONE <status>` marker so a shell failing at startup is not a miss; anything else `errCLIUnknown`, counted as found: fail open), and starts every tab
  anyway (master: a lookup alone never drops a tab; a CLI confirmed absent opens no trust gate); only with none of the team's found does it
  refuse (exit 1) with the pointers and `agentbus up <p>`. Nil `TabExe`/`LookPath` = no check.
- **`agentbus start` (spec `docs/superpowers/specs/2026-11-01-agentbus-start-design.md`) is a front door over
  `up`, not a second launcher** (`start.go`, `start_deduce.go`, `start_ask.go`; above the project-required exit, beside
  `up`). `deduceStart` reads the folder (resolved, its Git root, a linked worktree's main checkout: `canonDir`, used for
  templates' `working_dir` too, since bootstrap records the logical path), the project (a template on that folder, else
  `startProjectName` of its base name, `-2`… when another folder's template holds it), the name (`identity.as`, else
  `$USER`) and the team (the template's tabs, else `start.team`, else every boot role). The assistant is plain lines on
  `/dev/tty` (`setup.OpenTTY`); with no terminal and no `--yes` it prints and exits 2. An existing project with
  nothing to settle and no `--name`/`--as`/`--roles` gets a short screen (`shortScreen`, `openState`): one status
  line (agents by `d.Live`, busmon's tab by `busmonVerdict`, inside herdr only) naming what Enter does; `c` or a
  digit switches to the full screen. Its own writes are only
  `identity.as` when none is recorded and `start.team` when the person chose a team; everything else is `up`'s code path
  (`runUp`), with `--roles` for a reduced new team or `new --roles` for a team change, which is refused on a template
  edited by hand (rebuilt with `TemplateTeam` from its own tabs and compared byte for byte) or when it drops or moves an
  agent that is live or unprovable (`agentbus retire` first). Outside herdr with a terminal it runs `herdr --session
  <p>` on it, reads the socket from `herdr session list --json`, and runs `up` with `HERDR_ENV`/`HERDR_SESSION`/
  `HERDR_SOCKET_PATH` set, its output in `<state>/start.log`. herdr's first workspace of a session start created (labelled after the folder,
  one bare shell tab: `initialWorkspace`, read only when `herdr session list` showed the session not running before) is
  ignored by that `up` (`upDeps.IgnoreWorkspace`), so the project's own workspace opens busmon first; once `up` succeeds
  start closes it only if a re-read still shows one tab and one plain shell (`closeInitialWorkspace`, `paneShell`;
  never typed into), the note in `start.log` (start-tabs). A role's own profile in a project is its template tab's
  `--profile` (`ownprofile.go`: `tabLaunch`, `launchProfile` = own or a fallback, `ownProfile`); `tabProfile`, doctor,
  swap and reload read it there, and `up`'s `checkProfiles` stops before anything starts on a profile `roles.toml` no
  longer allows. Tests stub up, herdr and the bus (`startDeps`); none starts a real session.
- **The holder's services (herdr exit steps 6b and 6b2, `service/holder.go`, `cmd/agentbus/holdercmd.go`).** One unit per
  (project, build), never a template and never restarted to upgrade: `agentbus-holder.<p>.<key>.service`
  / launchd `com.agentbus.holder.<p>.<key>`, recording `X-Agentbus-Project`, `-Build` (the
  `BuildKey`) and `-Build-Raw` (plist: `XAgentbus*` keys), found by those values, never by a name
  prefix (`a` never matches `a-b`), and only when they are a `ValidBuildKey` and a valid project and
  the file's name is the one they give (a record of `..` is ignored: gc never removes outside
  `<datadir>/agentbus/holder/<key>`; each level is checked with `Lstat` before the copy is written and
  before it is removed: a symlink is never followed). Each runs its build's binary copy,
  `<datadir>/agentbus/holder/<key>/agentbus` (0700, raw id recorded in `build-raw`); a key recorded
  with another raw id is refused (S7). Restart on failure after 2 s, 5 starts in 300 s,
  `KillMode=control-group` (launchd: `KeepAlive` on failure, `ThrottleInterval` 10). `GCHolders`
  removes a non-current build's unit (systemd `disable`, file, `daemon-reload`; launchd only when
  not loaded, launchd's own "Could not find service"; any other print failure keeps it) and then its binary — only when the instance is inactive and no socket of that build
  answers, never a running or the current one, and never a binary another project's unit records;
  a manager that cannot say keeps the unit. `StartHolder` never restarts or kicks a running one.
  The unit runs `<copy> --project <p> holder --env <the project's env file>` in the project's
  `WorkingDirectory` (6b2): the holder starts every agent, and a service manager's PATH is minimal,
  so `agentbus holder` reads the env file, through an overlay and never into its own environment
  (a variable removed from the file, a secret, must not reach a later agent; reviewer-codex): each
  agent gets the holder's starting environment plus the file's current variables. Its launch context is in that file
  under names only the holder reads, written by `up`: `AGENTBUS_HOLDER_REPO` (the checkout whose
  `scripts/agent-launch` it runs), `AGENTBUS_HOLDER_MANIFEST` (the manifest up's template names) and
  `AGENTBUS_HOLDER_DIR` (the working directory, `~` expanded). Never `AGENTBUS_REPO` or
  `ROLES_TOML`: the env file is every service's, and those would pin the dispatcher too (foureyes).
  Like dispatch's, the file is the project's, not the build's: an old build's holder reads the
  current one. **Each Start reads that context afresh** and gives the new agent the file's
  variables: `up` never restarts a running holder (it would kill its agents), so a changed PATH,
  manifest or directory reaches the next agent while running ones keep theirs; a context that
  cannot be read refuses the Start (reviewer-codex). The settings (`holder.scrollback`,
  `dispatch.submit_wait`, `dispatch.person_quiet`) are read once, at start. An invalid
  configuration exits 0 (not restarted). `agentbus up` copies the running build, installs and
  starts its unit, then runs `GCHolders`; a `go run` build installs none, with a notice. No agent
  runs on the holder before the cutover. Holder units are on every service path (foureyes):
  `service list` shows them (`<p> holder active, build <raw>`); `down` stops every instance of the
  project (`StopHolder`: systemd `stop`, launchd `bootout`); `service remove` (whole project) and
  `remove --all`, hence `setup uninstall`, stop and remove them and the binary copies no other
  project's unit records. A password rotation and an upgrade's hint skip holder lines by kind;
  `service restart holder` is refused unless `--kill-agents` is given and says how many agents it
  would kill (`RestartHolder`: systemd `restart`, launchd `kickstart -k`).
- **The holder's health and its two commands (herdr exit step 6d1).** One evidence per project
  (`internal/dispatchsvc/holderhealth.go`, `holderEvidence`): per build key, its unit and whether the
  manager runs it, its socket in `<rundir>/agentbus/<p>/` (answering, terminals, age from the
  socket's mtime) and its binary copy; a copy that another project's unit records is that project's,
  never this one's leftover; a manager that cannot say makes it unknown. Three checks read it
  (`health/holder.go`): `holder` fails when the current build's unit is installed and no holder
  answers (the person's since holder hardening H3: `agentbus service start holder`, in For you); `holder-draining` warns while an older build still holds terminals (master's,
  never a fail); `holder-leftovers` warns when an older build that neither runs nor answers keeps
  its unit or copy (the person's: `agentbus --project <p> service gc`). `service start holder`
  starts the current build's installed unit only (`StartHolder` never restarts); `service gc` runs
  `GCHolders`, then `GCHolderBinaries` (copies no unit of any project records, kept when a holder
  of that build answers for any project, found through `holder.RunAgentbusDir`, which checks the
  run directory without creating a project's). Deferred to the cutover: `holder_restarted` (no
  agent is tied to a holder terminal before it) and `holder drain` (no agent to drain).
- **busmon's project switch (herdr exit step 7b1; spec `2026-11-14-busmon-project-switch-design.md`).**
  `o` on the Projects page (M) and `/project <name>` (completions: the broker's projects, read once
  at start) call `switchProject`: refused, flash only, for the current project, a project with no
  keys on this broker ("agentbus up <p> first"), and any draft (a typed line, the compose
  window's text or held drafts, an answer being typed). Otherwise the target's keys are read in a
  command and busmon ends (`switchTo`); `main` writes the old project's cache synchronously
  (`finishSwitch`; the model's `saveCache` is a command `tea.Quit` may outrun), then `execFn`
  re-executes busmon with `switchArgv`: rebuilt from the parsed flags (`optPtrs`), never edited as
  text, one-shot flags (`reset delete yes list pick version`) never carried, `--project=<target>`
  last. A failed cache write means no exec; both failures print the command to type and exit 1.
  A switch is also an upgrade (the installed binary is re-executed). **Start order**:
  `--project`, `AGENT_BUS_PROJECT`, then the project last used against this broker
  (`last-project` beside the per-project caches, one line per broker, written at start; the key is
  the connected client's network, address, db and tls, never `-H` text or credentials), skipped
  when it has no keys. Without any, busmon shows the picker's screen in open-here mode
  (`newOpenHerePick`, `pickToOpen`): the same list and the same check on the broker, but Enter
  records the project and busmon re-executes on it (`openPicked`), never `agentbus up` (that stays
  `--pick`'s, reviewer-codex); without a terminal it prints how to name one.
- **Project services (v0.12): the OS manager supervises, nothing of ours does.** `service/`
  writes systemd `--user` units / launchd plists (enabled at login only while `services.start_at_login` is on), the 0600 env file the
  services load, the rotating log and the restart budget. Every unit runs `agentbus --project P
  service run <kind>` (`cmd/agentbus/servicerun.go`), whose exit code *is* the restart rule: 0 is
  final (stopped, project gone, another instance, invalid config, budget spent), 1 restarts. Its
  state goes to `{p}:services` through an `EXISTS`-guarded script (`bus/services.go`); without the
  `installed` marker that `up` writes, a service exits and writes nothing, so a deleted project is
  never resurrected. `up` writes that marker only once the broker answers PING (`waitBroker`,
  up-broker-ready: a 10 s deadline on the clock that counts the pings' own time, each ping through
  `bus.ConnectContext` given at most 2 s and never more than is left, 250 ms between them;
  retrying only LOADING and a refused or reset connection (or EOF): a timeout or another dial
  error fails at once, since a starting local container refuses rather than black-holes; a give-up
  cut short by the budget names the broker's last answer, not the deadline). Don't add a supervisor loop, a `nohup` fallback, or a login autostart other than the
  systemd/launchd option decided on 2026-11-09 (off by default, no linger, not built yet:
  `decisions/2026-11-09-self-repair-pane-writer.md`); don't
  put a secret in a unit; tests use fake `systemctl`/`launchctl` (`tests/installer_test.sh`,
  `service/manager_test.go`) and never the user's manager. busmon only reads the state and the
  local log (`F3`), never starts or parents a service. F3 (busmon-f3, `services_cards.go`) is three cards,
  side by side at ≥110 cells: dispatch and missions (state, pid and since from `/proc`, summary, last
  error, log size of `service.LogMax`, `LogKeep` kept) and Redis (where it runs: `docker inspect
  agent-bus-redis` only when docker is on PATH, else the process from INFO; version, uptime, memory,
  `DBSIZE` keys via `Bus.RedisInfo`), all gathered in a command (`gatherServices`, the records read
  fresh through `ServiceStates`), unknown read `?`; while the Services or Redis page is the top page it
  is read again every `svcRefreshEvery` (5 s, busmon-f3-live: each result schedules the next read, so
  reads never overlap, a page under another keeps its chain without reading, a closed one ends it); ↑↓ choose a service card, Enter or `l` opens its log tail
  (`ovServiceLog`, `serviceLogBody`); the Redis card is selectable too, and Enter opens its read-only page
  (`redis_page.go`, `ovRedis`, busmon-redis-detail: `Bus.RedisDetail` = INFO memory/persistence/clients and
  per project its keys and stream lengths, `docker port` for the loopback bind, `bus.LegacyPasswordInUse`,
  a `docker logs --tail 20` bounded to 8 KiB; unknown read `?`); `busmon --delete` removes the local
  units before the keys.
- **Without a service manager (herdr exit steps 6c1, 6c2 and 6c3: detached supervisors).**
  `services.manager` (`auto`, `systemd`, `launchd`, `none`; `MachineOnly`) is the registry key of the
  old `AGENTBUS_SERVICE_MANAGER`: `service.ManagerChoice` reads the environment, then config.toml,
  and `Detect`, `Local`, `ManagerBin` and `restore` go through it (since 6c3 `none` means detached
  supervisors, below). `service.Supervisor` is the units' policy for a supervisor process: restart after 2 s on a
  non-zero exit, at most 5 starts in 300 s (judged when the next start really happens, after the
  wait: a wait that overran may have freed the window; reviewer-codex), an exit 0 is final, a stop sends the service SIGTERM; `AfterExit` runs after each exit.
  A holder with `Config.Pgids` set rewrites `<rundir>/agentbus/<p>/holder-<key>.pgids`
  (`PgidsPath`, 0600, atomic) with each live terminal's leader pid and start time at every start
  and end; `KillRecordedGroups` takes one cutoff (`NowTicks`, captured by the caller when the holder's exit
  is seen, the same for every group, and short of the tick being read, so nothing born after the
  capture is reached; a survivor started in the last 10-20 ms before the death is missed;
  reviewer-codex) and hands each line to `KillGroup`, which never signals the group number (after a group empties its number may be
  reused; reviewer-codex): it kills, each pinned by a pidfd and re-read through /proc after the
  pin, the processes of that group that started between the recorded leader and the cutoff, so the
  survivors of a gone leader die (foureyes) while a group made after a reuse is spared, and touches
  nothing when a live process holds the leader's pid with another start time. pidfd needs Linux
  5.3: ENOSYS ends the kill with an error; a member that refuses (EPERM, a root child of `sudo`)
  is recorded and skipped, the others still killed (foureyes), and the refusal returned; on any
  error `KillRecordedGroups` keeps the record, the only trace of what is left (reviewer-codex). Linux only
  (`groupkill_linux.go`): without /proc there is no start time, the holder records nothing and
  nothing is killed. It reads the record only from a checked run directory and never through a symlink
  (`O_NOFOLLOW`, regular, ours, private), and removes it.
  6c2: `holder.Supervised` is the holder's `service.HolderManager` without a manager. Its "unit" is a
  record, `<rundir>/agentbus/<p>/holder-<key>.pid` (0600, read like the group record): `InstallHolder`
  writes project, build key, raw id, env file, working directory and binary copy (another raw id for a
  key is refused, S7); `StartHolder`, under a `flock` on `holder-<key>.lock` (two `up` start one; the lock file is never
  removed, even by `RemoveHolder`, so every waiter shares one inode: reviewer-codex),
  spawns `<binary copy> --project <p> service supervise holder <p> <key>` in its own session (`setsid`),
  output appended to `<state>/agentbus/logs/<p>-holder.log`, and records its pid and start time. A
  supervisor is alive only while that pid holds that start time (a reused pid, a zombie: gone);
  `StopHolder` sends SIGTERM pinned by a pidfd and waits (15 s); `RemoveHolder` refuses a live one, so
  `service.GCHolders` works on it unchanged. Linux only for now: elsewhere `ProcStart` must be given
  (no start time, nothing judged). `agentbus service supervise` (`supervisecmd.go`) runs
  `service.Supervisor` on `holder --env <file> --record-groups` (the holder then sets `Config.Pgids`)
  and, after each exit, `KillRecordedGroups` with the cutoff taken first and the run directory the
  holder's env file gives it; it exits 1 when it gave up.
  6c3 wires it: `holder.Supervised` is also a whole `service.Manager` (`Name` `supervisor`), which
  package `holder` registers as `service.Unmanaged` at init, on Linux only (`service` cannot import
  `holder`; elsewhere there is no start-time reader yet, so a macOS `none` keeps the old answer, no
  manager and herdr tabs, as does a binary that does not link `holder`). `Detect` returns it for
  `services.manager = none`, and for `auto` on a system with neither: a Linux not booted with systemd
  (`/run/systemd/system` absent) or another system. A systemd that runs but whose user instance does not
  answer (an ssh session without its bus) stays an error: never a second dispatcher beside the units.
  `up` then installs dispatch and missions as records, one per project like their units
  (`<rundir>/agentbus/<p>/<kind>.pid`, same checks and lock, one `arg` line per argument), and `Start`
  spawns `<exe> --project <p> service supervise <kind> <p>`, which runs the unit's own command
  (`Spec.Argv`, never `--stderr`) under the same policy, killing no group. Every detached supervisor,
  the holder's too, starts with a unit's environment (`unitEnv`: PATH, HOME, user, locale, the XDG
  directories), never the agent or pane variables of the shell that ran `up`. `down`, `service`,
  `suspend`, `restore` and the health checks reach it through `Detect`; `--tabs` still forces herdr tabs.
  `service.Detached` (`Limits`) states what does not exist: `services.start_at_login` is refused with it
  (`LoginStartOf`), and `service status` prints it with each service's supervisor log, which `service
  logs` shows too. `service supervise` calls `holder.UnignoreTermSignals` first (spec revision 26): under
  `nohup`, nothing below inherits an ignored SIGHUP (`TestSuperviseUnderNohup`). busmon links `holder`
  by a blank import in `cmd/busmon/main.go` for this registration alone (its test names the manager
  without importing `holder`, which would link it into the test binary and prove nothing).
- **busmon's update check never runs on a `dev` build** (or with `AGENT_BUS_NO_UPDATE_CHECK=1`):
  `cmd/busmon/update.go` asks GitHub once a day, caches in `~/.cache/agentbus/latest.json`, fails
  silently and only adds a muted `↑ vX.Y.Z` to the header. Don't make it block startup or nag.
- **`"docs": "<agent>"` in missions.json** makes the conductor send that agent one directive when
  the change completes, written with the `complete` notice in one fenced script
  (`Store.PutChangeWithCmds` → `FencedCmdsWithChange`). The docs agent opens a PR from
  `pr/<change>` and never merges.
- **`required = true` on a role** (self-repair step 4, `Manifest.RoleRequired`): a boot role whose
  closed tab the self-repair watcher will recreate (step 7; an optional role's is only reported).
  `Load` refuses it on a pop and refuses any value but a boolean; `roles doctor` shows it (`REQ`).
  A new role key reaches the shipped `roles.toml` only after a release that parses it is
  installed: sessions read the main checkout's `roles.toml` with the installed `agentbus`, whose
  older parser refuses the key ("unknown fields in role …") and stops every launch, relaunch and
  swap. `required` itself waited for that release; since then `roles.toml` and
  `examples/roles.mixed.toml` mark every boot role, which `TestShippedManifestsMarkTheBootRolesRequired`
  checks.
- **The launcher's environment rules** (`rolecfg/config.go`, `launch.go`): `env_refs` may not
  target loader/hook variables (`LD_*`, `DYLD_*`, `HERDR_*`, `BASH_ENV`, `NODE_OPTIONS`, …);
  `ANTHROPIC_API_KEY` is dropped for every client unless `AGENT_LAUNCH_KEEP_API_KEY=1` or
  `env_refs` names it; elevated options are announced on stderr. v0.17 (`rolecfg/toolrules.go`):
  a role with `pops = true` gets `Bash(agent-spawn <role> *)` per other role (allow, or ask when
  that role has `approval = "person"`) plus its own `tool_rules`, in one `--settings` JSON for
  Claude Code only (other clients: a note); free-form rules naming `agent-spawn` or allowing all
  of Bash are refused. The ask matches command text only (a quoted role reaches the classifier):
  a convenience, not a lock. A missing skill warns and the
  agent still starts; `roles install-skills` stays strict. Top-level `[tool_rules]` rules are
  merged into every role before its own rules; the shipped Claude Code Read/Edit denies cover
  common secret-file names even with `bypassPermissions`. This is defense in depth, not a boundary:
  processes an agent starts (tests, dotenv, and similar tools) can still read environment files.
  A pop's client can stop on its own pre-session dialog (trust, hooks review) before it declares a
  status (A10): `agent-launch` binds the pane first, so the observe pass already inspects it, and a
  dialog herdr reports as `blocked` (Claude Code's trust check, `agentstate/testdata/screens/claude-trust.txt`)
  gets busmon's `prompt` flag and the prompt-notify ring; `agent-spawn` waits `AGENT_SPAWN_WAIT` s
  (default 30, `0` = no wait; `swap` passes 0, it proves the agent itself) for the first status
  (`agentbus agents wait <agent> --since <ms> --timeout <d>`, read-only) and otherwise names the
  pane. A dialog herdr does not report (Codex's hooks review) is read from the screen: a
  `Signature.Dialog` match, in the last 14 non-blank lines and only when the client is not ready,
  makes the inspection `blocked`, never ready (`screens/codex-hooks.txt`,
  `claude-trust.txt`); only patterns pinned by a real screen.
  `setup.sh install/update` also links `agent-launch` and `agent-spawn` into `BIN_DIR`; those links
  resolve back to this repository's scripts, and uninstall removes only links to these exact files.
  Resume Claude Code with `agent-launch <role> <project> [--profile <p>] --resume <session-id>` to
  retain role settings and identity; never use `claude --resume` by hand. Every Claude launch passes
  `--settings` (the resume hook, the turn hooks, plus tool rules when there are any). Codex, Kimi, Grok
  and agy refuse resume until their session id source is verified (`rolecfg.Clients`, `ResumeVerified`,
  self-repair step 5): Codex's hook proof needs a probe in a Codex pane (it re-trusts the launch hooks in
  `~/.codex/config.toml`), agy's needs `hooks.json` wired, Kimi has no source. Grok can take a session id
  the launcher chooses (`--session-id`, resume verified with grok 1.0.77), but `/new` and `/clear` start
  another session that only its `SessionStart` hook reports, and Grok runs a project hook only once the
  person trusts the file: until a run shows that hook proved, a resume could reopen a cleared conversation. A Codex pane restored by herdr itself runs a bare
  `codex resume <id>` with no hooks: it proves no session and reports no turns.
  **Automatic state** (spec `docs/superpowers/specs/2026-11-01-auto-state-design.md`): the launch's `--settings`
  (`launchHooks`) also runs `agentbus roles turn start` on `UserPromptSubmit` and `roles turn end` on `Stop` and on
  `SessionStart` (`cmd/agentbus/turnhook.go`: silent on stdout, always exit 0, 2 s with the dial, a prompt starting with
  `/` and a subagent's events ignored). A Codex launch carries the same two turn hooks as `-c` overrides for that
  launch only (`codexHookArgs`, route a: `features.hooks=true`, `hooks.UserPromptSubmit`, `hooks.Stop`, each command a
  TOML string through `tomlString`, since Codex keeps a `-c` value that does not parse as a raw string; never
  `~/.codex`; Codex lists them under its session flags and asks once to trust them, never bypassed). A Grok launch
  (route b, `rolecfg/grokhooks.go`) writes `<folder>/.grok/hooks/agentbus.json` where Grok starts (`UserPromptSubmit` turn
  start, `Stop` and `StopFailure` turn end, timeout 5; not `StopCancelled`, which can arrive after the next turn's start),
  only when absent or the file agentbus last wrote unedited (its hash in the launch cache; an identical file it did not write is used, never adopted), never the home folder, never
  through a link, a non-regular file never read; when it is agentbus's and in a Git checkout, its path is added once to
  the repository's local `info/exclude` (never a `.gitignore`; escaped as a gitignore literal, a line break refused,
  confirmed by `git check-ignore --no-index`); a failure only warns; Grok runs it once the person answers its own folder-trust
  dialog (never `--trust`); its hooks run as children of the `grok` process, so the bound pid is in their chain, and
  `subagentType` events are ignored. Kimi has no hooks, and neither has agy: its `--settings` refuses every hooks key
  (agy 1.5.3), and a `.agents/hooks.json` in the worktree would collide with the repository's own, so an agy role
  declares its state itself. An agy launch (`rolecfg/agy.go`) passes `--settings` with a session file in the launch cache:
  `toolPermission = request-review` always written (an omitted key inherits the person's own, which may be
  `always-proceed`), `allowNonWorkspaceAccess = false`, `permissions.allow = []` (a session list replaces the person's),
  and `permissions.deny` only when a deny translates (an empty one would drop the person's own denies). When one
  translates, and every shipped role's deliver deny does, the session's deny list **replaces** the person's own agy
  denies for that role (agy merges `permissions` field by field, a list whole): a deny the person keeps in their agy
  settings does not reach an agy role; put it in the manifest's `[tool_rules]` too (foureyes). Only `Bash`
  prefix denies have an agy form (`command(<prefix>)`); `Read`/`Edit` rules and a `*` inside a pattern (the project's
  `--host *` deliver spellings among them) are not enforced, and the launch names them on one line; `roles doctor` names
  them and the elevated options on a `risk:` line (`Manifest.LaunchRisk`, the launch's own logic, agy roles only; `not judged: <why>` when the rules do not
  resolve, never nothing). agy prints its
  `--help` on stderr, and preflight matches whole flags: `--mode` is inside `--model`. `Bus.TurnState` (`bus/turnstate.go`) flips idle/working in the `{p}:agents`
  snapshot only, never a status entry (`auto: "turn"`, the message kept, only from idle or working, only for the stored
  verified pid and start); a declaration clears `auto` and wins over a flip whatever their times (`mergeSnapshot`).
  `bus.EffectiveState` reads a flip's working as idle once a fresh observation has seen the pane ready for `staleAfter`,
  since `max(flip ts, the ready since)`, ready meaning herdr's `idle` or `done` with the pane not busy (`unknown` proves
  nothing); nothing is rewritten. A flip keeps the last declaration's time in `DeclTS` (`AgentSnapshot.DeclaredTS`):
  quiet-agent counts that as the agent's last write, never a flip, and `team-stall` reads each agent through
  `EffectiveState` with `dispatch.turn_stale_after` (2 min, `0` = off). busmon carries `auto` on each agent (`agentState.auto`, set by the poll, cleared by `declare`): the row's message reads muted with `(turn)`, the agent's page says so, and the flag `idle?` (`idleHint`) shows a working agent whose pane a fresh observation has seen ready (`idle`/`done`, not busy) for `display.idle_hint_after` (10 min, `0` = off, `shared.idleHint` copied to each agent by `takeSnapshot`).
- **Settings: one registry, `config/keys.go`** (config-file, spec
  `docs/superpowers/specs/2026-10-29-config-file-design.md`): every tunable's file key, env name,
  default, today's env rule (use, skip to the next source, or invalid) and typed file rule; per key, env
  over `config.toml` over the legacy `busmon.toml` (busmon's keys only) over the default; warnings name
  file, line and key and never echo a value; no secret and no connection in the file. Readers resolve
  through `config.Resolver`; `TestNoEnvLiteralOutsideTheRegistry` fails on a registry env name written
  as a literal elsewhere. busmon's settings are
  migrated (`loadBusmonSettings`), and so is the person's name (`personName` in `identity.go`: the files
  only, `config.toml` then `busmon.toml`; `identity set` writes `config.toml`; `ask` and the plugin keep
  their raw env rule through `config.EnvName`), and so are the services and writers (`config.Machine(getenv)`:
  env, that environment's `config.toml`, `busmon.toml`; `serviceQuota`/`Tokens`/`Clear`, `dispatch.PromptGrace`,
  `bus.ContextAlert`, `runawayThresholdsFromEnv`; the writers' caps per call through `bus.capFiles`, which
  reads no file in a test binary (`inTestBinary`, `testing.Testing`), so the person's own file never changes a test).
  `config.Path` reads `XDG_CONFIG_HOME`/`HOME` through
  the injected getenv only, absolute paths only, so a test's environment never reaches the real home;
  `config` does not import `bus` (bus will read its caps through it),
  so `ValidName` is repeated there, held equal by a test. busmon writes `config.toml` from its settings page
  (`C`, `ovSettings`, spec `docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md`, the person's answers:
  Display, Attention and Identity first, busmon's own display keys applied at once): `config.Set`/`Unset`
  (`config/write.go`: the key's own file rule, the smallest edit, its line replaced or added under its table or the
  table appended, the edited text read back as exactly that change, refused when the file does not parse, a temporary
  file renamed, 0600 in 0700), each key's `Group` (`config/group.go`, held by a test), the rows (`settingsGroups` in
  `settings_page.go`: `settingFrom` default, config.toml, elsewhere (a flag, the env or busmon.toml, read-only but
  busmon.toml's) or refused, `Set` when config.toml sets it to other than its default, the source in `agentbus config`'s
  words) and the interaction (`settings_ui.go`, settings-panels, its own key table `ModeSettings`/`settingsBindings`, reads
  and writes in commands): two panels below one row of top margin (about-trim) that carries the focus line (settings-focus-line, `settingsFocusLine`: `focus: groups`, `tabs` or `keys` and the key that moves it, muted with the focused word plain and bold, so it reads the same without colour; the hint, then `focus:`, give way on a narrow column; `keepSettingInView` scrolls to the top whenever the groups or tabs have the focus or the first key is selected, so the line is on screen then; a heavy accent bar `━` marks the focused panel's top edge: the sidebar's part of that row or the keys' column after the text on a wide page, behind the text above the tabs or the full-width row between the tabs and the keys on a narrow one, settings-focus-bar), the groups a sidebar (`settingsSideW`) beside the selected group's keys from a page
  of `settingsSideMin` (92) cells, a tab bar above them narrower (`settingsTabs`, tabBar's blocks, the far tabs giving way
  to an ellipsis); a value as a control (`settingControl`: `● on` ok / `○ off` muted, a choice `‹ v ›`, the rest plain, in one
  column after the group's longest label and a 3-cell gutter, settings-focus: `settingsLabelCol`, `settingsGutter`; elsewhere
  muted, refused the attention colour), `•` (`GlyphSet`) before a `Set` key; the selected key's detail
  zone (`settingsDetail`: label, file key, `from` source, default, read by; `settingHelp`). The focus (settings-focus,
  `settingsPage.keys`) is on the groups or the keys, the focused side lit (the group in the accent, the key's `▶` in the
  accent, the narrow tab a full pill) and the other muted (the tab dimmed by `frameFade`): wide, `↑↓` choose the group (its
  keys follow, the selection back to the first), `→` or Enter go to its keys, `←` back; narrow (the tab bar), `←→` step the
  tabs (wrapping), `↓` or Enter go to the keys, `↑` on the first key and `←` come back; Tab toggles; `↑↓` move among the keys
  (kept in view, `selLine`); the editor takes keys first, so on an open choice `←→` change its value, Enter writes, Esc
  closes it leaving the file as it is. Enter on a key flips an on/off at once, opens a choice or a line editor checked by the kind then the rule; `d` asks `y`, naming what
  applies next; `D` asks `y` naming the group's keys config.toml holds a line for (`settingsResettable` over `FileHeld`: a refused line
  and one a flag or the env shadows too, which the confirmation says still wins, never a `PersonOnly` key, said to stay), unset one by one then read back once; theme, fade, bell and the reminder and recap
  timings apply at once through `applySettings` and `shellConfig.retheme`, the rest at busmon's next start). `agentbus config`
  (`configcmd.go`, above the project-required exit, no broker) prints each value and its source;
  `--project` reads that project's env file for what its services see (only registry keys and the two
  per-project facts, never another line). The local LLM peer's endpoints are not registry keys:
  `[llm.<name>]` tables of `config.toml` only (`config/llm.go`, `File.Endpoints`; `url`, `model`,
  `remote`), each taken whole or dropped with a warning (a secret, user info, a query, an unknown
  key; a host other than `127.0.0.1`, `::1` or `localhost` without `remote = true`); `agentbus
  config` lists them by host and port, never the URL. `agentbus peer` (local-llm-peer step 1, package
  `peer/`) is the local model as a read-only test peer: `examples/roles.local-llm.toml` launches it through
  the adapter `scripts/local-llm-peer --spec-file <launch.json>`; the profile's `model` must equal its
  endpoint's (`Resolve`); one receiver lease from before the drain to its exit (`Bus.TakeReceiver`, then
  `Receiver.Drain` and `Deliver` under it, so the dispatcher sees rule 2 throughout); per entry (directive,
  challenge, reply; never a `wake=no` notice nor a reply from another local-llm peer; a tracked request
  accepted, then completed with the answer) a fresh turn (`Turn.Run`: the role prompt `roles/local.md`, the entry and its thread, at most
  8 tool calls, `turn_timeout`), the reply on the entry's thread, a report, its own `working`/`blocked`/`idle`;
  once the reply is sent, a failed report or status is logged, never answered again.
  An answer is cut under `CmdMaxRunes` with a note, and a reply the bus refuses for good is said, reported and
  acked (never turned again); pane lines carry no control but newline and tab, and neither they nor the answer carry a
  rune that reorders or hides text (`stripFormat`/`hidesText`: the bidi controls, the zero-width space, word joiners, the
  soft hyphen, the BOM, the deprecated, annotation, shorthand and musical format controls; the joiners, tag characters and Arabic number signs, which carry meaning, stay); errors carry the status, never the
  endpoint's body; the start's `/models` check has 5 s.
  The client follows no redirect and uses no proxy; only transport errors, 5xx, 408, 429 and 404 are retried
  (`blocked`, the entry kept, every 30 s), any other refusal fails the turn with a reply. Tools: `read_file`,
  `list_dir`, `grep` under the project root (peer-security: the template's `working_dir`, else the launch
  folder's Git top-level, never the cwd itself, an absolute path; the home from `$HOME`, else the passwd entry, else no start; `/`,
  `$HOME` and its ancestors refused, and (peer-residuals) a root at or under `$XDG_CONFIG_HOME` (else `~/.config`) or holding
  agentbus's configuration directory (`configRefusal`); no `..`, no `.git` (any case, like the denies: on a case-insensitive
  volume `.GIT` and `.ENV` open the real files, so names compare case-insensitively everywhere, fail closed),
  symlinks re-checked, then opened component by component from the root with `O_NOFOLLOW` (`openBeneath`), so a
  link swapped in after the checks is refused; directories read 256 names at a time (grep's own walk, list_dir's 501);
  regular files only; grep bounded by the turn and 20000 entries; the role's
  effective `Read(…)` denies with Claude Code's glob meaning, `~/` matched against the resolved home too, a bare
  `Read` denying everything, fail closed),
  `bus_thread`, `bus_agents`; no shell, no write, no verdict. `--check <endpoint>` asks its `/models`. Provider
  `local` is a built-in unlimited quota reader (listed by `agentbus quota` only with an endpoint). Health
  `llm-endpoint` (step 2; `health/llmendpoint.go` pure, `internal/dispatchsvc/llmcheck.go` the reader, every minute) probes
  only the endpoints the registered `local-llm` agents use (the one each peer resolved, `AgentSnapshot.Endpoint`, set by
  its own status with `EndpointFP`, its URL's fingerprint (HMAC under the host key `endpoint-fp.key` beside config.toml,
  0600, made once and linked whole: a bus reader cannot test a guessed path; no key, no fingerprint; a recorded one
  with an unreadable key is said, never ok), probed for the model the peer runs, an endpoint whose
  config.toml model or URL changed since the launch said; a snapshot
  without it matches by model), `GET /models`
  through `peer.Client` (no redirect, no proxy) within 2 s each, side by side: fail on no answer or an error
  status, warn when one answers without its model or a peer has no endpoint, ok when all list theirs; unknown
  with no such agent, an unreadable config.toml or no endpoint at all; for you on warn and fail
  (`agentbus peer --check <endpoint>`); the detail names host and port, never the URL's path; sentinel
  restarts nothing.
- **busmon's idle view** (spec `docs/superpowers/specs/2026-11-08-busmon-idle-view-design.md`, layout A, the
  person's choice): `display.idle_view_after` (0 = off, the default; 0 or 1m..24h, `fileIdleView`/`envIdleView`:
  a file value outside is refused, an env value skipped). `Model.ui.lastInput` is the poll time of the last input
  (`isInput`: key, paste, click, wheel, motion, focus; never a tick, a tail batch, a resize, a fade frame or a blur),
  the first poll's until then; `enterIdle` runs on each poll and never over `busyForModal()`, which also holds while
  the view is shown, so a due reminder, recap or tour opens once it is left; notices wait (`noticeHeld`) and no fade
  frame is scheduled. The waking input is dropped in `update` before any dispatch (`q` never quits).
  A terminal sends the current focus as soon as reports are on (Ghostty), so while shown a focus wakes
  only after a blur (`idleFocus`, `ui.idleBlurred`, reset on each entry). `View` asks
  `MouseModeAllMotion` and focus reports only while it is shown. `idleview_draw.go` draws you (the decide + merge
  count in 5-row digits), team (agents not idle, then the idle count) and next (the plan's task in hand, then the
  first upcoming) side by side, else stacked, else one line each; the block drifts one cell a minute
  (`idleDrift`, a diagonal bounce), the strip up to 4 cells.
- **master runs in Claude Code's auto mode** (`claude-sonnet-auto`, the person, 2026-11-08: they
  switched it by hand at every start); every other role keeps its profile (`bypassPermissions` for
  the workers). Checked against Claude Code's permission-modes page before the change: in auto mode
  an explicit ask rule still prompts (master's `agent-spawn deploy` and `infra`, the person's
  approval) and deny rules block in every mode; broad allow rules are dropped on entering it, and
  master's are narrow. rolecfg refuses ask rules only with `bypassPermissions`, where nothing prompts.
  `TestMasterStartsInAutoMode` holds master alone in auto, its ask rules kept.
- **A restored pane recovers by itself** (spec `docs/superpowers/specs/2026-11-08-codex-identity-recovery.md`).
  A new process fails `identityOK` until it proves its pid through the bus; Claude Code's
  `SessionStart` hook (`roles turn end`) does it at once, and Codex's launch now carries the same
  `SessionStart` hook. For any client that does not, the wake decision accepts a pane whose
  registered process is gone when one of its foreground processes carries this agent's own
  `AGENT_BUS_PROJECT`/`AGENT_BUS_AGENT` (`restartedInPlace`, `/proc/<pid>/environ`): the fixed wake line
  only, its turn proves the pid; `/clear`, the observation and pop-done stay strict; another agent,
  or no `/proc`, is still a mismatch.
- **busmon's Projects page (M) deletes only a quiet project** (spec
  `docs/superpowers/specs/2026-11-08-projects-admin-page-design.md` §5). Quiet means: no dispatch or
  conductor lease and no live service record younger than 30 s (`bus.ServiceFreshFor`, by Redis TIME,
  any host; `stopped`, `failed`, `gave-up` and the `installed` marker never count), no active service
  here, no verified agent process alive here, no herdr workspace open here; checked at `d` and again
  just before the delete, and the lease and record part again inside the delete script itself
  (`bus.DeleteProjectIfQuiet`, `deleteIfQuiet`). Agents on another host are seen only through their
  dispatcher, conductor or service records: the broker has no rule that tells a suspended agent from a
  remote live one (the rule `agentbus restore` uses). On a host without `/proc` (macOS) agents are not
  checked at all, and the panel says so (master's choice, never refused); an agent check that errs where
  `/proc` exists refuses. A writer outside the script cannot be fenced: the scan after the DEL reports
  the keys that came back, and busmon --delete exits 1. The outcomes keep their words (`deleteErr`):
  refused and unknown-before-the-script say "nothing deleted" (with the units removed here, if any);
  a lost connection during the script says its outcome is unknown; a failed scan after it says how
  many keys it deleted, never "nothing deleted".
- **`agentbus mem` needs no project and no broker.** It works on files under the repository
  root (`memory/` package: `check`, `new`, `sync`), so its block in `main()` sits above the
  project-required exit, like `limits` and `version`. AGENTS.md carries a generated copy of
  `docs/memory/INDEX.md` between `<!-- memory-index:begin -->` and `<!-- memory-index:end -->`:
  edit the index, run `agentbus mem sync`; `TestRepositoryPasses` fails `go test ./...` on drift.
- **`agentbus term` needs no project and no broker** (`termrec` package, herdr exit step 3):
  `record` runs a client under a PTY (`github.com/creack/pty`) and writes
  `agentstate/testdata/raw/<client>/<fixture>.jsonl` (`--out` elsewhere; `--force` replaces).
  A quiet wait needs the client to have written something, then counts from the last output or
  input (a resize is no I/O), so it never ends before a slow prompt or between a key and its
  echo; a client that stays silent until `max` gets a `quiet-timeout` mark. The header names the
  binary, not its path. A client still running when the script ends gets SIGTERM, then SIGKILL 2 s later (an
  interactive shell ignores SIGTERM). Without an emulator (step 4) the only judgement is on bytes:
  `dump --between` says what the client wrote between two marks. Tests run a fake client (the
  test binary under `TERMREC_FAKE`), never a real one.
- **`EmptyInput` is `Ready` on the current input box** (herdr exit 5d, the architect's ruling of
  2026-11-13): no separate `empty_input` pattern. Each client's Ready is anchored to the box's end
  (claude: the empty `❯` line directly above the closing rule; kimi, grok: directly above the
  bottom border; agy already was), so a second input line or text the client returned reads not
  empty (`<client>-multiline-typed`, `<client>-returned`, DERIVED until a person records them).
  `Signature.EmptyInput` adds the Dialog veto to Match's Modal one; `InputHolds(screen, token)`
  reads a token (`agentbus#<nonce>`, `/clear`) in the current input box only, also with its
  blanks and box borders removed (a wrapped line): from the box's start to the line the client's
  `box_end` matches (claude, agy: the closing rule; kimi, grok: the bottom border; codex: the first
  line out of its shaded block, read raw: an ANSI client's `box_end` sees the escapes, since a blank
  line inside codex's input is shaded too: reviewer-codex), never what a client draws under it (claude's slash menu lists `/clear` while the
  input holds `/cl`: foureyes); a box with no end on screen holds nothing. Mode 2004 is the holder's to check beside it.
  The anchoring reaches today's herdr dispatcher too: it can only wake less, never type into text.
- **`screen` is the seam over the terminal emulator** (herdr exit step 4; `screen/BAKEOFF.md` has
  the bake-off and its numbers). Production is `charmbracelet/x/vt`; gostty (a native library) is
  only the test oracle, behind the `gostty` build tag (`go test ./screen -tags gostty -run
  TestWriteOracle -update-oracle` rewrites `screen/testdata/oracle`), and
  `TestProductionLinksNoOracle` fails if a binary links it. The bake-off itself runs with `-tags
  bakeoff`. Plain and ANSI are rendered by the seam, one SGR sequence per attribute and color, so
  agentstate's signatures read the same bytes whatever the emulator. x/vt's scrollback is not
  used (about 12 KiB a line, trimmed by copying): scrolled-off lines go to a ring of rendered
  strings, and writes reach x/vt in 4 KiB pieces. Its answers to queries are drained into
  `Replies()`, else a query would block the write.
- **The holder's foundations** (herdr exit step 5a, package `holder`; nothing uses it yet). One
  function, `holder.ProjectDir`, is the run directory for the holder, the supervisor and every
  client: `$XDG_RUNTIME_DIR`, else `$TMPDIR/agentbus-<uid>`, else `/tmp/agentbus-<uid>`; each level
  (the fallback root, `agentbus`, the project) is made 0700 by `Mkdir` (which never follows a
  symlink), then read once with `Lstat`: a symlink, another owner (`owned by uid N, not you: another
  user created it; remove it or set TMPDIR`) or a mode other than 0700 is refused. `SocketPath`
  adds `holder-<key>.sock` and refuses a path past 103 bytes (macOS's 104 with its NUL).
  `service.BuildKey` is the one build key (security S7): lowered, reduced to `[a-z0-9.]`, cut to
  40, `.` and 10 hex digits of the raw id's SHA-256. Frames are a 4-byte big-endian length
  counting a type byte (control JSON or raw data) and the payload, at most 1 MiB. Control frames are
  closed sets: `DecodeControl` reads one object, refuses a duplicate key, a case-folded key
  (`encoding/json` alone would take `Kind` for `kind`), any field beyond its op's, and checks every
  value (submit `{kind, term, nonce, count}` with no text or name, start `{role, profile?,
  resume_session?, cols?, rows?}` with no argv, env or cwd). `PeerOf` reads the peer's pid and uid
  from the kernel (`SO_PEERCRED`; macOS `LOCAL_PEERPID` and `LOCAL_PEERCRED`), `CheckSameUser`
  refuses another uid both ways, and `InTree` reads a peer as inside a terminal when an ancestor is
  the holder by pid and start time; a chain that does not reach pid 1 within 32 ancestors is
  in-tree too (an unknown is refused the holder's writes, never let through).
  Step 5b, the server (`holder.Server`; still nothing uses it): one per project, a subreaper
  (`PR_SET_CHILD_SUBREAPER`, Linux), its socket 0600, a socket that still answers refused as
  another holder of the build. A connection is the same uid (`CheckSameUser`), then `hello` in the
  holder's protocol (else a refusal and the close), then one `reply` per control frame. `Start`
  builds the command itself (`TemplateLaunch`: `env ROLES_TOML=<manifest> <agent-launch> <role>
  <project> [--profile p] [--resume id]`, the role in the manifest, the profile its own or a
  `fallback_profiles` one, a resume only for a client whose resume is verified), runs it on a new
  PTY (never reused) with `AGENTBUS_TERM_ID` and `AGENTBUS_HOST`, and reports its pid and start
  time. The holder always reads the output into the emulator (and a 1 MB raw tail) and writes the
  emulator's answers to queries (DSR, DA) back, viewer or not. A terminal ends with its process
  group: on Linux the wait loop learns the leader's exit with `waitid(WNOWAIT)`, leaving it unreaped
  (so its pid, and its group's number, cannot be reused), ends what is left of the group (SIGHUP,
  SIGKILL after the grace: a child that ignores SIGHUP never outlives its terminal, reviewer-codex),
  then reaps it. The group always gets that SIGKILL: the `/proc` scan only spares the grace (a group
  that looks empty is killed at once), it never decides that nothing is left, since one scan can miss
  a process forked while it reads (reviewer-codex). `Close` is SIGHUP to the group, SIGKILL after the grace, and only while the leader is
  unreaped: a terminal that ended is never signalled again, by one Close or many, since its group's
  number may be another terminal's now (reviewer-codex). A descendant that left the group (setsid)
  is not reached; off Linux (no `/proc`) the leftovers are not ended at the leader's exit. A terminal's own pid is reaped by its `Wait`, so its exit
  status is kept; the reaper (on SIGCHLD, and a 5 s sweep) reaps only exited children it did not
  start, never `wait(-1)`. An in-tree peer is refused `start`, `close`, `submit` and an attach
  that types (`in_tree`); `submit` answers `later` until step 5d. `holder.Client` checks the
  holder's uid before it says hello. x/vt answers a program's queries asynchronously, so the holder
  pumps `Replies()` every 10 ms (`replyLoop`), not only after an output: a program waiting on its
  DSR answer writes nothing more. The subreaper is the process's, so every server's terminal pids
  live in one registry (`track`), held from the fork until the pid is recorded, and one reaper reads
  it.
  Step 5c, attach (spec 4.5, 4.6): an `attach` frame turns its connection into the viewer's: the
  reply, a first paint (`screen.Render`), then the output as data frames through the viewer's own
  `outputFilter` (security S3: OSC 52 unless `Config.OSC52`, OSC 8 links with their text kept,
  window reports `CSI 11/13-16/18-21 t`, `DCS $ q` and `+ q`, OSC 4/10-19 `?` queries, DA, every DSR
  request `CSI … n`, DECRQM `CSI … $ p`, XTVERSION `CSI > q`, kitty graphics queries (APC `G … a=q`),
  `CSI ? u`; a sequence cut across reads is read as one, and one past 1 MiB is dropped
  whole). A viewer's queue holds 256 chunks; one that falls behind is repainted from the emulator,
  never blocking the program. From the viewer: data frames are its input (an observer's are
  dropped), written under the terminal's input lock; `focus`, `resize` and `detach` control
  frames. A viewer is focused from its attach until a focus-out; an observer never is. The viewer's
  input is read as a stream (a sequence cut across frames is one): any key starts a draft and an Enter
  (CR, kitty `CSI 13 u`) ends it; SGR and X10 mouse reports, focus reports and the viewer terminal's
  replies (a CSI ending in R, n, y, c or t; any DCS, OSC, APC string) are no typing. A lone ESC waits
  100 ms for the rest of a sequence; nothing follows, it was the Escape key, counted at its own time
  (resolved by the next frame, when Activity is read, or at once on a focus-out or a detach). The most recently active non-observer sets the size. `Activity` reports
  the viewers, the focused ones, the last typing and the draft.
  Step 5d, submit (spec 4.2): the frame is `{kind, term, nonce, count}`; the holder composes the
  line (`termhost.WakeLine` with its own project and the terminal's role, or `/clear`) and judges
  the screen with the terminal's client signature (`Command.Client`, mapped to its agentstate
  profile; none: `not_ready`, never prompted). Under the input lock, which every viewer byte also
  takes (so the lock is the hold during a submit and bytes follow the CR in order): no person
  (`person_active`: a draft however old, typing within `person_quiet`, a typing viewer not proven
  unfocused, a viewer's lone ESC still inside its 100 ms wait), not left behind (`left_behind`),
  `EmptyInput` with mode 2004 on a settled screen (no output for 80 ms, else unsettled: `not_ready`), the same again after the hold, the line as a
  bracketed paste only, then the line's own token (`agentbus#<nonce>`, `/clear`) in the input box
  within `submit_wait` (2 s, the settling included: never longer): CR. Not seen: no CR, the terminal left behind, and the viewers' bytes
  stay held after the lock (`not_landed`): a queue of at most 1 MiB (past it, every further byte is refused until the hold ends,
  counted in the notice's `refused` and never delivered; the holder never stops reading a viewer,
  so its `release`, `discard` and `detach` always get through, spec r25), a `hold` notice to each typing viewer (and one that attaches), `release` delivers them
  in order, `discard` drops them; a settled screen whose input reads empty lifts the mark and
  delivers them (`leftBehindLoop`, every 250 ms). Tests drive a fake client drawn as agy's box.
  Not wired yet: `submit_wait` and `person_quiet` are `holder.Config` fields only (no
  `config/keys.go` entry, no production caller); they are registered as `dispatch.submit_wait`
  and `dispatch.person_quiet` when the holder's main lands (step 6).
  Step 6a, the adapter (`holder.Host`, `holder/host.go`; nothing calls it yet): `termhost.Spec`
  is `{project, name (the role), profile, resume_session, size}` — no argv, env or cwd (S2a). A
  `TermID` is `<build key>/<terminal id>`: `Start` goes to the current build's socket, `List`
  and `Describe` read every holder socket of the project (an older build listed as draining),
  and the other calls reach the terminal's own holder. `Submit` sends the line's kind, nonce and
  count only, and refuses a wake line built for another project or agent than the terminal's
  (the holder would type its own). It passes the termhost contract on a real test holder
  (`TestHostContract`, a fake client driven by files).
- **`AGENT_BUS_PROJECT` is required.** Both binaries die with an error if the project is missing.
  Pass `--project <p>` or set the env var. There is no global default namespace — that was the old
  pub/sub collision bug. The **one** exception is `busmon --list`, which exists precisely to answer
  "what do I put in `--project`?" — so its block in `main()` must stay **above** the project-required
  exit, and above `--reset` (a read-only query never purges). Move it down and it dies on the error
  it was written to resolve; `TestProjectsFindsALiveProject` will not catch that, only the ordering
  in `main()` will.
- **`ValidName` replaced the `ValidAgents` allowlist.** Agent names must match
  `^[a-z][a-z0-9_-]{0,31}$`; `agentbus` rejects anything else. No code change needed to add agents.
- **`ValidStates`** (`working`, `idle`, `blocked`, `waiting`, `done`) is still a hardcoded map in
  `bus.go`. Adding a state means editing that map. `waiting` (v0.12.3) means a *person* must decide:
  busmon sorts it first with ♙, fades the word `waiting` in and out (3 s, `fadeLevel`, ~10 redraws a second only while one waits; true colour only), counts it in the header and rings the bell once per new
  declaration (`tea_signals.go`, after the poll, never in View).
- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT` (defaults `localhost`/`6380`) with the password from
  `REDIS_PASSWORD`, else the password file, else `AgentBus2025!` (redis-password, `bus/password.go`:
  `PasswordFile` = `$XDG_CONFIG_HOME/agentbus/redis.env`, else `~/.config/agentbus/redis.env`, one
  `REDIS_PASSWORD="…"` line, 0600 in 0700, written by a rename; a file that exists but cannot be read
  is an error, never the default, and no error quotes its content; `LegacyPasswordInUse` reads the
  same order). Compose reads the file through `env_file` (`required: false`) and gives redis-server
  its password as a config on stdin (a shell `REDIS_PASSWORD` at `compose up` wins, then the file,
  then the default; `"` and `\` escaped; the healthcheck uses `REDISCLI_AUTH`), never on an argv.
  `setup` writes it (`password` in `setup/redis.go`) only for a fresh broker: no file, none exported nor in the checkout's `.env` (compose would use it;
  status warns about it), no
  `agent-bus-redis` container and no `agent-bus_redis-data` volume, each read for sure. `./setup.sh
  rotate-redis-password` (`setup/rotate.go`, the person's act, never sentinel's) checks everything
  first (none exported, this checkout's broker running, no `REDIS_PASSWORD` in the checkout's `.env`
  nor in any project's services env file, the current one logs in), asks (or `--yes`;
  `--dry-run`), then `RotatePassword`: new file (old kept as `.prev`), `CONFIG SET requirepass`, a
  fresh login, else the broker set back and the file put back byte for byte (an error from CONFIG SET is checked against the broker first: a login with the new one means applied; if neither logs in, or the set-back fails, the
  new file stays, the old in `.prev`: the broker may hold either); then `docker compose up -d
  --force-recreate redis` from the checkout (foureyes: the container keeps the env_file it was created
  with, so a restart would come back on the old password), a fresh login waited for; then the running
  project services restart. Its rollback test starts private `redis-server` processes (CONFIG SET
  changes a whole server, never the shared test broker), opt-in through `AGENTBUS_TEST_REDIS_URL`.
  `scripts/setup-legacy.sh` neither writes nor rotates it; its help is compared to the Go one without
  `RotateHelp`.
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` sender identity defaults to `anonymous`,
  overridable via `AGENT_BUS_AGENT`; inside a herdr pane (`HERDR_PANE_ID`) with `AGENT_BUS_AGENT`
  unset, a command that writes as the sender is refused with the fix (A8, `identityGuard`:
  relaunch through the launcher or export it; read-only commands still run, an unknown command
  counts as a write).
- **`docker-compose.yml` binds `6380` to loopback** (`127.0.0.1:6380:6379`) so the bus is not
  LAN-reachable (the password travels in plaintext); the SSH tunnel is the only remote path. Don't
  revert to a bare `6380:6379` (= `0.0.0.0`) without a reason.
- **Attention and recap (v0.18)** — busmon derives answered `waiting` declarations only from a
  later addressed `human=yes` command or a reply in the agent's thread; open asks remain separate
  attention items. Due reminders wait until drafts and other modals clear, then group oldest-first
  and snooze locally. `R` opens a recap built from current snapshots and retained events; capped
  history boundaries and failed reads are labelled instead of treated as empty. The optional
  summary directive requires an explicit `y` confirmation and uses the normal send path (`send.go`).
  Global settings are in `~/.config/agentbus/config.toml`, then the legacy `busmon.toml` (or
  `$XDG_CONFIG_HOME/agentbus/`), and
  local last-visit/snooze data is atomically cached under `~/.cache/agentbus/` with mode `0600`.
- **Project health (v0.19)** — the dispatcher's separate bounded health loop writes
  `{p}:health`, transition-only `{p}:health:log`, and `{p}:health:notes` through fenced scripts;
  Redis `TIME` owns persisted timestamps. Health keys do not discover a project or make
  `HasKeysBesidesDispatch` true. Missing/stale evidence is `unknown`, never `ok`; remediation text
  is static guidance. A new `fail` episode wakes sentinel once, plus one separate daily directive.
  Sentinel may restart an identified failed project service once within its budget, or refresh the
  local model table; everything else is escalated. Usage refresh is at most every 15 minutes with
  a fenced writer; model refresh is daily under a host-local lock. `redis-version` (redis-watch,
  `internal/dispatchsvc/redislatest.go`) compares the broker's INFO version with the latest Redis release
  **of its own major** (the major it runs, read from INFO; no claim about the compose tag): the dispatch
  service's `RedisRefresh` chore reads GitHub's recent `redis/redis` releases (5 s, drafts and
  prereleases skipped) once a day under a host-local lock into `~/.cache/agentbus/redis-latest.json`
  (major -> newest release, 0600; a failed fetch moves only `checked_at`, silently); warn when behind
  in its major; a newer major is never a warning, only named in the detail (changing major is the
  person's decision); unknown when the broker is unread, no release of its major is found or no check
  succeeded within 48 h. The person updates the broker; sentinel never restarts or upgrades it. `disk` (disk-check,
  `health/disk.go` pure, `internal/dispatchsvc/diskcheck.go` the reader behind injected `diskDeps`, so no test touches a real disk;
  5 min): the filesystems agent-bus depends on (docker's root from `docker info` when docker is on PATH and the daemon runs
  on this host (`dockerNotLocal`: the endpoint Docker itself reports, `docker context inspect` with no name, which resolves
  `DOCKER_CONTEXT` and `DOCKER_HOST` in its own order (checked on Docker 29; never reimplemented), a local `unix://` socket): not
  measured when docker is absent, the daemon remote or its endpoint unreadable (the note names the scheme, never the host),
  or its root in a VM, on macOS or Docker Desktop, never a host path of that name; `HOME`; `go env GOCACHE` when go is on PATH, a failed or empty answer not measured, `off` no path; the
  services' working directory, the repository, and its
  `.worktrees`; `os.TempDir()`), each device once (`st_dev`), judged by statfs's free-to-unprivileged bytes and %: warn under
  `health.disk_warn_pct`/`_gib` (10 %, 5 GiB), fail under `health.disk_fail_pct`/`_gib` (2 %, 1 GiB); the detail names each
  path, its `major:minor` and free space, the worst first; a path not measured (or a filesystem reporting no size) is
  said, unknown only when nothing was. For you on warn and fail with a command that only looks (`du -sh
  ~/.cache/go-build ~/.npm ~/.cache/pip`); `go clean -cache` is named in the detail as the person's choice
  (`DiskCleanChoice`, kept whole: the paths are cut to leave it room within the 300-rune detail), never in the copyable line; a fail wakes sentinel, which never deletes anything. `github`
  (busmon-github-check, `health/github.go` pure, `internal/dispatchsvc/githubcheck.go` the runner; 15 min, 10 s) runs only
  when a remote is on github.com (origin first): `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com` and
  `git -c credential.helper= -c core.askPass= ls-remote https://github.com/<o>/<r>` with `GIT_TERMINAL_PROMPT=0`, in parallel, every command
  under `LC_ALL=C` and without any askpass program (`probeEnviron`: `GIT_ASKPASS`/`SSH_ASKPASS` removed, `SSH_ASKPASS_REQUIRE=never`;
  github-askpass: the terminal switch alone left KDE's ksshaskpass asking for a username every 15 minutes) (the answers are read by git's English messages); any answer from GitHub is reached (auth unknown),
  matched before the network errors since it may quote a repository named like one (TLS by its own phrases), only a network error failed, an unrecognised answer or a missing binary not
  judged. Both reached ok, one warn (for you), neither unknown then warn after `GitHubNeitherWarnAfter` (1 h, tracked
  in the check's memory: a restart starts it over); never fail, so it never wakes sentinel. The stall guard (spec
  `docs/superpowers/specs/2026-11-01-stall-guard-design.md`; judgement pure in `health/stall.go`, readers in
  `internal/dispatchsvc/stallchecks.go`; `dispatch.stall_after`/`clear_stall_after`/`quiet_after`, 15 min, 0 = off):
  `team-stall` fails (waking sentinel, which sends master one waking directive with the next ready task) when every
  non-dormant agent (`bus.DormantAfter`) is idle that long, each pane seen not busy by a fresh observation (none =
  unknown), nothing is open (request, accepted task, ask, gate, pause, swap, obligation, valid hand-off, mission) and
  the plan has an agent task ready whose owner is not held; person tasks only = warn `waiting-person` (for you); it
  reads `quiet-agent`'s record (warn suppresses it, unknown is unknown, `QuietOff` when quiet_after = 0).
  A `waiting` the person answered (stale-waiting, spec `docs/superpowers/specs/2026-11-06-stale-waiting-design.md`:
  `bus.WaitAnswered`, no open question and, strictly after the declaration, a `human=yes` cmd to the agent or a
  reply on its thread, or one of its asks `answered`, at its latest answer not withdrawn) is read by team-stall and
  review-queue as idle since the answer (`answerWaits`, after `readTeam` and `readOpenWork`; it reads the asks and
  2000 cmd entries only while a non-dormant agent waits, a failed read then unknown), named in the detail
  (`master's wait answered 22:30`): the 2026-11-05 stall, master's declaration never lifted after the person
  answered, is `TestAnsweredWaitNeverBlocksTheGuards`. Nothing is written: only the agent lifts its `waiting`. The plan's
  verdicts are read forward (`plan.ReadWithVerdicts`). `quiet-agent` warns (master) on a waking directive or
  escalation from master, the dispatcher or a person, delivered with its payload (an expired request never counts),
  and nothing written since by the agent on status, report or cmd (same millisecond = written), not working, held or
  seen busy; silent since before the retained cmd history = not judged; collected per agent. `clear-stall` warns on
  a valid hand-off older than its threshold, naming the clear pass's last reason (`dispatch.ClearDecisions`; reason
  `pane-focused` is for you). A warning alone woke nobody (chain-reliability): after each stored `clear-stall` or
  `quiet-agent` record the health loop's `StallWake` hook (lease token) sends the wakes the judged input owes
  (`health.ClearStallWakes`/`QuietAgentWakes`), each one waking directive from `dispatch` under `FencedCmdOnce`:
  a stalled hand-off whose reason is `health.NotIdleReason` wakes the agent with the exact command to re-declare it,
  `agentbus handoff <a> --report <id>` (never `status idle`: the wake is a prompt, and auto-state's turn-start flip
  voids the record as working since, while a new declaration is newer than that flip; `ClearHandoff.Report`), with
  no report known, or any other reason (auto-clear off included), master, once per hand-off and target (mark `stall-clear:<a>`,
  episode `<at>|<target>`); a quiet agent wakes master once per directive it went quiet on (mark `stall-quiet:<a>`,
  episode the directive's id). The health loop's `StallNotify` hook (lease token, after each stored `team-stall`)
  tells the person once per fail episode (`FencedNotifyOnceForPerson`, mark `stall` = the record's since, kept across an
  unknown pass and deleted once the check reads ok or warn): at once when master cannot act (`dispatch.CannotAct`, the escalation rule), else at twice
  `stall_after` from the episode's first since (kept in the reader's memory across an unknown pass). `review-queue` (spec
  `docs/superpowers/specs/2026-11-04-review-queue-design.md`; `health/reviewqueue.go` pure, `internal/dispatchsvc/reviewqueue.go`
  the reader) warns (master) when the same idle team (`teamIdle`, shared with `team-stall`), nothing else open (gates
  aside), owes a review: an open gate whose challenge the reviewer read, older than `stall_after` (a challenge no longer retained is
  never proved read, a group skipping trimmed entries: not judged, unknown when nothing is owed); or a subject approved
  within 72 h (fixed) and not done in the plan, where a reviewer who engaged with it (a verdict on it, an open gate on
  it, or a challenge under a ref a verdict on it carries) has no approve of the deciding head; the `StallWake` hook
  wakes master once per owed set (mark `stall-review`, episode a hash of the owed keys); never fail. `idle-implementer`
  (spec `docs/superpowers/specs/2026-11-08-idle-implementer-design.md`; `health/idleimplementer.go` pure,
  `internal/dispatchsvc/idleimplementer.go` the reader; `dispatch.idle_after`, 15 min, 0 = off) warns (master) for each
  implementer (a manifest role with the `implement` skill, `rolesWithSkill`) idle past the threshold, its pane not busy,
  its provider not held, with a board request addressed to it in state `requested`; it needs no idle team. Its clock is
  the `{p}:agents` state time, which a turn end restarts where turn hooks run (a short turn answering a wake restarts
  it: intended). It wakes master once per implementer and requested set (mark `stall-implementer:<agent>`, one
  `{p}:quota` field each, so two implementers never alternate one field; episode a hash of the name and the sorted ids):
  idle, working, then idle again on the same set wakes nobody twice. Requests only, never ready plan tasks
  (`team-stall` names those). Long warns (sentinel-warn-digest): `agentbus health warns` is sentinel's daily digest,
  one message to master, of the checks whose effective status is warn (a stale record is unknown) since more than 24 h,
  each with its age and detail (`health.WarnDigest`). `agentbus health accept <check> --as <person> <reason>` is the
  person's accept, kept in the notes hash under `accept:<check>` (so a sentinel note never replaces it and
  `HealthNotes` never reads it), Redis `TIME`: it counts only for the current warn (written at or after its since),
  holds it out of the digest for a week (counted, not repeated), then the digest lists it again with the accept. It
  changes no measured status. An agent identity refuses the accept, `--as` is required (unproved, as reload's).
  `plan-drift` (the architect's design, reports 1794586011340-0 and 1794586021722-0;
  alert only, the person approved; `health/plandrift.go` pure, `internal/dispatchsvc/plandrift.go` the read) warns master,
  never fails, on a merge of origin/main since the plan's load that no task links and no `agentbus plan ignore <pr>`
  covers, a task whose merge came from another branch than its board entry records, and a task linked to a PR while a
  later PR of its branch stem (`feat/x-2` for `feat/x`) merged after it (its own merge in the list does not hide one). It reads local git only, in the dispatch service's
  working directory, through the bounded exec (own process group, killed on its deadline, output capped at 256 KiB
  while git writes, the group killed past it,
  `-c core.fsmonitor=false -c core.pager=cat`): `rev-list --first-parent --format=%s --max-count=500
  <plan commit>..origin/main`, the plan commit refused unless it is a commit id. Only GitHub's merge-commit subject
  (`Merge pull request #N from owner/branch`) is a signal. Nothing fetches: origin/main's freshness is the newest FETCH_HEAD
  of the repository (the common directory's and each linked worktree's: FETCH_HEAD is per worktree, and the
  agents fetch in theirs) that records `branch 'main' of <origin's URL>` at the very commit origin/main
  holds; a fetch of another branch or remote, or one that saw main elsewhere, is no evidence. None, or older
  than an hour, makes the check unknown, never ok. Load the plan from a commit on main: from a
  branch, the merges of main since its fork point would warn as not in the plan. It wakes master once per drift (mark `plan-drift`, episode a hash of the drift). It writes
  nothing to the plan; a derived Done is a later step. `{p}:plan:ignore` (a set, a record key) is dropped by a new
  `plan load` and by `plan clear`. busmon shows the health pill, F5
  details and retained transitions in its R recap. Each check has a place per status (`Definition.Place`: `Person`
  statuses with their `Command` are for you, any other warn or fail is in hand by its `Handler`, ok is fine, unknown
  not measured; `PlaceOf` adds the evaluator's fixed `Outcome.Reason` code, stored as `Record.Reason` only when the check
  declares it in `Reasons` (code → the person's command, `CommandOf`): services' spent budget (per kind against
  `ServiceBudgetStarts`, read before inactive since a spent kind exits; its command restarts only that kind) and
  `waiting-git` are for you, an inactive service or a recent error in hand, reviewer-codex and foureyes); busmon pages spec `docs/superpowers/specs/2026-10-31-busmon-pages-mockups.md`): the pill counts only
  what is for you (`health ▲ 2 to do (F5)`, `▲2` below the wide tier), else `?N`, else a muted `●` for in hand, else
  `✓`; F5 is the one list pattern (`listrow.go`: one line per check by place, the fine ones folded, `a` lists them,
  Enter opens a row in place, `y` copies a For-you row's command; the selection follows its check across polls). Runaway is warning-only. `cmd-delivery` judges only the consumer groups of agents registered in
  `{p}:agents` (A9, `deliveryEvidence`: a dead group's lag tripped `fail` for nobody; `agents forget`
  destroys a forgotten agent's group). Scanner, canary, key
  inventory and project-defined checks belong to later releases (password rotation shipped as
  `./setup.sh rotate-redis-password`, the person's act); health is not a security boundary.
- **busmon's footer names Enter's outcome on the main screen** (busmon-row-actions, spec
  `docs/superpowers/specs/2026-11-06-busmon-queue-triage-design.md` §5): `rowHint` (`rowhint.go`) gives,
  from the selected row's key and its `Expanded` state, the page Enter opens (`answer`, `agent`,
  `thread`, `task`, `PR`, `health`, `request`, `notices`, `full text`), the change in place
  (`expand`, `fold`, `unfold`; a Requests fold reads `ListItem.Expanded`, since `reqOpen.folded`
  is inverted by name), or `rowNoPair` (`merge:unknown`, or no row selected: an empty key), which leaves the `enter` pair out.
  `pageHint` answers with it when no page is open, in `ModeShell` only (`shellHint`; the picker
  keeps `open`), and `footerHintsOn` puts the `enter` pair first in `ModeShell`. A key outside the
  table keeps `detail`. A new row-key prefix needs its line in `rowHint`:
  `TestRowHintEveryRowAndItsOutcome` presses Enter on every selectable row of five fixtures (the
  Requests ones from `reqShell`, all closed and all open), again on an in-place row after its
  change, and holds the word to what Enter did and to every row-key kind being met.
- **busmon's header strip is a partition of For you** (busmon-queue-strip, same spec §3): `stripOf`
  (`queue_strip.go`) counts `decide` (ranks `rankAsk`…`rankGate`), `merge` (`rankMerge` but never
  `merge:unknown`) and `unread` (`unreadItems`), so `decide + merge = decisions(forYou)` and the strip
  can never disagree with the panel. A new For you rank must be placed in one chip or none, there.
  The panel's frame label is the same words (`forYouLabel`: `2 decide · 1 merge`).
- **busmon's For you holds no plan task and draws no unread line** (home-inbox, spec
  `2026-11-07-home-inbox-drawer-design.md` §3): `rankPlan` and `planItems` are gone, so a plan task
  never counts in `decide`, the reminder or the walk; the plan's `♙ N` is the header's plan item.
  One muted pointer row, `plan-next:<task>` from `nextPlanTask` (the first person task Waiting,
  else Blocked, else Upcoming), ends the panel and the empty state alike; it is no `forYou` item.
  The panel draws `forYouPanelItems` (`forYou` minus `rankUnread`); the model keeps the unread lines
  for the strip, `u` and the walk, so For you expands nothing in place and `recapInPanel` no longer
  drops `unread:` recap rows.
- **busmon's plan is one board for the tab and the drawer** (plan-board, same spec §4):
  `planBoard` (`planboard.go`) gives the lines, the task items and the side panel for both; it
  replaced `planOverlay` and `planTabItems`, and the done group's `toggle` row is gone (`d`,
  `ActShowDone`, on the tab and the page). The drawer is the `ovPlan` page with `drawer` set:
  `readerRect` reads the bottom page's flag, so a page opened from the drawer stays in its frame,
  and at the wide tier `grid` does not draw Agents under it. The side panel is a frame of its own
  (`drawPlanSide`) inside the tab's frame or the page's, drawn over the narrower rows.
  The header's clickable items carry a `headerHit`; `headerLayout` returns their spans, and `click`
  hit-tests the header after closing an open reader. `tourVersion` is 5 (3: the header page; 4: the welcome page's For you line, home-inbox; 5: Useful keys with `p`, plan-board).
- **busmon's triage walk is For you's live order minus `visited`** (busmon-triage, same spec §4,
  `triage.go`): `walkNext` opens the first `forYou` row not visited (never `merge:unknown`) through
  `openForYouItem`, the function Enter uses too (an unread line straight to its message page). The
  walk's page is the reader's bottom page (`walk.serial`); `walkCheck`, run by `pop` and
  `closeReader`, ends the walk once that page has left the stack, except while `walk.moving`
  replaces it. The terminal acts call `walkDone`: `gotAnswer` with no question left, and `gotSend`
  (now returning a command) for a send to the waiting agent (a plan task's act went with its row). `n`/`p`
  on the walk's page go to `walkStep`, never `readerStep`; on an ask page they are bound only for
  that (`shellAskTable`). `busyForModal` counts `m.ui.walk`, so the reminder and the recap wait.
  When `refresh` drops the walk's ask page (answered or closed elsewhere) it sets `walk.lost`, and
  `afterUpdate` (`walkLost`) moves the walk on, since `refresh` cannot return a command.
- **History archive** (spec `docs/superpowers/specs/2026-11-01-history-design.md`, package `history/`): the
  dispatch service's chore (`dispatch/history.go`, its own goroutine beside the reconciliation, 1 min, a 5 s
  pass, 2000 entries shared a page per stream in turn, the round starting one stream further each pass) appends `history.Streams` to `<state>/history/YYYY-MM-DD.jsonl` (UTC day of the id;
  `{"stream","id","f"}`), only while `HoldsDispatch` and under an flock on `history/.lock`; files only, no
  Redis write, so nothing new is fenced. On start (and after a failed write or a deleted cursor) a torn last
  line is truncated and each stream's position is max(`cursor.json`, the files' highest id); no cursor and no
  day file writes the `archive restarted` gap line (`of=*`), a stream trimmed past its position one `gap` line. Every gap line is kept again in `gaps.jsonl` (`ReadGaps`; pruned with the days), and a gap matches time bounds by the stretch it covers (`from`..`to`), not its id, so `--until` never hides a hole.
  Every field goes through `health.Redact` (whole field on any hit; `SafeText` shares `health.Sensitive`).
  `history.keep_days` (90, 0 = off) and `history.max_mb` (256), pruned daily, never today's file. Bus reads:
  `Bus.StreamAfter`/`StreamOldest`. `agentbus history` (`historycmd.go`, after the identity guard and before the
  broker is dialled: it needs the project, not the broker) reads the day files (`history.ReadDays`), then the live
  streams, `history.Merge` by (stream, id), `history.Filter` (`--agent` matches from/target/agent and a verdict's
  author/reviewer; a gap line passes an agent or kind filter it may hide, never `--thread`/`--grep`), newest
  `--limit` (200); says `broker unreachable: archive only` or `no archive on this host: live streams only`.
  busmon's About page (`A`, about-page, `about.go`, `ovAbout`; about-design: centred, the two-row wordmark `aboutLogo` on top (the half blocks, `GlyphUpperHalf` joined the closed set; the name in words below `aboutLogoW` + 2 cells), the subtitle, then the versions as label/value rows in one column under THIS BUILD (busmon, agentbus marked `✓ same build` or `≠ busmon`, protocol, go, the update state marked) and THIS HOST (redis, herdr, plugin), `y` still copying the flat `Lines`; about-credits (about-trim: no author line and no project row under the subtitle; the project stays in what `y` copies): at the foot, `<License> License · Copyright (c) <Year> <Author>` and `buildinfo.Source`, copied by `y` too; the pseudonym and licence are `buildinfo` constants held to `LICENSE` by `TestLicenseMatches`, never read at run time, and nothing more identifying is shown): the versions read in a command (`aboutSources`: the project, busmon, `agentbus version` beside it, protocol, Redis INFO, `herdr --version` through `HERDR_BIN_PATH`, the plugin's `herdr-plugin.toml` version (`AGENTBUS_REPO`, `buildinfo.Repo`, the binary's folder), the update check's cached `latest.json` only, never a network call, Go; `y` copies them), master and coder on a bus line, a packet travelling there and back on the fade schedule only (`animated` reads `aboutPlaying`; the run starts at the first `fadeMsg` after the open, `aboutState.start`, one cell per `aboutCell` 60 ms of frame time), still with fade off, 16 colours or no colour (`aboutMotion`), where `r` (`ActReplay`, OnPage) says there is nothing to replay; else it replays.
  busmon's History page (`H`, `history_page.go`, `ovHistory`): loaded in a command (`historyLoadCmd`: the last 3 days
  under an 8 MiB cap through `history.ReadBack`, which pages **by position** with a `Cursor` (day file, byte offset)
  because a day file is not in id order; `ReadGaps`; the live streams through the writer's optional
  `liveStreamReader`; the three sources kept apart in `histView` and windowed together, `window`), newest first
  under day headings (`historyBody`, short kinds below 80 cells), Enter opens an entry in the message page (`n`/`p`
  there step the page's rows, `historyStep`), `o` (`ActOlder`, OnPage) widens the window by three days (the archive read on from its cursor, the gaps read again, the live entries windowed again), `/` filters it with
  Activity's syntax on its own line kind (`lineHistFilter`, `historyFiltered`), `t` narrows it to the selected
  entry's thread.
- **Backups** (spec `docs/superpowers/specs/2026-11-01-backup-design.md`, package `backup/`, being built step by
  step): `bus/keykinds.go` classes every `{p}:<kind>` record (restored) or runtime (archived, restored only with
  `--with-runtime`), a ":*" kind one `ValidName` tail only; `TestEveryWrittenKeyKindIsClassified` scans the module for
  kinds written (literals joined with `+`, and literal kinds given to any bus function that routes a parameter into
  `StreamKey`, found to a fixpoint), so a new key is classified when it is added. `bus.DumpProject`: one `MULTI` of
  `TYPE`/`PTTL`/`DUMP` per project (`ProjectKeys`' exact prefix), only `PTTL = -1` kept. `backup.Writer`: a `.tar.zst`
  (klauspost zstd, default level, one encoder goroutine, frame checksum), `coverage.json` first, every member hashed,
  `manifest.json` last; any failed `Add` abandons the archive; `Verify` checks every member before anything is
  restored; `Run` writes a temporary file in the folder and links it to its final name (never replacing one).
  `agentbus backup` (`backupcmd.go`, above the project-required exit; `--project` only from the command line, never
  `AGENT_BUS_PROJECT`): every project of the host (broker + templates), `backup.dir`/`backup.keep` (group Backup),
  `backup list`, `--dry-run`; secrets (`redis.env`, `services/<p>.env`) only stat'ed, left out and listed.
  Retention never removes the newest archive covering a project. `agentbus restore` (`restorecmd.go`,
  `backup.Restore`): `Verify` first (member names must be clean relative paths), the broker's version not older than
  the manifest's in any part, the project's `upLock`, then `projectLive` (dispatch/conductor lease, a service active,
  the herdr workspace open, a registered agent's verified pid alive; a read that fails refuses, and so does no service
  manager unless `AGENTBUS_SERVICE_MANAGER=none`) whatever `--force`; `bus.RestoreProject` is one Lua script (leases
  absent, no archive key present unless `--force` deletes it, checked before any delete; `--force`'s keys `DUMP`ed with
  their `PTTL` then deleted; `RESTORE` under `pcall`; on error every written key deleted and every forced key put back,
  `REPLACE` with its TTL, so the project is exactly as it was); records only unless `--with-runtime` (unknown keys too); files
  after a successful Redis part, each verified against the manifest before its rename (`Walk`, `Verified`), a
  symlink's target replaced, the roles destination only an absolute clean `.../roles.toml` from the archive's
  template; the host's settings only without `--project`. Secrets (`backup/secrets.go`): `--secrets` needs `age` on
  PATH and a recipient (`--recipient` or `backup.age_recipient`, `config.ValidAgeRecipient`: an `age1…` public key),
  else refused before anything is written; a tar of `redis.env` and `services/<p>.env` is streamed into `age -r` (no
  clear text on disk) and the ciphertext is the member `secrets.age`; `restore --identity <file>` pipes it into
  `age -d -i` (agentbus never reads the key), each secret restored 0600 by the file rules, the password file only
  without `--project`. `backup.after_command` (`backup/after.go`, `RunAfter`): config.toml alone (`Key.PersonOnly`: no env,
  `MachineOnly`, `config.Set`/`Unset` refuse it; `after_env` a TOML list kept as a comma-joined string), split by
  `rolecfg.ShellSplit` and run with no shell in `backup.dir`, its own process group (SIGTERM at `after_timeout`, SIGKILL
  10 s later), the `AGENTBUS_BACKUP_*` variables, an environment scrubbed of secret-named or `health.Sensitive`
  variables but those `after_env` names; output capped at 64 KiB, each line `health.Redact`ed; run after the rename and
  before the retention, never on a refused run, its outcome in `<state>/backup-last.json` `after` per project
  (`RecordAfter`, 0600); a failure only warns. Every role denies `Edit` of `~/.config/agentbus/config.toml` (an Edit rule covers the Write tool; Claude Code never matches a `Write(path)` rule and warns about one at each launch, `neverMatches` in `rolecfg/configdeny_test.go` keeps them out)
  (shipped `[tool_rules]`), and a Claude Code launch adds the path `XDG_CONFIG_HOME` gives it (`rolecfg.ConfigDenies`,
  `//` absolute form; a relative one adds nothing and says so). `backup.schedule = daily` (`backupschedule.go`, `scheduledBackupPass`;
  `dispatch.BackupConfig`, an hourly pass only while `HoldsDispatch`): due when a project of the host has no archive
  covering it under 24 h (`backupDue`, per project), a host try-lock `<state>/backup.lock` (`backupLock`, `LOCK_NB`),
  due judged again under it, then `makeArchive` (the same as by hand, `Scheduled`: `AGENTBUS_BACKUP_SCHEDULED=1`);
  secrets only with `backup.age_recipient`, without `age` the archive is made without them and `SecretsError`
  recorded; `backup-last.json`'s `scheduled` part (`RecordScheduled`) is written only by the chore. `agentbus backup`
  warns about a defaulted `backup.dir` while `daily` is on. busmon's C page shows the three `backup.after_*` keys (Backup group, `settingOnPage`: only its
  `PersonOnly` keys) read-only: Enter says "edit config.toml yourself", `d` offers nothing. Below them (backup-page,
  `settings_backup.go`, read with the rows in the page's command: `readBackupFacts`, nothing without a HOME or XDG folder):
  the folder, `Back up now` (Enter, then `y`: `agentbus backup`, `--secrets` when `backup.age_recipient` is set and `age` is on PATH (else left out and said, as the schedule does), run as Z
  runs suspend, `startRun` in `sleep.go`: one child at a time, its own session, output in `<state>/agentbus/backup.log`,
  its last line the notice, its end reading the page again), one row per archive of this host (`backup.List`: date, size,
  projects, secrets or not), and the last runs from `backup-last.json` (errors through `health.Redact`); Enter on an
  archive opens its coverage, the restore dry runs to copy (`y`, the path shell-quoted) and what takes each project offline first (`agentbus down <p>`, its herdr workspace closed: a suspend keeps the agents and the workspace, which restore refuses even with `--force`; `backupOfflineSteps`); busmon restores nothing. Health `backup` (`health/backup.go` pure, `EvaluateBackup`; `internal/dispatchsvc/backupcheck.go` the reader; 15 min,
  for you, `agentbus backup`): per project, warn on no covering archive younger than `backup.warn_after` (168h, `0` =
  this reminder off), on the latest scheduled attempt covering it failed while `daily` (cleared by a newer covering
  archive; the age case only by one with secrets, read from `coverage.json`'s `secrets`), on its latest
  `after_command` run failed while one is set; unknown when the folder or the record cannot be read; the detail names
  the folder.
- **Notifications** (spec `docs/superpowers/specs/2026-11-03-notifications-design.md`, package `notify/`): the
  dispatch service's chore (`internal/dispatchsvc/notifychore.go`, `dispatch.NotifyConfig`, 30 s, only while
  `HoldsDispatch`) tells the person what waits, through ntfy, Discord and the desktop. Targets and their secrets are
  in `notify.toml` beside config.toml (`notify.Load`: refused whole when open to others or not the person's, its folder
  writable by no one else, opened once `O_NOFOLLOW` and judged by fstat; per target https, http only `insecure` on a
  private host, ntfy.sh by default and never `self_hosted`, a topic of 12-64 safe characters, Discord's hosts only;
  warnings carry no value; a `Target` prints only kind and name). Its one writer is `notify.Edit` (notify-guided-setup
  §4, `notify/edit.go`): by table, every other byte kept (every `[..]` or `[[..]]` line is a boundary, only a
  `[kind.name]` part is editable, any other is kept verbatim; the comments right above a header are its table's);
  the targets after an edit must be those before it changed by the edit's own ops, every other target with the same
  values (a rename keeps its own), or nothing is written; values
  judged by `parse` (Load's rules) and TOML-encoded
  before any write; a refused, unparsable or swapped-folder file never touched; `flock` on `notify.toml.lock`, size
  and mtime compared at the rename (`ErrChanged`); a 0600 temp file renamed over it, the folder synced; loaded again,
  and the old bytes put back (or a new file removed) when an added or renamed target does not load. `notify.Explain`
  turns a send's `SendError` into the setup's plain sentence (a TLS failure is the reason `certificate`), and
  `internal/agentcaller` is the agent-caller rule both binaries share. Its CLI callers (step 2,
  `cmd/agentbus/notifysetup.go`): `notifications setup` (questions on `/dev/tty`, refused when stdin is no terminal),
  `add ntfy|discord|desktop` (a drawn topic, `notify.DrawTopic`; a token or webhook from a file, `-` or
  `--webhook-stdin`, never an argument, and an unknown flag is a usage error that echoes nothing; `http` only with
  `--insecure`), `remove` (asks on the terminal, or `--yes`; `notify.on` goes off only on the person's yes), `rename`
  (the header, `notify.targets` and the `notify-last.json` record, `Last.Rename`) and `show-topic`; `add` turns
  `notify.on` on and lists the target, then tests it (exit 0 delivered, 2 written but failed, 1 nothing written, a
  malformed command line included). A
  topic, its link and its QR (`notify.QR`, half blocks drawn black on white, rsc.io/qr, L, up to version 6) go only
  to `/dev/tty`: without one, stdout says to run `show-topic` on a terminal. All five refuse an agent first
  (`refuseAgent`: `AGENT_BUS_AGENT`, an agent ancestor, an unreadable broker) and every role denies them
  (`rolecfg.NotifyWriteDenies` in the shared rules and a drafted role's own; the launch adds the `--project`/`-p`
  spellings, `NotifyProjectDenies`). They also refuse any global `--project` or `--host`, before or after `notifications`, an empty value included (main
  compares the arguments before and after `globalArgs`)
  (reviewer-codex: `agentbus --host x notifications add …` would escape the plain deny), so the plain deny covers
  every spelling that runs; `status` and `test` keep accepting them. Every role also denies its
  Read/Edit and `Bash(agentbus notifications test*)` (no wildcard before the words: it would deny prose), and the
  command refuses `AGENT_BUS_AGENT` or a process under a registered agent's verified pid in any project (so on Linux
  it needs the broker); a backup secret. The settings are `notify.*` (group Notifications, config.toml only, list keys
  `Key.List`). Events (`notifyfacts.go`, never an agent's text in a summary line): open asks, `waiting`
  declarations not answered (`bus.WaitAnswered`, stale-waiting: an answered one is no event and no longer
  silences a prompt; the cmd stream is read only while an agent is declared waiting), plan tasks waiting for the person (episode `plan:<id>`, first seen in memory), team-stall at fail
  (a stall only; the stall guard's own notice is never an event), other fails, for-you warns, `for=person` notices,
  prompts by the desktop ring's rule. `notify.Decide` is pure: a target's first pass records everything standing as
  `baseline` and sends nothing; then the due events without a field (delay, kinds, pause = fail only, quiet hours =
  `quiet_exempt` only, past `max_per_hour` fail/stall only), one message per target, `full` detail (`notify.Scrub`) only
  to a private target (desktop or `self_hosted`); fields pruned when their fact or target is gone, never by age.
  `{p}:notified` (`bus/notified.go`, fenced; runtime, ignored by `HasKeysBesidesDispatch`) holds kind, episode, target
  and time only; a field is written after that target's success (at least once). A pass with any failed read sends
  nothing and records the source in `<state>/notify-last.json` (host-wide, under the flock `notify.lock`: latest 20
  outcomes per target apart from the counted hour; a send's hour reserved under the lock, given back if it fails).
  The dispatcher's own prompt ring is skipped (`PromptConfig.Skip`) only for an episode that began after a listed
  desktop target's baseline. Health `notifications` (5 min, for you, never fail); busmon's C page shows the group, then
  `Set up ntfy|Discord|the desktop`, then one selectable line per target (`notifyTargets`, never its URL). Enter on a
  set-up line opens the guided setup (notify-guided-setup step 3, `notify_setup.go`, `ovNotifySetup` in its own
  `ModeNotifySetup` table): four steps, nothing written before the last, which writes through `notify.Edit` (an edit
  after `e` removes the old table in the same edit), lists the target and turns `notify.on` on (`nsList`), then tests
  (`nsSend`) and says the outcome by `notify.Explain`. A field being edited takes every key but tab, enter and esc
  (esc puts its value back); a paste goes whole into it, a newline ends it; masked fields draw `••••` and a count. The
  code is `notify.QR` in cells drawn black on white (`Seg.QR`; reverse under NO_COLOR), beside the steps when the
  page has 26 rows and is not narrow, else on `c`. A target line opens its page: `t`, `n` (rename, the record moves),
  `x` (modal y/n; when that leaves no target listed, a second modal asks to turn `notify.on` off, which only y does),
  `s` (the topic and code until any key, that key consumed). A step with one field takes a paste without a tab first. `?` on step 1 opens the old read-only template page
  (`settings_notify_assist.go`). The page refuses an agent: `AGENT_BUS_AGENT`, an agent ancestor, or agents that
  cannot be read (`nsAgentCaller`, `internal/agentcaller`; busmon's tests stub it in `TestMain`, never the broker). One HTTP client: no redirect, no proxy, 10 s, a 429 waited once (seconds
  or a date, at most 60 s), a 5xx retried twice; errors name the target and status, never the URL or body.
- **Liveness is passive** — there is no heartbeat stream. busmon shows the age of each agent's
  last declared status as a freshness bar (empty after `staleAfter` 10m, atop
  `cmd/busmon/main.go`) and never infers `offline`. Agents are one-shot CLI calls, so each
  status/report publish *is* the heartbeat; don't add a heartbeat unless agents become long-running
  daemons.
- **`Bus.Tail` uses XREAD (no consumer group)** — it never touches group cursors, so busmon never
  competes with `agentbus watch` for cmd entries. `Bus.WatchCmd` uses XREADGROUP with the group
  name set to the agent name, giving at-least-once delivery across one-shot restarts.
- **Stream length is capped** at ~1000 entries per stream (XADD MAXLEN ~ in `stream.go`). Older
  entries are trimmed automatically; the busmon backfill on startup replays only the last `--limit`
  of whatever remains (default 25; `--limit 0` for all).
- **`busmon --reset` is `XTRIM`, not `DEL`.** `Bus.Purge` trims each stream to `MAXLEN 0`, so the
  visible history is wiped but consumer groups (cmd at-least-once cursors) and the
  `armed`/`pilot`/`gate` keys **survive** — don't switch it to `DEL`, which would reset cmd delivery
  state. The confirmation reads stdin **before** the TUI starts; a piped/non-TTY stdin counts as
  "no", so `--reset` never purges unattended without `--yes`. **`busmon --delete <p>` is the `DEL`**
  — the deliberate opposite: it retires the project, so consumer groups, leases and every hash go
  with it. `--reset` clears a project you are keeping; `--delete` removes one you are not.
- **`ValidName` in `bus.ProjectKeys` is what stands between `--delete '*'` and an erased broker.**
  The `SCAN MATCH` glob is built from the caller's string (`project+":*"`), so the name is validated
  **before** the client is touched — `TestProjectKeysRejectsGlobsBeforeDialing` passes a nil client
  on purpose, so a guard that ran too late would panic rather than pass. The trailing colon also
  makes the prefix exact: `demo:*` never reaches `demo-2574:status`, so a delete cannot take a
  same-prefixed sibling with it. Never build that glob from an unvalidated string.
- **The verdict ledger is the audit source of truth, the gate is just a lock.** `verdict` writes
  `{p}:verdicts` unconditionally (self-approvals included — the roll-up ignores them, but the record
  stays). The roll-up rule: APPROVED iff the latest independent approve (reviewer ≠ author) is newer
  than any reject. Don't gate "was this approved?" on the `{p}:gate:{agent}` hash — that only holds
  *open* challenges; query `agentbus verdicts` instead. And never close a stale gate with a verdict
  of your own (it counts as an approve): `agentbus gate close` leaves a trace that is not a verdict.
  busmon's For you lists a gate only when it concerns the person (`personGateItems`: the person
  opened it, or it holds the person, read as `personGates` when the person is no agent); a review
  between agents never counts in the strip's `decide` chip.
- **A plain `subscribe` acknowledges older entries unseen; `--drain` does not.** With no
  `--since`, `subscribe` floors at the server's "now", so everything that waited for an unarmed
  agent is ACKed without output. That is why a woken agent must run `subscribe --drain` — keep the
  wake text and the skills pointing there, never at a plain `subscribe`. One exception (v0.14.2,
  `Bus.WatchCmdDeliveryFromNow`, `keptForPerson`): a person's waking entry to that agent
  (`human=yes`, not `wake=no`, retained, within `DefaultHorizon`) is delivered, so an answer that
  arrives while the agent re-arms without its cursor is never lost. An explicit `--since` keeps
  the strict floor.
- **An unverified `status` never overwrites a verified identity.** Every `{p}:agents` write goes
  through `putSnapshot` (`bus/ident.go`, one WATCH/MULTI transaction, `mergeSnapshot`): a status
  with no pid keeps the stored pane/pid/pid_start/boot_id/terminal, a verified one replaces them, an
  older entry never regresses state, `bind` changes only the identity, and a verified status
  replaces the identity only if it is newer than `ident_ts` (the last proof). Don't write the
  snapshot with a bare HSET — Codex's app-server env carries another session's `HERDR_PANE_ID`,
  and a blind write would erase the bound pane. One runtime per agent name.
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
  Self-repair step 6b, the shadow (`internal/dispatchsvc/selfrepairchore.go`, `dispatch/selfrepairchore.go`):
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
  Self-repair step 7b, on mode acts (`internal/dispatchsvc/selfrepairact.go`): `on` (the default) runs the pass
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
- **Ideas (ideas slice 1): the project's ideas live on the broker, not in notes.** `bus/ideas.go`:
  `{p}:ideas`, one flat hash (`seq`, `<id>` the definition {title, text, by, at, source}, `<id>|s` the
  state, `<id>|log` the capped audit {from, to, by, at}), every write one Lua script with Redis `TIME`.
  Four states, none final: `to discuss`, `decided`, `doing`, `dropped`. `agentbus idea add` is open to
  agents and always starts at `to discuss`; `idea set` is the person's: refused in an agent's
  environment (`AGENT_BUS_AGENT`) or a herdr pane (`HERDR_PANE_ID`), named by `--as` or `identity.as`
  (never `pilot`), and the script refuses a name registered in `{p}:agents` or a reserved one. A set is
  a compare-and-set on the state the setter saw (`--from`, or the state read just before; busmon's
  picker passes the state shown), so two windows never overwrite each other. Each set writes a `reply`
  to `people` (`wake=no`, `human=yes`): Activity shows it, nobody is woken. busmon: Work's IDEAS lists
  the ideas still `to discuss` (a plan task naming an idea's id or title takes its place); F6 lists
  them all by state (tab: one state, enter: the idea, a: add one in the compose window, its first line
  the title and the rest its text, s: the state picker, ←→ choose, enter sets it) and flags a state set by someone
  other than the configured person. busmon refuses a set or an add when it runs in an agent's
  environment (`AGENT_BUS_AGENT`: it would inherit the person's name; an agent adds with `idea add`
  under its own); a failed add keeps the window's text for the next `a`. An add is idempotent by
  its request id (`IdeaDef.Request`, a `req|<id>` field holding the idea and its Redis time, dropped by the next add
  after a day: no field TTL, the broker floor is 7.2): busmon reuses the id while
  the text sent is unchanged, and `idea add` always sends one and prints it on a failure (`--request <id>`
  retries), so a reply lost after the script committed never yields a second idea; it does not refuse a herdr pane, since the
  person's own busmon runs in one (the plugin). **The limit, said plainly: this is an
  audit, not a proof.** Agents run under the person's uid with the same broker URL, so nothing stops a
  deliberate `idea set --as <person>` from a plain shell, or a raw `HSET`: the guards catch accidents
  and every set records who and when. Real prevention would need a separate uid for the agents (out of
  scope). The ideas once kept as open-question notes are moved by master with `idea add --source`
  (data, not code).
- **Asks (v0.14): a person's decisions are structured questions, not report text.** `bus/asks.go`:
  `{p}:asks`, one flat hash (`<id>` definition, `<id>|q|s|a|<qid>|r|c|e`, `open:<agent>`), every write
  one Lua script (`askCreate`, `askAnswer`, `askWithdraw`); the id is the stream id of the `ask` cmd
  (`target=people`, `wake=no`, no `ref`; `people` is never a cmd target or consumer group). First
  answer wins; an answer to a replaced ask follows `replaced_by` only for an unchanged question; one
  open ask per agent; each answer is a `reply` (ref = ask id, `human=yes`) that wakes the agent. Client
  JSON never carries answers. v0.15.1: `ask <agent> --withdraw <id> --question <qid> --note …` closes one
  question (`AskAnswer.Withdrawn`), the ask ends with the last one; a directive typed to an agent with an
  open ask is held once with a hint (Enter sends, Tab opens its panel prefilled). The answer window
  (ask-replaced-answer) follows `replaced_by` as the bus does, for a question carried unchanged (`followAsk`,
  `bus.SameQuestion`), its draft too (`answerDraft`); a question changed, dropped or closed keeps the text, says so,
  and a second send makes it a message to the asking agent (`answerToMessage`), as does closing it (Esc keeps it as a
  message draft to that agent); a message draft is never overwritten, a second one held after it (`heldDrafts`, each
  opening once the one before is sent or emptied); the master skill forbids replacing an
  ask the person may be answering. busmon: the reader's ask page (`tea_reader.go`, `ModeShellAsk`, `!`/`w`/F4; `w` with no ask open
  opens the compose window to master, `ActAskOrWrite`, busmon-keys; Enter on a For you ask or in the agent's page; `askpanel.go`'s bodies), counts and fade follow open
  questions (`waitingQuestions`), bell per ask id; `markdown.go` renders long texts (`renderMarkdown`,
  overlay bodies only; rows stay on `Preview`).
- **Observed is not declared (v0.14.3).** The services' dispatcher inspects every verified pane
  every `ObserveEvery` (15 s; `Config.Observe`, 0 = off, so the default config and its tests
  inspect nothing) and replaces `{p}:observed` (`bus/observed.go`, fenced, whole hash, `PEXPIRE`
  120 s), after the pause check (a paused project is not observed), within a 5 s budget per pass (inspections and time reads; the final write has its own 2 s bound),
  the interval counted from the pass's end. It only reads panes: no typing, no state change; a pane
  failing `identityOK` is not observed. `{p}:observed` is not in `projectKinds` and
  `HasKeysBesidesDispatch` ignores it, so a write racing `busmon --delete` never revives a project. busmon reads it with the dispatcher facts and adds the flag `busy` (and an `Observed`
  detail row) when the pane runs a turn but the agent does not declare `working`. Never let it
  rewrite or replace the declared state. Client updates (spec
  `docs/superpowers/specs/2026-10-21-client-reload-design.md`): the launcher fingerprints the client
  it resolved (`clientfp`, `AGENT_BUS_CLIENT_FP`, kept in the verified identity as `client_fp`); the
  observe pass re-checks it at most once a minute (or reads the Claude Code `versions/` layout for
  agents without one) and adds `update` and `client_judged` to `{p}:observed`; one directive per
  update episode goes to sentinel when it is registered (a snapshot), else to the escalation target
  (`FencedCmdOnceMarks`, mark `update:<client>:<episode>` = `sent`); health `client-updates` warns;
  busmon flags `update`. Metadata only: the dispatcher never runs a client; sentinel reloads the
  outdated agents with `agentbus reload` (lifecycle-3b, below). Auto-reload (spec
  `docs/superpowers/specs/2026-11-06-auto-reload-setting-design.md`): the directive's text follows
  the reload policy, read at each send (`Config.ReloadPolicy` = `serviceReloadPolicy`, i.e.
  `service.ReloadPolicy` over the env, the project's env file, then `config.toml`): `on` with
  sentinel registered says reload the ready ones now, master last; `on` without one asks the person
  through the escalation target (it may reload no one but sentinel); `propose` keeps the proposal;
  `off` reloads nothing. Never word it "propose only" under `on`: that text, left over from v0.22,
  is why `on` never reloaded until v0.29. Under `on` with sentinel, the episode's send also sets a
  per-agent mark `update:<client>:<episode>:<agent>` for each agent ready then (`reloadReady`:
  declared idle, not observed busy, no `accepted` board task, no gate on either side
  (`ReviewsOf`), no swap or reload intent, not on a fallback, every read succeeded), in the same
  script, with `update:<client>:<episode>:auto=on` saying the episode went out automatically;
  on later passes, only for such an episode and while the policy is still `on`, an agent of the
  episode now ready with no mark gets one hint
  (`client update: <agent> is ready now …`, `retryReloads`), never sentinel about itself. busmon's
  C page shows `sentinel.reload` as "Auto-restart on update" (on / propose / off) in the Services
  group, with self-repair's keys (`servicesOnPage`).
  v0.15: an observation carries `since` (first reading of
  that state by the same pid, carried forward) and a failed inspection keeps the previous reading
  (`Observation.Fresh`, 120 s); a pane seen `blocked` (its client's own menu or permission prompt)
  reads `waiting` with the flag `prompt` (`promptWaiting`), one bell per `since`, unless the agent
  declares blocked/waiting or has an open ask. The dispatcher also rings the desktop once per such episode
  (`dispatch/promptnotify.go`, decision `docs/memory/decisions/2026-10-27-prompt-notify.md`): after
  `AGENT_BUS_PROMPT_GRACE` s (default 30, `0` = off) of the same pid and `since`, with the same
  suppressions plus a pause or a swap, through the context alert's `notify-send` runner (missing =
  a logged note); its only write is the fenced mark `prompt:<agent>` (= `pid:since`) in `{p}:quota`,
  deleted when the pane is no longer blocked or the agent leaves the observation. No bus entry, nothing typed.
  The same runner rings once per episode for an agent deferred for `identity mismatch` while
  messages wait for it (A8, `dispatch/identnotify.go`: mark `ident:<agent>` = the registered
  `pid:pid_start`, deleted once its decision is no longer a mismatch), since that agent's escalation
  goes to master, which helps nobody when master is the one lost. An escalation caused by the pane's state (rules
  7-8, `Decision.Cause` → wake `esc_cause`, or read from the reason of an older record) is
  re-armed once the pane is ready: the loop inspects such a record before `Decide`, tries clear
  and `at` stays. An escalation records the obligation's high id it was for (`WakeRecord.HighID`, chain-reliability):
  a newer message for the agent re-opens the wake with fresh tries (`Decide`'s reset, "new message since the
  escalation") instead of "already escalated" until progress; still one escalation per high id. When the escalation target cannot act (it is the agent, its provider is held,
  its tab is blocked), the escalation is a `notify` for the person only (`recipientCannotAct`).
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
- **Every dispatcher write is fenced.** Wake records, escalation/notify XADDs, deletes and group
  creation go through Lua that first checks `GET {p}:dispatch == token`, so a dispatcher that lost
  its lease — or whose project was deleted — writes nothing and recreates no key. Persisted times
  come from Redis (`TIME`, stream ids), never the dispatcher's clock. Don't add an unfenced
  dispatcher write. Accepted residual: `busmon --delete` (SCAN then DEL) can race one fenced write
  into an orphan key.
- **The `subscribe` JSON output is versioned; nothing else is.** Every `subscribe` event carries
  `"v"` (= `bus.ProtocolVersion`, currently 1). The rule: within a `v`, changes are **additive only**
  and consumers **must ignore unknown fields**; **bump `v` only on a breaking change** to the
  subscribe contract (a field removed/renamed/repurposed or a semantics change). A consumer that
  sees a higher `v` than it knows should **fail loud**, not best-effort — that is what turns a format
  cutover (the old sentinel→JSON break) into an explicit signal. `agentbus version` prints the
  constant. Stream entries and `agents`/`usage`/`verdicts` output are **not** versioned (they are
  additive-by-key via `ParseEntry`/`json.Unmarshal`).
- **Request priority is the person's, and lives in the board entry** (spec
  `docs/superpowers/specs/2026-11-04-request-priority-design.md`, phase 1, `bus/priority.go`): `RequestInfo.Priority`
  `high`/`low` (absent = normal, `normal` never stored) with `priority_by` and `priority_at` (Redis `TIME`), written only
  by `Bus.SetRequestPriority` (one script `requestPriority`: unknown, untracked and done refused with
  `requestTransition`'s words; the same level writes nothing; `updated` untouched; no stream entry). Every other board
  writer is a Lua cjson round trip, so the field survives accept, block, delivery, the conductor and done; **never write a
  board entry from a Go struct** (`json.Marshal` + `HSET` drops unknown fields): `TestNoStructWriterOfTheBoard`
  type-checks the module (`go list -export`) for a `json.Marshal` of `BoardEntry`/`RequestInfo` or an `HSet`/`HSetNX`/
  `HMSet` whose key reaches `BoardKey` through assignments and production call sites, and the old `boardSet` is a test
  helper. `bus.RequestLess` (rank, created_at, id) and `bus.NextRequest` (an accepted request first, `finish first`; else
  the first requested with `Availability` retained; blocked does not hold; a done one in review neither holds nor is
  ready: no review barrier) are what `agentbus request next [<agent>] [--json]` prints (exit 64 when nothing is ready)
  and the skills tell agents to follow. `agentbus request priority <task> high|normal|low --as <person>` refuses an
  agent caller (`refuseAgentForPriority`: `AGENT_BUS_AGENT`, or a registered agent's verified pid among the ancestors, or
  a check that cannot be made), passes the identity guard only with `--as`, and every role denies the plain
  `Bash(agentbus request priority*)` (no `agentbus * …` form: it would deny messages). Plan-task priority is phase 2.
- **What waits for the person's merge is derived, never remembered** (merge queue, spec
  `docs/superpowers/specs/2026-11-05-merge-queue-design.md`, package `mergequeue/`). `mergequeue.Derive` (pure) joins
  the verdict ledger with the open PRs **by head commit**, never by `pr:<n>` subject: an item is an open, non-draft PR
  whose head is a subject's deciding approved head (`bus.RollUp` APPROVED, the decider's `Head`, `deliver`'s own rule);
  every other open PR is counted (`Unapproved`: a newer push, no review, an approve trimmed out, a late approve on an
  older head), never silent. An approved PR that GitHub reports `mergeable: CONFLICTING` is no item either: the
  person cannot merge it; it is listed in the record's `conflicting` (PR number and the PR's own base, which may differ
  from `delivery.base`; `agentbus merge-queue` prints "approved but conflicting with their base, to rebase:
  #500 onto main") for master to have it rebased. `UNKNOWN` (not computed yet)
  stays an item. A closed or merged PR is not in the open list, so never an item. Sources: `gh pr list --state open --json … --limit 1000` (`ReadGitHub`; a list of exactly
  the limit is `ErrIncomplete`, a pass read as unknown), else the git fallback (GitHub without `gh`, GitLab and
  Bitbucket for now, local): `RemoteTips` (`ls-remote --heads`), `FetchBase` (`--no-write-fetch-head --no-tags
  --refmap=`: the empty refmap stops git moving `refs/remotes/origin/<base>`; no ref written, tested byte for byte),
  `InBase` (an ancestor, or no diff: "contents in base", a squash or a revert); local mode reads `LocalTips` (a local
  branch whose tip is the head; no origin with `delivery.forge = auto` is local, as for `deliver`). Every command runs
  with `mergeQueueEnv` (the github check's `probeEnviron`: no askpass, `GH_PROMPT_DISABLED=1`, `GIT_TERMINAL_PROMPT=0`,
  `LC_ALL=C`), 10 s each; agentbus reads no token. The dispatch service's chore (`internal/dispatchsvc/mergequeuechore.go`,
  `dispatch.MergeQueueConfig`, 5 min, 20 s, only while `HoldsDispatch`) replaces the fenced cache `{p}:mergequeue`
  (`bus/mergequeue.go`, a hash, `PEXPIRE` 15 min, runtime, ignored by `HasKeysBesidesDispatch`): `attempted_at` every
  pass, `items_at` and the items only on a success, so a failed pass keeps the last items and never makes them look
  fresh; the reason is said. Readers: busmon (on `requestPoll`, optional `mergeQueueReader`; For you rows at
  `rankMerge`, a decision, `(as of HH:MM: reason)` while a pass fails, one `merge:unknown` row (never a decision) when
  nothing is known; Enter or the reminder open `ovMerge`, `y` the PR's link or in local mode `git switch <base> && git
  merge --no-ff <head>`, `Y` the reviewers' messages), `agentbus merge-queue [--json] [--live]` (read-only: `--live`
  derives in the caller's checkout and writes nothing; exit 0 items, 64 none, 2 unknown), health `merge-queue` (for
  you, never fail), a `decision` notification per item (episode `merge:<pr or branch>|<head>`; none for an item a
  waiting plan task announces, and the check's own warn is never an event), and `plan.Derive` (`Inputs.Merge`, read
  by `plan.ReadWithVerdicts` and busmon's poll): an approved merge task whose subject is queued waits for the person
  with no PR link; nothing makes it done.
- **A report's headline is `bus.ReportHeadline`** (report-one-row §4): the first line of substance of `full` (else `message`),
  blank lines, Markdown headings, code fences and table lines skipped, a list marker stripped, cut after its first sentence;
  the first heading's text when nothing else, else "". The notice `report --to` writes carries it (else the first line; still
  sanitised and cut at 300 runes, the envelope unchanged so `reportSubject` reads old and new), and busmon, the recap, `i`'s
  quote and the `reports` table show it.
- **`report` keeps a bounded preview AND the full text.** The write path stores `message` (the
  ≤500 one-line `SanitizeReportMessage` preview, for `listen`/busmon) and — only when it carries
  more — a `full` field (`SanitizeReportFull`: newlines/tabs kept, ≤8000). Don't raise the preview
  cap to "see more" — that floods the flat viewers; use `agentbus reports <id>` (or the busmon
  ` (+N)` marker) instead. The `full` field lives in the entry and is trimmed with the ~1000-cap
  report stream — `report` is an activity channel, not the durable audit (that's the verdict ledger).
- **An agent whose terminal is gone shows `no terminal`, never its last state (agent-no-terminal-state, 2026-11-16).**
  busmon's Agents view (▼, error colour, with the escalated and blocked) and `agentbus agents` show `no terminal` for an
  agent that ran in a holder terminal (its snapshot names one) when a holder of the project answers on this host and
  holds no live terminal of it; its own state comes back with its terminal. It is a reader's projection
  (`bus.ShownState`): the snapshot is never rewritten. With no holder answering (another host, the holder down)
  nothing is judged and the declared state shows. `agentbus agents --json` prints the stored snapshots as before.
- **`agentbus -p <p> screen <agent>` prints an agent's last screen text, read-only.** It reads
  through the project's holders (any build) and never types. Options: `--lines N` (default 40,
  at most 200) and `--json` (`agent, term, build, rows, lines, trusted, read_at`); the header
  names the terminal, its build and the read time. From inside an agent's terminal (`AGENTBUS_TERM_ID`), only master's
  and sentinel's terminals may run it. The person in a shell outside the terminals may too (the
  person's answer to Q2). Escape sequences and control characters are dropped, so nothing the
  program printed acts on the reader's terminal. An untrusted screen (the holder's emulator
  resynchronising) prints no lines. The caller is the terminal the holder named
  (`AGENTBUS_TERM_ID` on the socket `AGENTBUS_HOST`), or else `AGENT_BUS_AGENT`: it is a team
  rule, not a security boundary (agent-screen-blocked §2).
- **`agentbus --help` is gh's layout** (agentbus-help-gh step 1): USAGE, nine UPPERCASE groups
  (agent, message, review, work, terminal, project, usage, admin, internal last), one
  `name: brief` line per command, then FLAGS, EXAMPLES and LEARN MORE, no color, nothing over 80
  columns. A new command needs a `Brief` (2 to 6 words) in `cmd/agentbus/commands.go` and its
  name in exactly one of `helpSections` (`cmd/agentbus/help.go`), or the suite fails; its detail
  stays in `agentbus help <command>`.
- **`agentbus update` installs the latest release, with a snapshot to roll back to (update spec step 1, `update/`).**
  - **Where it comes from.** The newest `vX.Y.Z` tag on the clone's `origin/main`.
    - The tag is fetched (`git fetch --tags origin`; never a branch or a working tree).
    - It is checked out as its own worktree of the clone, `${XDG_DATA_HOME:-~/.local/share}/agentbus/src/<tag>`.
    - Both binaries are built there. From then on they read that tree, not the clone.
    - Because the tree is a worktree, later updates need the clone's `.git`. A moved or deleted clone is refused, with the path to put it back to.
  - **What it refuses.** A `dev`, `-dirty` or diverged build, and a binary not built by setup. Each refusal names `./setup.sh update`.
  - **Before any change.** A snapshot goes to `<state>/agentbus/updates/<id>/`: both binaries, then a manifest written last.
  - **Then, in order:**
    - `agentbus`, then `busmon`;
    - the `agent-launch` link;
    - the skills, strict and checked;
    - `--project <p>`'s `dispatch` and `missions`, which must record the new build within 60 s.
  - **On any failure** it rolls back by itself.
  - **What it never touches.** Holders, agents, the broker, `config.toml`, env files and other projects' services, which are listed with their commands.
  - **Running it.**
    - `--check` prints the plan only.
    - `--rollback [<id>]` runs the snapshot's own `bin/agentbus update --restore <id>`.
    - `--list` shows the snapshots; 3 are kept.
    - An update killed midway is refused until `--rollback`.
    - It is the person's command: an agent is refused for everything but `--check` and `--list`.
    - It writes nothing on the bus: the terminal and `<state>/agentbus/updates/history` hold its record.
- **An ask comes from an agent, an answer from the person.** `agentbus ask <agent>` names the asking
  agent, which must be the caller itself (`AGENT_BUS_AGENT`): another agent's name, registered or
  not, is refused, and so is any name from the person's shell. Also refused: the person's recorded
  name (`identity.as`, unless the caller is an agent of that name) and a name that wrote from
  busmon as a person and holds no agent entry. Nothing is stored, and the refusal says
  `ask <your agent name>; the person is the one who answers`.
  `--withdraw` is not checked, so a stray ask can still be closed. `agentbus answer` is refused
  from an agent (`AGENT_BUS_AGENT`, or inside an agent's terminal), `--as` included: answers are
  typed in busmon or the person's own shell. The dispatcher also skips an entry under the person's
  recorded name that has no terminal (written from the person's own shell).
- **An attach names its way out on screen (attach-prefix, 2026-11-16).** `agentbus attach` (and busmon's `t`,
  which runs it) starts on a banner, `To leave: press F12 then d`, for 2 s or until a key. That key is not sent
  (the person has not seen the agent's screen: an Enter would answer a dialog unread), unless it is the prefix,
  so `F12 d` works during the banner. The outer terminal's title names the same way out for the whole attach. Ctrl-C goes to the
  agent; it never leaves. An agent page's Screen line reads `t attaches, then F12 d detaches` with the prefix in
  effect. F12 stays the default: on the person's machine it was the outer herdr, not the desktop, that took F12.
  `attach.prefix` is set in busmon's Settings (Display), with attach's own rule for a prefix from the setting:
  refused unless every client the project's roles run on (own and fallback profiles) records it `free`, naming
  each other client and its free keys. Today only F12 is recorded free, for Claude Code, Grok and Codex (codex-cli 0.169.0, its
  `f12-at-input` probe, 2026-11-16): with a Kimi or agy role, attach to it still needs `--prefix`.
- **`dispatch.background_ignore`: the clients' own helpers are never background work**
  (background-ignore-helpers). A process named in the list (default `codebase-memory` and
  `codex-code-mode`, the clients' own helpers; config.toml only) never holds a hand-off's `/clear`, a
  pop's retire or the holder drain, whatever its CPU or where it runs. A shell whose every child is
  such a helper is its wrapper and is ignored too. A shell running anything else, or nothing, still
  counts, as does any unlisted process (`go test`, a dev server). The list is read at each look, so
  a change applies without a restart. Generic names (`MainThread`, every node or Python process;
  `import`; `bash`) are left out on purpose: they would hide real work. Add one only knowingly.
- **Claude Code's hooks now report background work (background-work-visible step 1).** The turn hook reads
  `background_tasks` and `session_crons` from Claude's `Stop` and (newly installed) `SubagentStop` payloads and
  writes `{p}:bgtasks:<agent>`: each task's type, status, sub-agent name and first-seen time, a digest of its id, the
  agent's proved pid; never a command, a description, a cron's prompt or a raw id. `SessionStart` drops it; it lives an
  hour; `busmon --delete` removes it with the project. The dispatcher's background judgement reads it as the
  `hooks` source, for the agent's own process only: a Claude agent whose last Stop listed nothing now reads
  background none when no process works either (before, its hooks were never read, so with no busy process it read
  unknown, which skips a `/clear` after a hand-off and a pop-retire as running work does: `background work: unknown`
  in the dispatch log). Takes effect at the agents' next launch (the hook) and the dispatcher's restart.
- **The process source in the observation (background-work-visible step 2).** Each verified agent's observation in
  `{p}:observed` gains `bg_procs` (its active non-helper descendants, the background judgement's own filter) and
  `bg_oldest` (the oldest one's start, Redis clock); no process name or argument is published. A process's age counts
  from the dispatcher's first sight of it, so a dispatcher restart restarts the ages; the skip's reason stays without
  an age (its once-per-change event compares it). busmon reads them in step 3.
- **busmon shows an agent's background work (background-work-visible step 3).** One mark per agent after its
  message, the state word unchanged (an agent with only background work still reads `idle`): its declared text
  (`agentbus background`) first; else the tasks its Claude Code hooks reported (`… 3 bg tasks 14m`, the oldest's age
  counted from agentbus's first sight); else the dispatcher's process count, a lower bound (`… bg ≥1? 9m`). `◷`
  follows when it has scheduled wakeups, never counted as running work (the person's `⏰`, drawn one cell wide). A
  record of another pid, past its hour, or an observation of another process or too old shows nothing. The header
  chip names an agent with observed work alone as `bg`. The agent's page gains a Background section: the declared
  text and who set it, one line per task (`subagent Explore · running · 3m`), the source and its last report, the
  program names running under the agent (read live from the holder while the page is open, never stored), and the
  wakeups with their schedule.
- **A background skip that lasts is told to master, and a declaration holds the skip (background-work-visible step 4).**
  When the dispatcher has skipped an agent's auto-clear or pop-retire for its background work for
  `dispatch.background_notice_after` (default 2h, `0` off, Health group), the escalation target gets one notice per
  episode, naming the agent, the action and the source (hooks, process, screen, declared, unknown); wake=no when the
  agent is the target itself. The episode is two `{p}:quota` marks, `bgskip:<agent>:<action>` and
  `bgskip-notice:<agent>:<action>`, ended by the first pass that does not skip, by a new process, or when the pass does not run (pop-retire off, the project paused); a pass that holds the action for another reason (the pane focused, an open gate) ends it too, so the count starts again. The agent's declared
  text (`agentbus background`) is now a judgement source, always running while it lasts, read by the dispatcher and by
  `holder drain` (`--ignore-background` still overrides): it holds the pop-retire and the drain, and the auto-clear
  until the agent's context episode has escalated (the `context-escalate` mark, for its current session), when observed work alone still holds it.
- **A client's own MCP servers are not background work (background-work census, 2026-11-21).**
  - **The rule:** a client profile may declare `direct_helpers = true` under `[background]`. The
    client's direct children that are not a shell (sh, bash, dash, zsh, fish, ksh) are then its
    helpers: its MCP stdio servers and its daemons. They never count, whatever their CPU, and
    neither does anything under them (the `node` an `npx`-started server runs, codegraph's watcher):
    the census names each process's branch, the client's direct child whose subtree holds it.
  - **What still counts:** work the agent starts runs under the client's shell, so a dev server it
    left running still blocks `auto-clear` and `pop-retire`, and still gets the long-skip notice.
  - **Who declares it:** Claude Code does, as checked on this host. Every agent's
    `codebase-memory-mcp` is a direct child of `claude`, and each tool command is
    `/bin/bash -c source …shell-snapshots… && …`. This ends the first real false positive: docs's
    `pop-retire` skipped for 2 h on `process 'codebase-memory'`.
  - **Who does not, and why:**
    - **Codex** has the same shape (its MCP servers and `codex-code-mode` are direct children, its
      commands are `/bin/bash -c …`). But a one-command `bash -c` or `bash -lc` execs the program in
      place (checked: `bash -c 'sleep 3'` leaves only `sleep`, a direct child), so a dev server would
      read as a helper. Codex therefore lists its helpers by name instead,
      `helper_branches = ['^codex-code-mode$', '^codebase-memory$']`: a listed direct child and its
      subtree are helpers, any other direct child still counts. **codegraph is still counted:** Node 24
      names every node process `MainThread`, so its MCP server cannot be told from a dev server by
      name. That needs the command line, which the census does not read yet.
    - **Grok, Kimi and agy** are not checked.
  - **What a helper starts is the helper's:** an MCP server that shells out to run real work (a
    build, a test runner) never counts. None does on this host today.
- **The Board's alerts say what they measure (board-drift step 4, spec §7).** "no acceptance 40m since
  request", "no reply 3h since accept", "body lost (request 27h)". "No reply" fires only on an
  `accepted` request: one in `review` waits on its reviewers, a `blocked` one on someone else. The AGE
  column is now CHANGED (the last transition, `Updated`, as before), and a task's detail lists the
  request's own times: Requested, Accepted, Responded, and In review (since its last change, while in
  review). From 100 columns the alert column is 34 wide, so the starting point is never cut. "since
  report" (a newer `reported_at`) comes with step 3's `report --task`.
- **A closed pop that owns open requests is no longer silent (board-drift step 1, spec §6).** The
  dispatcher keeps such a pop (its board tasks still owe work), as before, but now tells master once per
  episode, woken: "<role> has no terminal and owns open requests: <task (state)>, …. Bring it back:
  agentbus --project <p> pop <role> --resume <session>" (the session `{p}:resume` holds; without one,
  plain `pop <role>`). It sends it through `FencedCmdOnce` under the `{p}:quota` mark
  `popstranded:<role>` (episode = the pop's snapshot time), only when the pop is closed (no terminal,
  process gone), and not when a read failed. While master cannot act (held by its limit or login, or
  its own tab blocked) it waits, as pop-done's notice does, and the mark stays unset. The dispatcher still never relaunches a pop (Q4).
- **busmon's sparklines follow `display.graphs` (braille-sparklines, 2026-11-18).** The Tokens page's
  7-day column is drawn in `block` (eight blocks), `braille` (two days per cell, 4 levels, filled; padded
  to 7 cells) or `ascii` (`_.-:=+*#`); `auto`, the default, is `ascii` when the locale (`LC_ALL`, then
  `LC_CTYPE`, then `LANG`) does not name UTF-8, else `block`, and never braille. Env
  `AGENT_BUS_BUSMON_GRAPHS`; set on the `C` page, applied from the next frame. Any non-zero day lights at
  least the lowest step (the bottom dot in braille). Gauges and bars keep their blocks in every style.
- **The broker's ACL users, part 3 (broker-hardening step 4b): rotation, status, purge.**
  - **`./setup.sh rotate-redis-password` with `broker.acl` on** rotates each ACL user instead of the
    default user's `requirepass`:
    - as `admin` (from its password file), each user in turn: the new password file first (the
      previous kept as `.prev`), then `ACL SETUSER <user> resetpass ><new>`, then a fresh login as
      that user, then `ACL SAVE`, so `users.acl` in the volume holds each new hash as soon as it
      is set; `admin` last; no recreate is needed;
    - ping's new password copied into the volume (`/data/ping.pass`) for the healthcheck;
    - the running project services restarted.
    - **A user the broker refuses stops it:** that user and the ones after keep their passwords,
      the ones before stay rotated and are saved. A failed `ACL SAVE` stops it at that user and
      says to run it again before the broker restarts.
    - Refused while `REDIS_URL`, `REDIS_PASSWORD` or the `broker.acl` variable is exported;
      admin's file that does not log in changes nothing.
  - **`./setup.sh status`** shows `broker.acl` beside whether the broker's volume holds `users.acl`,
    and warns when they disagree (either way the clients are locked out). A stopped broker is not
    probed.
  - **`./setup.sh uninstall --purge`** sets `broker.acl` back to `off` once the volume is removed: a
    new broker has no `users.acl`. A plain stop keeps both.
- **`./setup.sh acl on|off` (broker-hardening step 4b, part 2): the person's act**, refused from an
  agent's environment or process tree before any password file is read.
  - **On:**
    - a new password file per user, `~/.config/agentbus/redis-<user>.env` (0600);
    - `users.acl` (passwords hashed, `user default off` last) and the healthcheck's `ping.pass` put
      in the broker's data volume;
    - the broker recreated, proved by an `admin` login with the default user refused;
    - then `broker.acl=on` in `config.toml`, and the running project services restarted.
    - A broker that does not take its users is put back: files removed, recreated, setting left off.
  - **The compose file switches on the file:** with `/data/users.acl` present it loads it instead of
    `requirepass`, and the healthcheck logs in as `ping`; `docker compose up` by hand keeps that.
  - **Off:** removes both files from the volume (a one-off `compose run`, so it works on a broker
    restarting on a file it refuses: `off` needs no running broker), recreates the broker, proves
    `redis.env`'s password, then sets `broker.acl=off`. The user password files stay; they are
    rewritten at the next `on`.
  - **On is refused while the broker's ACL is live** (it would replace the files its users log in with): to remake the users, `acl off` then `acl on`.
  - **Refused while** `REDIS_URL`, `REDIS_PASSWORD` or `AGENT_BUS_BROKER_ACL` is exported (each wins over the files).
  - **`rotate-redis-password` with ACL on, `setup status`, `uninstall --purge`:** see
    `broker-acl-rotate.md` (part 3).
- **The broker user per process (broker-hardening step 4b, part 1; off by default, nothing changes yet).**
  - **The setting:** `broker.acl` (`off` or `on`).
  - **With it on,** each process logs in as its role's user, with the password of
    `~/.config/agentbus/redis-<user>.env`:
    - the services (`service run`, the holder service) as `dispatcher` when no registered agent is
      among their ancestors and they carry no identity (a service manager's launch); otherwise the CLI's rule;
    - `setup`, `backup` and `restore` as `admin`, and only from the person's shell;
    - busmon and every other command by `AGENT_BUS_AGENT` and the process tree: under a
      registered agent other than master, `agent` whatever it claims; master's as `master`; the
      person's as `busmon`. A process whose ancestry cannot be read gets `agent`.
  - **A missing password file is an error,** never the default user.
  - **Turn it on with `./setup.sh acl on`** (part 2), never by editing `broker.acl` by hand: the
    users and their files come from there. Rotation per user is `./setup.sh rotate-redis-password` (part 3).
- **Backups hold the broker's snapshot, `broker.rdb`** (broker-hardening step 5). After the logical dump,
  `agentbus backup` (and the schedule while `broker.acl` is off) runs `BGSAVE`, waits (2 min at most) for that save to finish
  `ok`, then copies only the RDB it completed: from the container (`<runtime> cp agent-bus-redis:/data/<dbfilename> -`,
  `broker.runtime`) or, for a native broker, the file itself. It holds the keys with a TTL that the
  logical dump skips. The AOF files are never copied. **The copy must be the connected broker's:** before
  the save, a random marker is written to it under its own key (`agentbus:backup:snapshot-mark:<marker>`,
  created only if absent, 10 min TTL, deleted after by that run alone: an existing value is never
  overwritten or removed, and no snapshot is taken then),
  and a copied file that does not hold it is left out (`REDIS_URL` naming another broker than the local
  container, or another broker's file at the same path); a restored broker holds that key until its TTL
  ends. A save that is refused, fails or does not finish, a data directory not on this host, a file
  older than the save asked for, or one without the marker leaves `broker.rdb` out,
  with the reason in the run's output and the manifest's `left_out`; the archive is then logical only.
  - **Restoring it is manual** (the logical restore stays the everyday one):
    1. stop the broker (`<runtime> stop agent-bus-redis`);
    2. in its data volume, remove the AOF directory (`appendonlydir`) and the old RDB, **keep `users.acl`**
       (a broker with `aclfile` set and the file missing does not start, and without it every user is
       gone), and put `broker.rdb` in as `<dbfilename>` (`dump.rdb`);
    3. start it once with `--appendonly no`, so it loads the RDB rather than an AOF;
    4. run `CONFIG SET appendonly yes` (as `admin`), which writes a fresh AOF from memory;
    5. restart it with its normal config (`./setup.sh install`, or `<runtime> compose up -d redis`).
  - **With `broker.acl` on, only `admin` takes it:** the scheduled backup runs in the dispatch service as
    the `dispatcher` user, which may neither `BGSAVE` nor write the marker, so its archives are logical
    only and say why (NOPERM, "run agentbus backup by hand"); `agentbus backup` by hand connects as
    `admin` and takes the snapshot.
- **The broker's durability is stated, and its access has a stated limit (broker-hardening step 1).**
  - **Durability:** `docker-compose.yml` gives the broker its persistence in the config on stdin,
    not on the argv and not left to Redis's defaults: `appendonly yes`, `appendfsync everysec` (a
    crash loses at most about a second of writes) and `save 3600 1 300 100 60 10000`.
    `bus.Persistence` reads `appendfsync` too. The `broker-persistence` check now also warns when
    AOF is on with `appendfsync no`.
  - **The limit:** agents run as the person's OS user, so they can read every credential the
    person's files hold, ACL passwords included. ACL users protect against mistakes (a wrong
    command, a wrong key), not against a hostile agent. A separate OS user for agents is out of
    scope for now.
  - **The password:** the broker's historical default password ends only when the person runs
    `./setup.sh rotate-redis-password`; no agent runs it.
- **The broker's runtime is a setting, `broker.runtime` (docker or podman; broker-hardening step 3).**
  - **What follows it:** every caller of the broker's container names its binary through
    `internal/brokerrt`, with `<runtime> compose` as the compose command:
    - the installer: preflight, the owner and volume checks, status, uninstall and the rotation;
    - the `redis-password` and disk health checks;
    - busmon's Redis page and Services card;
    - `scripts/bootstrap`'s `ensure_broker`, which reads it through the new `agentbus config get
      broker.runtime`, and stops rather than guess when it cannot.
  - **Under podman:**
    - `redis-password` filters the broker's container by name, because podman has no `publish=`
      filter;
    - the disk check measures podman's storage root, only when `podman info` says the service is
      not remote (a podman machine is not measured).
  - **Without systemd:** nothing new is needed, since `agentbus up` starts the broker. Under
    rootless podman, a reboot needs the person's own `agentbus up`.
  - **The guard:** `TestNoRuntimeLiteral` fails on a new `"docker"` literal in the Go code, or a
    script line calling docker (`busmon-demo.sh` aside).
  - **Valkey (`broker.image`):** compose runs `${AGENT_BUS_BROKER_IMAGE:-redis:8-alpine}`, and
    every compose call passes the setting (`brokerrt.ComposeEnv`). A backup's manifest records the
    server (`redis` or `valkey`), and a restore across them is refused, since their RDB formats
    differ. Not verified yet: the Valkey image's `redis-server` and `redis-cli` names, and the
    `redis-version` check on Valkey.
- **The ACL writer inventory (broker hardening step 4, first PR; no ACL user is enabled yet).**
  - **The check:** `bus.ACLRules` holds the proposed users. `TestACLInventory` runs every command
    shape the suite's production callers sent (`bus/testdata/acl-inventory.json`) through
    `ACL DRYRUN` on the disposable broker, as each user that sends it.
  - **Rebuild it** after adding a writer: record the suite, then fold the recording.
    - Record:
      `GOFLAGS=-tags=aclrecord AGENTBUS_ACL_RECORD=/tmp/acl.jsonl AGENTBUS_TEST_REDIS_URL=… go test ./... -count=1`,
      with `AGENT_BUS_AGENT` unset.
    - Fold: `go test ./bus -run TestACLInventory -acl-update=/tmp/acl.jsonl`.
    - Without the tag the recorder is not built.
  - **Not covered:** a writer no test reaches, and a key a script builds itself. The check logs
    both.
- **Untracked files no longer make a build "+dirty" (build-id-untracked).** Go's `vcs.modified`
  counts untracked files, so notes left in the repository root gave every setup build a "+dirty"
  build id, and that longer key put the holder's socket path over its limit (2026-11-16). setup now
  stamps `buildinfo.Tree` from `git describe --dirty` (tracked files only) and the build id follows
  it; setup says once per install when it builds from modified tracked files. A plain `go build`
  carries no stamp and keeps Go's own view.
- **busmon reads `blocked` for an agent whose client asks something in its terminal.** That is
  a permission or limit menu the dispatcher observed (`{p}:observed`), until a person answers it
  there. It used to read `waiting`, which now means only the agent's own question (a declared
  wait or an open ask). The state column, the For you line and the agent's page say "blocked:
  waiting for an answer in its terminal". The page's Screen section then shows the screen's last
  6 non-blank lines, read live from the holder while the page is open; a line that looks like a
  credential is hidden. The row no longer carries the `prompt` flag or the holder-event
  `blocked` flag: the state column says it (agent-screen-blocked §3, step 3a).
- **busmon's flags, the person's corrections (busmon-cli-flags items 1, 4, 5).** `-l`'s MASTER is the
  registered `master` agent's declared state (`bus.ProjectSummary.MasterState`, the agent named `bus.ACLMaster`), `-`
  without one; the pilot lease (`Master`) is no longer shown there. `--url` (`-u`) sets `REDIS_URL` before
  any connection (`applyURL`), so a unix socket or an ACL user reaches every path, and a switched busmon
  inherits it without the URL in its argv (`--url` is a one-shot in `switchArgv`); `--host` beside a URL,
  given or in `REDIS_URL`, exits 2. The picker is `--new` (`-k`); `--pick` still parses, unlisted.
  With `broker.acl` on, busmon still logs in as its own ACL user (`bus.ConnectAsContext`): a URL's
  user and password are replaced, so `--url` names the broker's address there, never another user.
- **`busmon --delete` keeps the Projects page's rules** (busmon-cli-flags, item 2): a project that is not asleep is refused with the page's reason (Z, x, or `agentbus down` first), `--yes` included, and an unknown counts as live; the hosts where its units remain are named before the question and after the delete; a backup (`agentbus backup --project <p>`, given `--host` as `REDIS_HOST` and refused if its environment would reach another broker than the delete's) is taken just before the delete, and a failed backup deletes nothing. The archive it makes is never removed by `projects.delete_backups`. An agent's process tree is still refused (`refuseDestructive`).
- **busmon's Memory page writes, the person's own acts (person-memory step 4, part b).** `a` opens the
  compose window: its first line `topic type importance` (prefilled with the selected topic and
  `preference standard`; `keep-both` or `supersedes=<id>` after it for a second decision on a topic),
  the rest the person's words, whitespace collapsed to one line; Ctrl+S remembers them with busmon as
  the source (`said: <date> by <you> from busmon`). A refusal (a topic that already holds a decision,
  a credential shape, a bad filing) opens the window again with the text kept. `x` on a topic's page
  forgets the selected observation after `y` (any other key keeps it; git history keeps the text
  until `agentbus mem person purge`). `e` hands the terminal to `$VISUAL`, else `$EDITOR`, else `vi`
  on the topic's file; on its return the whole memory is checked (`Store.CommitEdit`): clean, the file
  is committed as `edit <topic> (by hand, from busmon)`; not, nothing is committed, the file stays as
  saved and the bottom line names the first finding. All three are refused in an agent's environment
  (`AGENT_BUS_AGENT` or a registered agent among busmon's ancestors) and on a machine that is not the
  memory's host, and write nothing.
- **busmon's Memory page, F7 (person-memory step 4, part a: reading).** The person's facts kept on this
  host (`personmem`, `${XDG_DATA_HOME}/agentbus/memory/person`), a topic a row with its kind, its count
  and its newest quote; Enter opens a topic (each observation's type, importance, id, quote, the
  question it answers, what it supersedes, who said it). A file that does not parse is listed as such.
  The store is read when the page opens. On a machine that is not the host (`memoryHere`: the host the
  project's services report, else the one holding the project's checkout), the page says where the
  memory lives and reads nothing. Adding (`a`), forgetting (`x`) and editing (`e`) came with part b.
- **busmon shows which project it opened (busmon-project-badge, part 1).** For its first 3 s
  (`projectBadgeFor`, from the first poll) the project's name in the header is a pill in the accent
  colour (reverse video under NO_COLOR, the pill's own fallback). When neither `-p` nor
  `AGENT_BUS_PROJECT` named the project (busmon took the last one opened on this broker), the bottom
  line says `last project opened: <p> (no -p) · M to switch` once. A switch from the Projects page
  re-executes busmon with `--project`, so it gets the badge and no notice. Only main sets
  `shellConfig.badge`: tests and goldens start without it. Part 2 (a per-project accent colour) follows.
- **A report names its addressees, and a reply goes to its sender (busmon-reply-target).**
  `agentbus report --to <name>` now records the addressees on the report entry itself (field `to`,
  additive; older reports have none), so busmon's page shows the addressee (the person, or an agent), not "broadcast". In
  busmon, `i` or `r` on a message replies to its sender even when an unsent draft to another agent
  is saved: that draft is held and opens again once the reply is sent (before, the draft opened in
  the reply's place, its recipient shown as if it were the reply's).
- **busmon reads `stuck` for an agent whose screen nobody recognises and that has not changed for
  `dispatch.stuck_after`** (3 minutes by default; busmon reads the same setting as the
  dispatcher). It is judged from the dispatcher's observation of the agent's current process:
  settled, its client's signature reads neither ready, nor busy, nor a prompt. The state column
  says `stuck`, For you lists it as "stuck: its screen is not recognised and has not changed for
  <d>", and the agent's page heads its live screen with those words, the last 6 lines below. A
  declared blocked or waiting, or an open ask, is not covered (agent-screen-blocked §3, 3b-2).
  busmon reads the setting on its own machine and the dispatcher on its own (its env file): with
  different values there, busmon says stuck at another time than the desktop ring. An agent being
  swapped still reads stuck in busmon, since its screen is what it is; only the ring waits.
- **busmon, three fixes for the person** (busmon-three-fixes):
  - `/resume` on a project neither paused nor suspended asks nothing: it says there is nothing to
    resume (no red "Run agentbus … resume --yes? y / n"). An unreadable pause state still asks.
  - `G` (live) on Activity with auto-scroll off says so and names the setting (`display.follow`, `/follow`
    or Shift+C turn it on); it still jumps once to the newest lines.
  - The Requests view (`r`, `/requests`) is titled "Requests", with the tab it came from as the way back
    ("esc back to Work"), instead of the tab bar; in the narrow layout the tab row is not drawn under it,
    and a click where a tab used to be switches nothing.
- **A failed CI check goes back to its author, at most `ci.max_handoffs` times, then to the person (ci-loop,
  spec `2026-11-19-ci-loop-design.md`).** GitHub with `gh` only: the merge-queue pass's one `gh pr list` also
  reads `statusCheckRollup`; GitLab, Bitbucket and the local forge get no loop until they have a PR reader.
  `ci.loop` (default on, `AGENT_BUS_CI_LOOP=off`) and `ci.max_handoffs` (default 3; 0 sends every red PR
  straight to the stop). A PR is red when an entry failed (`FAILURE`, `TIMED_OUT`, `STARTUP_FAILURE`; a status
  `FAILURE` or `ERROR`) and nothing still runs; `CANCELLED` and `ACTION_REQUIRED` are not failures, the
  latest of two same-named entries wins, a draft is skipped. The owner is the one distinct `author` of the
  verdict ledger's entries on the head (self-addressed verdicts ignored), else master; the owner's directive
  wakes it, master gets a copy with `wake=no`. One episode (PR, head, sorted failing names; its sha1 digest in
  `{p}:ci`, a runtime hash, `pr:<n>` = `digest|count|stopped|done`) is handed off once; a new head counts
  toward the same cap, green does not reset it. At the cap the loop stops: master's directive (`wake=no`) and
  the person's notice (For you) naming `agentbus ci reset <pr>` (the person's only, refused in an agent's
  environment; also busmon's `/ci reset <pr>`). An owner that declared `blocked` or `waiting` holds a
  hand-off, never the stop. Every write is `bus.FencedCIStep` (one fenced script, each part recorded):
  **at least once**, a duplicate harmless (same text, same `episode`), never exactly once; a closed PR's field
  is deleted (`bus.FencedCIForget`). The loop's failures are the merge-queue record's `ci` field, named by
  its health check beside the queue's outcome.
  - **The base is checked before a hand-off.** For the bases of the red PRs that reach a new decision, one
    `gh api graphql` call per base reads its latest commit's rollup. A PR with a failing check that also
    fails on a red base is held (neither handed off nor stopped, nothing counted), and master is woken once
    per base episode (`FencedCmdOnce`, mark `ci:base:<branch>` in `{p}:quota`); a check that still runs on
    the base makes the PR wait; a base that cannot be read sends nothing for its PRs (named in `ci`). A base
    with no CI on push has an empty rollup: it is not red, and the loop runs as written. The base is matched
    **by check name**: a CI that posts a different context for a PR than for a push (Jenkins
    `…/pr-merge` against `…/branch`) never holds a PR, which then goes back to its author as before.
  - **Silence is never "all green".** A check that never finishes (`EXPECTED` or `PENDING` forever, a
    required check never posted because the CI server is down), a base that stays running, or a base read
    that keeps failing keeps the PR silent, its stop included.
  - **The hand-off gives a link, not the log.** It names `gh run view --log-failed` only for a GitHub
    Actions check run (a `CheckRun` with a workflow); a commit status or another app's check run gives its
    URL alone. The agent must be able to reach that URL (an internal Jenkins, for example).
- **`agentbus merge-queue` marks an approved PR whose head's checks are red (ci-red-mark, ci-loop spec §8 Q8).**
  - **The data.** It comes from the rollup the pass's `gh pr list` already reads, so there is no new `gh` call.
  - **The rule.** A failed entry, taking each name's latest, under the loop's §2 rule. A PR is marked even while another check still runs.
  - **The display.** The row gets `CI red: <checks>`, naming at most 10 then "and N more". With `--json`, the item gets `ci_red`, the sorted names.
  - **No mark is not "green".** There is no mark when `ci.loop` is off (the rollup is not read), when the rollup is cut (100 checks or more), or under the git fallback (GitLab, Bitbucket, local).
  - **The base is not consulted.** A red base shows as red on its PRs too.
  - **busmon** does not show the mark yet.
- **No more writes from `anonymous` (cmd-identity-anonymous, 2026-11-16).** From a shell, `agentbus` writes as
  `AGENT_BUS_AGENT`, else as the person: `AGENT_BUS_HUMAN`, else the name `agentbus identity set` recorded in
  config.toml. With neither, a command that writes is refused with that fix; before, it was sent `from anonymous`,
  and master's answer went to nobody (an `anonymous: no snapshot` escalation followed). `report --to`, `reply` and
  `cmd` refuse `anonymous` as an addressee. Inside an agent's terminal with `AGENT_BUS_AGENT` unset, a write is still
  refused, an inherited `AGENT_BUS_HUMAN` included. A CLI entry sent as the person is not marked `human`: busmon alone
  writes those. `AGENT_BUS_AGENT=anonymous` is the same nobody as unset. The cron `daily-review-trigger.sh` no longer
  exports it: it sends as the recorded name, or is refused (cron mails the refusal) on a machine with none.
- **Codex's question menus read blocked (codex-rate-limit-prompt).** A Codex menu whose question
  ends in "?" right above its `› 1.` selector (the rate-limit menu "Switch to <model> for lower
  credit usage?", which held reviewer-codex 15 minutes on 2026-11-16) is now the dispatcher's
  `blocked`: master gets `blocked: <agent> (codex) since … screen: "<question>"` and busmon shows the
  agent waiting with a blocked flag, instead of "idle" and a bare "agent unknown" escalation. A
  menu without a question mark still reads unknown. F12, attach's default prefix, is now recorded
  free for Codex (probe `agentbus term record codex f12-at-input`, codex-cli 0.169.0), so attach to
  a Codex agent no longer needs `--prefix`.
- **A conflicting approved PR goes back to its owner (conflict-owner §4, build B).** After the
  conflict check, each pass hands every conflict git confirmed to its owner, once per episode (the PR,
  or the branch in the git fallback, and its head; a base move under the same head is the same
  episode and sends nothing), with master's copy (wake=no): "Hand-off k of N". `{p}:conflicts` holds
  one field per PR (`pr:<n>`) or branch (`br:<digest>`), `last|count|first|escalated`, written by
  `FencedConflictTrack` under the dispatch lease (`first` from Redis `TIME`); every directive is a
  `FencedCmdOnce` with its mark in `{p}:quota` (`conflict:`, `conflict-copy:`, `conflict-esc:`).
  Past `conflict.max_handoffs` (default 3; 0 sends the first to master) or `conflict.escalate_after`
  (default 2h, from `first`, the owner held or not; 0 no time bound) master gets the escalation,
  woken, and the owner nothing more until the field goes. An owner `blocked` or `waiting` gets
  nothing and is not counted (the age still runs). The field goes, with its marks, when an approved
  head of the PR is clean or the PR is closed or merged (git fallback: the branch gone, or its tip
  in the base, approved or not: a reset or fast-forward resolves it; a read that fails keeps it); a fix
  in review or turned draft keeps it, so three failed fixes reach the cap. Only a
  pass that read everything forgets; a failed one touches nothing. What could not be written joins
  `conflict_check` ("conflicts: …"). The hand-off says rebase, resolve, test and have the new head
  reviewed: it is delivered as usual (only master and docs push here); it names `origin/<base>`, or
  the local `<base>` in local mode.
- **The merge queue's conflict check (conflict-owner §2, build A: detection; the hand-off is `conflict-owner-handoff.md`).** Each
  pass judges the approved heads and the PRs the forge flags with `git merge-tree --write-tree` against
  the base tip (`mergequeue.CheckConflicts`), oldest approved first. It runs in an empty bare repository
  made for the pass in the project's state directory and removed after, with only `PATH` and an
  isolated `HOME`, no global or system config or attributes, and `--attr-source=<empty tree>`, its
  objects through `objects/info/alternates`: no merge driver, hook or attribute of the repository
  runs, and no ref or `FETCH_HEAD` is written (absent heads are fetched once with `--refmap=`; local
  mode fetches nothing). git older than 2.42 is "too old", nothing judged. A confirmed conflict
  leaves the items for `Conflicting` with `branch`, `head`, `base_tip` and `files` (the first 10);
  a forge flag git finds clean is an item again; what git could not judge (absent head, time) stays as
  before and is named in the record's `conflict_check`, also printed by `agentbus merge-queue`.
  `mergequeue.ConflictOwner` (the board's claim or request branch, else a `<role>/` prefix that is a
  registered agent, else master) names the owner the hand-off goes to (build B).
- **The context alert escalates (context-escalation A, spec 2026-11-15-context-escalation-design.md §2, §3.1-3.3,
  §3.5, §5).** The alert's directive keeps its time (`FencedCmdOnceAt`, `context:<agent>:at`). An alerted agent
  with no hand-off standing (`bus.Handoff.VoidFor` against `{p}:agents`: same pid and start, same proved
  session, no `working` since) escalates once per episode when its known fill reaches `dispatch.context_urgent`
  (90, used only above `context_alert`; at or below it, off and logged once) or the alert is
  `dispatch.context_handoff_deadline` old (20m; 0 = off; at least 1m), on the publication's Redis time: master
  gets a directive that wakes it (`context-escalate:<agent>`, none when the agent is master), the person a notice
  (`context-person:<agent>`) and the desktop notification. The mark names are `bus.ContextMark` and kin
  (`bus/context.go`); an episode's end deletes them all, the alert's time included. The `context` check reads
  them: an escalation of the fill's session with no hand-off standing is a warn with reason `escalated`, For you
  (`escalated: coder 91% (no hand-off 27 min)`); the check never judges a threshold nor writes, and the
  notification relay skips it (the dispatcher's notices are its only rings). An episode ending in the session
  it alerted, no hand-off standing, with an automatic `compact_boundary` in the session's transcript after the
  alert (`tokens.AutoCompactAfter`, under the agent's `CLAUDE_CONFIG_DIR`, else the service's, else
  `~/.claude`) tells master once, `wake=no` (`context-compacted:<agent>`, episode `<session>|<alert at>`, kept
  apart). Not in A: the mid-turn hook (`{p}:nudge`) and the compaction ceiling (PR B).
- **A project may deliver to its own base (dev-flow-step1).** A project template
  (`<projects-dir>/<project>.toml`) may set `[delivery] base = "dev"`: `agentbus deliver` then opens
  the PR into `dev` (`--base` still wins), and the conductor starts a change whose `missions.json`
  names no `base` from it. A project without the line keeps the machine's `delivery.base` (default
  `main`), as before. `agentbus config --project <p>` shows the base and where it comes from. A base
  that is not a branch name stops `deliver` (unless `--base` is given) and the conductor, with the
  template named: nothing is delivered to a base nobody chose. The conductor reads the base once, at
  start: after editing the template, run `agentbus --project <p> service restart missions`. The merge
  queue still records the machine's base until the next step reads the project's there too.
- **PRs go to `dev`; `main` takes only promotions (dev-flow-step2).** The roles and skills name the
  project's delivery base (`origin/<base>`, shown by `agentbus config --project <p>`) instead of
  `origin/main`, and the idle-implementer stall guard says "one PR per step from origin/<base>". The
  master role and skill gain the promotion by hand (the check on a clean worktree at the `dev` head,
  the ask in For you, the fast-forward push on the person's yes, a failure kept to master) until
  `agentbus promote` exists. CLAUDE.md and AGENTS.md state it for this project; decision note
  `docs/memory/decisions/2026-11-16-dev-branch-flow.md`. Plan-drift still reads `origin/main` until
  the next step.
- **The dispatch service starts its own build's holder when none answers.** After an update
  restarts dispatch on a new build, the first time the service holds the lease it installs and
  starts that build's holder, as `agentbus up` does. The older holders keep their agents until
  `agentbus holder drain` moves them; it never stops or restarts a holder.
  - A failure is a line in `dispatch.log`, tried again every 5 minutes while the lease is held.
  - If the installed agentbus was replaced since the service started, it refuses ("replaced
    since the dispatcher started"): restart the dispatcher. `agentbus up <project>` does the same
    by hand (agent-screen-blocked Q1).
- **The dispatch service's runtime is `internal/dispatchsvc` (dispatchsvc move, 2026-11-20).** Its health-check readers, stall
  checks, chores and reboot judge left `cmd/agentbus` whole, unchanged; both binaries keep their paths. The CLI reaches it
  through `Deps`, `Config`, `ForegroundConfig`, `Runtime` and `MergeQueueLive` only. `TestGuard` fails when the package
  imports the CLI or the screen, calls the mission runner, imports `os/exec` in a new file, runs a program not on its
  list or any shell, or imports a module package not on its list; a file path in an older note under `cmd/agentbus` for
  one of these files now reads `internal/dispatchsvc`.
- **busmon says why something is unknown (project-switch-ux item 2).** The dispatcher computes the
  health checks, the quota readings and the merge queue.
  - **When it does not run,** busmon names the reason instead of a bare `?` or "not computed yet":
    - "project stopped" when its services were stopped;
    - "dispatch down", "dispatch failed" or "dispatch gave-up" otherwise.
  - **Where it shows:**
    - For you's merge line reads "merge queue unknown: <why>";
    - the header's health summary adds it after `?N` ("stopped" for short in the compact header);
    - the quota gauges get one "(<why>)" after them when a provider has no reading at all.
  - **When dispatch runs, or its state is unknown,** the old wording stays.
- **A role may be popped on another profile by name: alt_profiles (fable-profile-architect).** A
  role's `alt_profiles` in `roles.toml` list profiles `agentbus pop <role> --profile <p>` and a
  template tab's `--profile` may name, on the same provider too; unlike `fallback_profiles` they
  are never a quota escape (`swap` and the quota ask ignore them). A session started on one resumes
  on it (holder drain, `up`), never silently on the role's own. The shipped manifest adds
  `claude-fable` (Claude Code's `fable` alias, the models table's frontier) as architect's
  alternate: `agentbus pop architect --profile claude-fable`; plain `pop architect` stays on Opus.
  Fable counts against Anthropic's separate `7d-fable` window (`agentbus quota`), and a full window
  holds every Claude agent of the project today (the hold reads the provider's tightest window).
- **A Claude Code agent boots by itself (fresh-agent-boot).** Its launch passes one fixed first
  turn on the command line, after the role's system prompt: "Run the On boot steps of your role
  now." (`rolecfg.BootLine`), fresh or resumed (`--resume`), so an agent started by `up`, a pop, a
  reload or the self-repair no longer sits at an empty prompt, unbooted and "agent unknown", until a
  person types into it. Nothing types it: the dispatcher's lines stay the wake line and `/clear`.
  Codex and agy already take the role prompt as their first turn; Kimi and Grok are unchanged.
- **The relay of host commands (herdr exit §4.2, step 10a; wired in 10b).** After the cutover the holder
  refuses `Start`, `Close` and `Submit` from inside an agent's terminal, so a host command run there sends the
  dispatcher a host directive on `{p}:cmd` (`type host`, target `dispatch`, `wake=no`): a closed set of names,
  `{op, agent, profile, ignore_background, from}`, ops `up pop relaunch reload swap suspend resume retire drain`
  (`bus.HostOp.Validate`: an agent exactly when the op takes one, a profile only for swap and pop, every name a
  `ValidName`), never argv or text. The dispatcher's own goroutine (`dispatch/hostops.go`, beside the
  reconciliation) reads them every 250 ms while it holds the lease: one outside the set gets `refused: <why>`;
  otherwise `bus.FencedHostAccept` (one fenced Lua script, Redis `TIME`) accepts it only within 5 s of its entry
  id and writes the `accepted` reply in the same step, or replies `expired`; an id more than 1 s ahead of the
  server clock (an explicit `XADD` id) is `refused` (security S10a-1); a directive already handled is `seen`. Accepted ones go to one serial worker (64 queued at most; a full queue fails the operation), joined
  when the loop ends, which marks the operation `running` under the lease (`bus.FencedHostRunning`, only from
  `accepted`) before it runs `HostOpsConfig.Run`, and runs nothing without the lease; it writes one outcome
  reply, `done` (no wake) or `refused`/`failed` (waking), plus a `host-op` event. `{p}:hostops` (runtime) keeps
  each directive's state; a dispatcher starting replies `not-run` to each `accepted` one (never started) and
  `unknown` to each `running` one (it may have acted: check before asking again), tells master, and never runs
  either late. The command side
  (`relayHostOp`) exits 0 on `accepted` and 1 on anything else; its deadline is 5 s after Redis `TIME` at the
  send (never after its own id: a forged entry ahead of the clock pushes later ids forward); with no reply by
  then, after a last read, it withdraws the directive (`bus.WithdrawHostOp`, an `HSETNX`-like script on
  `{p}:hostops`, atomic against the accept: a withdrawn directive is `seen` and never acted on, however late the
  dispatcher reads it; withdrawn too late, the dispatcher's reply is there and is the answer), and only then
  prints the spec's "the dispatcher did not answer within 5 s … Nothing was done" text. Since 10b, main sends
  the directive for each of those commands run with `AGENTBUS_TERM_ID` set, and the dispatch service's `Run`
  (`cmd/agentbus/hostrun.go`) runs it with the very command a shell runs, the sender as the caller.
- **herdr is gone (herdr exit, step 10b).** Every agent runs in a terminal of the project's holder; nothing
  execs herdr (`internal/noherdr` fails the suite on a Go `exec` of `herdr`, a read of `HERDR_BIN*` or
  `HERDR_SOCKET_PATH`, or a shell line running it; no allowlist). An agent's terminal is `AGENTBUS_TERM_ID`
  (the identity guard, `idea set`, `deliver`'s role check and the launcher's bind read it; `env_refs` may not
  set `AGENTBUS_*`), never `HERDR_PANE_ID`; these person-only checks read the caller's own environment, so
  they stop mistakes, not an agent that unsets the variables: only the holder's in-tree peer check is enforced
  (security S10b-1). Inside a terminal, `up` is refused at once (the person's command; the relay's sender is
  self-attested, so no up is relayed, and the dispatcher refuses a host `up` from anyone: reviewer-codex;
  `up --dry-run` runs, it starts nothing), and `pop`, `reload`, `swap`, `retire`,
  `suspend`, `resume` (with no words) and `holder drain` go through the dispatcher (`relayFromTerminal`): only
  the words the closed set carries (`pop --profile`, `swap --profile|--back`, `retire --forget --episode <ms>
  --reason <text>`, the end of a done pop the dispatcher asks master for; a reason is one line of at most 120
  characters, never starting with `-` (security S10b-4), recorded, never run), anything else (`--fresh`,
  `--now`, a `--reason` outside retire…) refused with exit 2, never dropped; the service runs the op in-process with the
  sender as the caller (`hostRunner`), so each command keeps its own authorisation: the person's commands
  (suspend, a suspended project's resume, holder drain) see the sender as `AGENT_BUS_AGENT` and refuse it;
  an op on the sender itself, `--ignore-background` from anyone (security S10a-3) and `relaunch` (no such
  command) are refused before anything runs; exit 0 is `done`, 2 `refused`, else `failed`, with the command's
  last line. The dispatcher prunes finished `{p}:hostops` records older than 24 h (`bus.HostOpsKeep`) at its
  first pass and hourly (security S10a-4). `agent-spawn` is gone: master pops with `agentbus pop <role>`
  (its rules `Bash(agentbus pop <role>)` and `Bash(agentbus pop <role> *)`, never a prefix), `setup` removes
  an `agent-spawn` link it made, recoveries name `agentbus --project <p> pop <role>`. The daily review's cron
  sends sentinel a bus directive (the dispatcher wakes it); its crontab line and the services' env file carry
  no `HERDR_*`; the suspend snapshot has no herdr session; `up` has no `--session`; self-repair's hold kind is
  `holder`. Rollback: reinstall the release tagged as the last with herdr.
- **`agentbus holder drain <project> [--ignore-background]` (herdr exit step 6d2, `holderdrain/`).**
  Finishes an older holder build's drain on the person's word, never automatically. A shell that
  carries an agent's identity (`AGENT_BUS_AGENT` or `AGENTBUS_TERM_ID`) is refused. Nothing is done
  while any holder socket of the project does not answer (its terminals would be unseen). Each agent
  whose live terminal is on an old build, in name order, one at a time, is moved by the reload
  protocol (`agentbus reload`'s, on the holder):
  1. the reload intent, so the dispatcher neither wakes nor clears it, and a wake being typed is
     waited out (10 s);
  2. the same idle process re-checked;
  3. the old terminal closed and its program gone, so two clients never run one session;
  4. the new terminal started on the current build with its `{p}:resume` session;
  5. its proof within 3 min: a verified status whose pid and start time are the new terminal's
     program or one of its descendants;
  6. the swap ledger's `reloaded` entry and a `relaunch` event.
  A start that fails or a missing proof stops the drain (exit 1); the ledger records it with the way
  back (`agentbus up <p> --resume`), and an unproved new terminal is left running. An agent that
  declares or shows `working`, is `blocked`, has a person active, a typed unsent line, an
  unreadable terminal, a swap in progress, a process the bus does not prove in that terminal, or
  background work running or unknown (spec 4.3b, judged over a 10 s CPU window after a first look)
  is skipped and named; exit 3 means "run it again later". Codex, Grok, Kimi and agy have no
  authoritative background source, so they always read unknown: the person drains them with
  `--ignore-background`, which lifts that check only. Only when every holder still answers and
  nothing is left on an old build does it run `service gc` and write `holder-drained`.
  `termhost.Term` now carries the terminal's client profile (`Profile`), which the look judges with.
- **`holder` warns `holder_restarted` (herdr exit step 6d2, `health/holder.go`).** When the
  current build's holder answers, the `holder` check also weighs every agent whose last proved
  identity is a holder terminal (its pane `<build key>/<terminal>`, a verified pid). An agent is
  lost when all of these hold:
  - the holder of that build answers and started after the agent's proof;
  - the agent's proved process is proved gone on this host: no such process, another start time
    for its pid, or another boot (a reboot ends it too); a lookup that fails otherwise, or a
    proof recorded without a start time, proves nothing and never makes an agent lost;
  - it was not declared `done`.
  A holder restart kills every process it held, so a terminal id the new holder reuses never hides
  a lost agent, and a terminal that ended by itself is not called lost. The holder's start is its
  socket's age on this host; the proof's age is on Redis's clock, each measured on its own clock.
  The warn names the build, how long ago it restarted and the agents lost. Nothing is relaunched by
  itself: `agentbus up` or self-repair brings them back, and sentinel only escalates (its runbook
  and the check's remediation say so). With the agents unread, the check judges as before. No agent
  runs on a holder before the cutover, so it stays OK until then.
- **`agentbus attach` (herdr exit step 7a, `cmd/agentbus/attach.go`, `attachkeys.go`).** `attach
  <agent> [--observe] [--prefix <key>]` finds the agent's live terminal (its role) among the
  project's holder sockets, the current build first, and streams it raw both ways
  (`holder.Client.ReadFrame`/`SendData`/`SendControl` after an `Attach` call). The way out is the
  prefix, `attach.prefix` (default `f12`; `f1`-`f12`, `ctrl+<letter>`, `ctrl+\`, `ctrl+]`, never
  `ctrl+c/d/h/i/j/m/q/s/z`, one rule in `config/attachkey.go`): `<prefix> d` detaches,
  `<prefix> <prefix>` sends one, `r`/`x` release or discard held keys (only while the holder
  holds some), any other key after it is dropped with a bell; legacy and kitty encodings, split
  reads, a kitty release or repeat of it dropped; a lone ESC goes after 30 ms. Attach asks the outer
  terminal for focus reports (`?1004h`) and turns `ESC[I`/`ESC[O` into `holder.Focus`, never keys:
  the holder's person-active judgement sees a blur (reviewer-codex); a program that enabled focus
  reporting gets none through attach, and one that turns it off (`?1004l` in its output, split
  between two frames or not: `focusWatch` keeps an open sequence across frames) has it turned
  back on at once. In kitty mode the repeat or release of the key that ended a pending
  prefix is dropped too (foureyes). **Gated per
  client**: the client profiles' `[keys]` table records `free`/`bound` per key as step 3 settled
  it (claude and grok: `f12` free, verified offline; codex: `f12` free by its
  `term record codex f12-at-input` probe, 2026-11-16; kimi and agy unknown until that probe is run
  and recorded for them); bound is refused naming the
  free keys, unknown is refused unless `--prefix` names the key (then a warning in the title),
  `--observe` is never gated and never types. Pending prefix and hold notices go to the outer
  terminal's title, never over the program's screen; detach restores the outer terminal
  (`attachRestore`). `TermInfo.Profile` names the terminal's client profile.
- **The agent row's terminal facts (herdr exit step 7b2, `cmd/busmon/hostfacts.go`).** Every 2 s
  busmon reads `{p}:events` (the whole stream at its start and after a gap of more than 500
  entries between two reads, else the newest 500) and the project's holders (List and Activity
  only), and puts on the agent's row: `blocked` (its latest blocked/unblocked event is a blocked
  one: the dispatcher saw its client's prompt; group 1, the page says on what), `draft` (its
  terminal that `agentbus attach` picks holds a typed, unsent line; the page says since when) and
  `bg work` (background work made the dispatcher skip an action less than 10 min ago on the
  broker's clock, the clock the events carry; the page quotes it). The events are a timeline: busmon
  keeps each agent's latest blocked/unblocked as it saw it, so `blocked` stays until the agent's own
  unblocked event, however many others follow; a busmon started after a `busmon --reset` trimmed the
  events sees none. Nothing shows while herdr hosts the agents. A failed events or clock read keeps
  the last facts; a holder that fails only loses the drafts.
- **busmon attach (herdr exit step 7b2, `cmd/busmon/termpreview.go`).** An agent's page (enter on
  the Agents panel) shows a read-only **Screen** section: the last 30 text lines of the agent's
  terminal on the project's holders that `agentbus attach` picks (the current build's holder first, its newest live one), read through `holder.Host` List and Screen every
  second while the page is on top (never under another page, never Submit), every line through
  `Preview` so no byte the program printed reaches busmon's terminal; no section while herdr hosts
  the agent, `not read: <why>` when the holder fails. `t` on the Agents panel or on the agent's page
  runs `agentbus --project <p> attach <agent>` through `tea.ExecProcess` (the `agentbus` beside
  busmon); detach (the prefix then `d`) returns to busmon, and a refusal (a bound or unverified
  prefix, no terminal) shows agentbus's last line in the status line. Spec 4.6's `enter`/`a` were
  taken (detail, silent agents): `t` instead.
- **The dispatcher on a terminal host (herdr exit step 9, `dispatch/hostruntime.go`,
  `dispatch/hostevents.go`).** `dispatch.HostRuntime` is a `Runtime` on `termhost.Host` judged by
  `agentstate`; **nothing runs it yet**: the services' dispatcher keeps herdr's `Runtime` until the
  cutover (step 10), and on herdr none of what follows happens. A pane is a `termhost.TermID`; the
  client comes from the launch profile (`Client`), never the screen; a screen is settled when the
  program wrote nothing for 800 ms (and nothing while it was read); `Focused` is a person active
  (a draft, a key within 3 s, or a non-observer viewer attached and not proven unfocused:
  `termhost.Activity.FocusedViewers`, from the holder's `focused_viewers`), so the wake waits as
  under herdr's focus rule ("pane focused"), never attempted; the identity is the host's child and
  its process tree. On a host
  the loop also: reports a **blocked** episode to master (enter: a waking directive `blocked: <agent>
  (<client>) since … — screen: "…"` after two settled looks of the `Blocked` signature in the
  observation pass; leave: `unblocked: <agent> after …`, `wake=no`; the escalation target itself
  blocked: a notice for the person), once per episode by `{p}:quota` marks `blocked:<agent>` /
  `unblocked:<agent>` (a restart inside a screen-only episode may send its enter again: the episode
  is in memory); tells the person when a line was pasted and **not seen** (`ErrNotLanded`: "wake
  line" or "/clear may be left unsent in <agent>'s input"); skips **auto-clear and pop-retire on
  background work** (`background work: process 'pytest'`; unknown counts as running, so Claude
  Code, whose subagent hooks are authoritative and not wired to the bus yet, is always skipped
  there: unless that wiring lands first, auto-clear and pop-retire pause for every Claude agent at
  the cutover); and writes `{p}:events` (`wake`, `wake-refused`, `wake-not-landed`, `blocked`,
  `unblocked`, `background-skip`) through `bus.FencedAppendEvent`, fenced by the dispatch lease.
- **`dispatch holder-behind`: no holder of the dispatcher's build runs, an older one does.**
  After an update restarts the dispatch service on a new build, the agents on an older build's
  holder are still read and woken. But a `pop`, a relaunch or a `reload` starts on the
  dispatcher's own build and fails. The state's detail names the builds that answer and which
  of them host an agent ("answers but hosts no agent" otherwise). `agentbus up <project>`
  installs and starts the missing holder beside the old one, without touching the agents.
  `waiting-holder` now means that no holder of the project answers (agent-screen-blocked §1).
- **The holder contains its screen emulator (holder hardening G1, part 1; spec §7.1).** Each terminal's
  x/vt emulator runs in its own goroutine (`holder/emu.go`, `emuLoop`) behind a 4 MiB queue. The read loop
  never waits on it: tail, viewers and `readSeq` move on. Its lock is `t.emu`; `t.mu` never runs it.
  - A full queue (a gap) or a recovered panic (`run`, with its lock released) latches the screen
    `invalid`.
  - No progress for 10 s with bytes waiting latches it `stuck`. That goroutine is abandoned, and no
    second one starts.
  - A read while the emulator is behind, past 200 ms, is `lagging` (not latched).
  - Screen, settle (submit) and a viewer's paint use the emulator only when caught up (`view`,
    `paint`, `parsedSeq == readSeq` under its lock).
  - `TermInfo.Screen` / `ActivityInfo.Screen` say the state, `ScreenGen` is raised on a latch, and
    `ScreenReq` refuses anything but ok with code `screen` (`termhost.ErrScreenUntrusted`). The
    dispatcher then reads an inspection error and waits: no wake, `/clear` or submit.
  - Nothing restores `ok` in place: the terminal is replaced (reload).
  - The read loop finds DA1, DA2 and DSR 5 queries in every chunk:
    - while the screen is trusted, each static answer stays pending until its reply is observed in
      what the emulator gave (in order; a Replies panic latches first; a latch during an answer pass leaves the
      pending answers to that pass, which retires what it saw and owes the rest, so no query is
      answered twice; a pass hung on a latched screen is abandoned by the watch; one pass at a time (`ansMu`),
      which rechecks the latch and marks itself under `t.mu` after taking the emulator's lock, and
      owes what is pending on a `Replies` panic even if already latched, reviewer-codex);
    - a latch writes the pending ones once (a query in the hang window before the stuck latch, or
      dropped by a gap: foureyes);
    - once latched, it answers at once, with x/vt's own strings, never a cursor position.
  - An untrusted screen's viewer is painted a notice (replace it with `agentbus reload` once the agent
    is idle), then the live output.
  - Part 2 brings health `holder` on invalid/stuck and `agentbus reload --replace-broken`.
  - `go test -race ./holder` passes. `./screen` fails on x/vt's known `Emulator.Close`/`Read` race,
    on dev too; the holder never calls `Close`, so the emulator goroutine is not in that path.
- **A dead holder is the person's (holder hardening H3).** Health `holder`'s fail goes to For you with
  `agentbus --project <p> service start holder`, and the desktop is rung once per episode through
  `FencedNotifyOnceForPerson`, mark `holder-down` holding the fail's since (Redis time; an unknown
  pass keeps it, ok or warn deletes it), so a restarted dispatcher does not ring twice; the
  notifier's fail line covers it (prefix `Holder down: `, never a second line). Its handler is
  master, not sentinel, who died with the holder. The warn `holder_restarted` stays in hand and,
  while `dispatch.self_repair` is on, says how many agents wait and when the last comes back at
  self-repair's pace (`selfrepair.ProjectPause`, one relaunch per pause, master first).
- **Before a release, run the holder's live check: `agentbus check holder-restart`** (or
  `scripts/holder-live-check.sh`). It brings up a disposable project of its own (`lc` + 6 hex:
  master and coder, never your team), kills its holder with SIGKILL, and times the restart, the
  relaunches (master first, one every 2 minutes, each on its session), the wakes and health
  `holder`; then it removes the project, also on a failure or Ctrl-C (`--keep` leaves it). About
  six minutes a round. `--clients fake` (the default) runs `agentbus fake-client` as `claude`, no
  quota; `--clients real` runs Claude Code, once its folder
  (`$XDG_STATE_HOME/agentbus/.livecheck/work`) is trusted: `cd` there, run `claude`, accept, quit.
  On a real machine its relaunches pass only with the holder's terminal numbering kept across a
  restart (step 1a). The soak run waits for its criterion (holder hardening spec §5).
- **Holder socket name, bounded (P0-2 B, `holder/rundir.go`).** A holder now listens at
  `<rundir>/agentbus/<project>/h-<id>.sock`, where `id` is the first 16 hex digits of its build
  key's sha256: 23 bytes whatever the key, so a dirty build's long key and a 32-byte project fit
  under `/run/user/<uid>`. Before listening, it writes `h-<id>.key` beside the socket (0600, the key
  in full), and its shutdown removes both. Every reader goes through `holder.ProjectSockets` or
  `SocketFor` and takes both names: the host's socket list and its calls, holder health
  (draining, leftovers), `attach`, busmon's projects page. A holder of an older build keeps its
  `holder-<key>.sock` after an upgrade and is still found, reached and drained. A new-format
  socket whose key file is missing, not a regular file, or names another key is ignored.
- **Holder socket path, per OS (P0-2, `holder/rundir.go`).** The holder's socket path limit is
  this OS's `sun_path` less its NUL: 107 bytes on Linux, 103 on macOS (and on any other OS). It was
  macOS's 103 everywhere, so on Linux a dirty build's long key (a 105-byte path) was refused: the
  holder never listened and the dispatcher waited for it. `agentbus up` now checks the path before
  it starts anything (`holder.SocketPathFits`, which creates nothing). A path too long for this OS
  refuses up with the path and the fix (a shorter `XDG_RUNTIME_DIR` or `TMPDIR`): `nothing
  started`. A socket name of bounded length, whatever the build key, is the next step.
- **A restarted holder numbers its terminals on (holder hardening, found by the endurance test).** Each holder
  process numbered its terminals from `t1`, so after a crash, a restart or a reboot the relaunched master
  got `<key>/t1` again, the dead master's pane: self-repair's proof ("a new process in a new pane",
  `replaced`) refused it for 60 s and every relaunch after a holder crash was escalated as failed. The
  holder now records its last number in `holder.Config.TermsFile`, `<state>/<project>/holder.<key>.terms`
  (`holderTermsFile`, set by `holderConfig`), and numbers on from it; an unreadable record numbers from
  `t1`, a failed write is ignored (the process still numbers on). One small file per build is left
  behind by `service gc`. `TestHolderRestartEndToEnd` (release gate) pins the recovery: crash, the
  same socket served again, `holder_restarted` naming both agents, both resumed master first and
  proved, woken again, `ok`; then a holder that never returns fails in For you.
- **Host commands from an agent's terminal reach the dispatch service (host-relay-timeout, 2026-11-16).** Since herdr
  exit 10b, `pop`, `reload`, `retire`, `swap`, `suspend`, `resume` and `holder drain` run inside an agent's terminal
  are relayed to the dispatcher, but only the foreground `agentbus dispatch` turned the relay on: the dispatch
  service (`service run dispatch`) never read a host directive, so each one ended `the dispatcher did not answer
  within 5 s ... Nothing was done` (seen with `reload sentinel` and `pop coder2` from master, on both the old and the
  new build). The service's dispatcher now runs the same relay (`serviceDispatchConfig`, `hostOpsConfig`). It takes
  effect when the dispatch service restarts on the new build.
- **The person is never an agent.** `agentbus ask <agent>` and `agentbus status <agent>` name the
  agent that asks or declares, never the person the questions go to. From another agent's terminal,
  the person's recorded name (`identity.as`) is refused for both, before any broker call (an
  agent of that name, the person's own shell and `ask --withdraw` still pass). `agentbus ask
  <person>` run in master's terminal once wrote the person into `{p}:agents` with master's terminal.
  busmon then listed them under Agents, and the dispatcher woke and escalated them. An entry under
  the person's `identity.as` is no agent, whatever terminal it names (person-never-agent): one
  written before that guard from an old holder's terminal no agent shares now once made the
  dispatcher escalate "pane gone" for every report to the person. The dispatcher skips it (it
  reads `identity.as` at start), busmon drops it, and `agentbus agents` leaves it out of the list
  (`agentbus agents <person>` still shows it). A human sender still in the capped cmd stream is
  skipped when its entry borrows another agent's terminal. A leftover entry is removed with
  `agentbus agents forget <person>` (`--force` if its status is recent), or busmon's d.
- **F6 sets an idea's state with one key** (ideas-triage part 2, the person's feedback): on the Ideas list and an idea's page, `y` decided, `l` later, `d` doing, `x` dropped, at once and as the person (the same refusals as the `s` picker, which stays), and `u` undoes this busmon's last change once (a compare-and-set from the state it set). There, `y` is no longer copy: `Y` copies the whole idea (title and text); every other page keeps `y` = copy, and `l` stays the Services page's log. Copy and the log are now page-offered keys (`pageOffers`), so no binding shadows another.
- **Ideas have a fifth state, `later`** (ideas-triage): kept for another time, neither decided nor dropped, between doing and dropped in the order of life (`agentbus idea set <id> later`, F6's picker and tab filter). Each F6 row now says its state in the state's colour, and an idea's text wraps to the page instead of being cut. An older busmon or agentbus lists a `later` idea under no section and refuses `later` as a state: update both on every host that triages.
- **A person named `anonymous` is nobody too (identity-anonymous-human, 2026-11-17).** With `AGENT_BUS_AGENT` unset,
  `AGENT_BUS_HUMAN=anonymous` or `[identity] as = "anonymous"` in config.toml no longer passes as the person's
  identity: a write from the shell is refused with the fix, as with no identity at all (reviewer-codex on #571).
- **busmon's settings page chooses `person.language` from a list** (language-picker): Enter on "Your language" opens the ISO 639-1 table under the row, each entry its code, its own name and its English name (`fr  Français (French)`), the current value under the cursor; typing filters with the command palette's ranking (`fr`, `fran` and `french` all find it), and only a list entry can be written. `agentbus config set` and a hand edit of config.toml are still judged by the key's own rule (refused, or `invalid` and `en`). Any setting given `Options` in `config/keys.go` gets the same list.
- **busmon's light theme paints its own background** (white) on every cell, so `display.theme = light` reads on a dark terminal too; dark keeps the terminal's background, and 16-colour or `NO_COLOR` never paint. Each role keeps a minimum contrast in both themes (4.5:1 for text and muted, 3:1 for the rest but frame lines; `theme_contrast_test.go`): Dim, the dark inactive tab and xiaomi's light colour were raised to meet it.
- **Login expiry, part 1: the reader and the identity (login-updates r9 §3.1-3.3, `loginexpiry/`).**
  No behaviour changes yet (part 2 runs the check). `loginexpiry.ReadFile` reads one field,
  `claudeAiOauth.refreshTokenExpiresAt` (Unix ms), from `<config dir>/.credentials.json`:
  - the path must be absolute and clean;
  - the file is checked with `Lstat`: a regular file, owned by this user, at most 64 KiB, and the
    same file once opened;
  - it is decoded into a struct holding only that field;
  - a value before 2020 or more than two years ahead is `not a time`, never an expired login.
  What leaves the reader is a time and a reason from one closed set (`invalid path`, `no file`,
  `not a regular file`, `not owned`, `unreadable`, `too large`, `no field`, `not a number`,
  `not a time`, `keychain entry not verified`, `keychain: exit <n>`). No token, other field, byte or
  decoder error is ever wrapped, logged or returned.
  On macOS the reader is `security find-generic-password -s "Claude Code-credentials" -w` (read
  only); it answers `keychain entry not verified` and runs nothing until the service name is
  verified on a Mac.
  An agent's `CLAUDE_CONFIG_DIR` is published with its status and its `bind`, and kept in the
  `claude_config_dir` snapshot field only with a proof, like `client_fp`; `/proc/<pid>/environ` is
  never read. The Claude profile declares `[login] source = "claude-credentials"`; no other client
  does.
- **Login expiry, part 2: the check and the warning (login-updates r9 §3.4-3.5,
  `dispatch/loginexpiry.go`).** The dispatcher reads each Claude Code agent's expiry with
  `loginexpiry.Read`, from the agent's own `claude_config_dir` (`~/.claude` when unset): once an
  hour, once a minute for a directory whose login has expired, and once per pass for agents
  sharing a directory. Only clients whose profile declares `[login] source` are read (Claude Code).
  Per agent: `ok`, `warn` (within `dispatch.login_expiry_warn`, default `72h`; `0` turns warnings
  off), `fail` (passed) or `unknown` with the reader's reason. A `warn` tells the person once per
  agent and expiry (`login-expiry:<agent>`, episode the expiry in ms):
  `coder's Claude login expires Thu 15:40 (2 days): run /login in its pane.` The new `login-expiry`
  health check (every 5 minutes) reports the loop's last results, worst first, and reads no file;
  it never notifies the person. An expired login holds its agent (part 3, `login-expiry-3a.md`).
- **Login expiry, part 3a: an expired login holds its agent (login-updates r9 §3.6,
  `dispatch/loginhold.go`).** On `fail` the dispatcher writes `hold-login:<agent>` in `{p}:quota`
  (fenced, its value the expiry in ms). While that field exists the agent gets no wake. One agent
  is held, not the provider, so other Claude agents keep working.
  - **What it had:** an agent with work (an accepted task, or `working`) gets a `{p}:held` record:
    provider `login`, since, tasks, branches, last report. This is run-budget §4's record, built
    here first.
  - **Notices, once per expiry:** the person (`login-expired:<agent>`):
    `coder's Claude login expired Thu 15:40: run /login in its pane. Its work is held (master told).`
    Master (`run-held:<agent>`): `coder is held (Claude login expired Thu 15:40, released after
    /login): accepted wiki-3 (feat/wiki-3). Reassign it, or wait.` Master's own expiry sends no
    directive, and the person's notice names master.
  - **Release:** the next read after /login (within a minute) finds a later expiry still ahead.
    The agent gets `Your Claude login is renewed. Work interrupted by it: …`, then its `{p}:held`
    field and its hold field are deleted, the hold field last. The same expiry, an older one, or an
    `unknown` read keeps the hold. An agent swapped to a client with no login expiry is released
    once the swap is over, with no resume message (the swap resumes it). `retire --forget` deletes both.
  - **Readers:** `bus.HeldReason(marks, agent, provider)` (`quota`, `login` or `""`) is read by:
    the dispatcher's wake skip, its clear and its pop-end judgement; `CannotAct`, whose reason now
    reads `master is held: its Claude login expired`; and team-stall (`t1 (coder, login held)`),
    quiet-agent, idle-implementer and idle-master (`held: its login expired`).
  - **busmon:** shows `coder: Claude login expired Thu 15:40, held` under the Accounts cards and in
    the agent's detail.
  - **Self-repair and pop facts (part 3b):** self-repair holds a login-held agent (hold kind
    `login`, "its Claude login expired: /login in its pane releases it") and never relaunches it, since
    a new process would hit the same expired login. `ReadPopFacts` gives `LoginHeld`, so a login-held
    pop is never judged done ("its login expired (held)").
- **An expired Claude Code login is told to the person (login-updates 2.1/2.4, `quota/claudeauth.go`,
  `dispatch/loginnotice.go`).** The dispatch service reads Claude's transcripts (`CLAUDE_CONFIG_DIR`,
  else `~/.claude`; `projects/*/*.jsonl` touched in the last 6 h, 256 KiB tails), at most once a
  minute: the newest entry the client wrote for an auth failure (`isApiErrorMessage`, `error:
  "authentication_failed"`, model `<synthetic>`, all client-set) with no served answer newer in any
  session means expired; a `/login` in any session clears it. One notice for the person (`for=person`,
  mark `login-expired:claude-code`, episode the host's local date of Redis `TIME`): at most one a day
  per project, sent even while paused, naming no agent (the login is the account's). Nothing else
  changes: no hold, no retry, nothing typed; an expired agent still costs one failed turn per wake.
  No credential file is read. Codex, Grok and agy (screen fixtures) and Claude's pre-expiry banner
  are later slices.
- **busmon says when a newer Codex is published (login-updates 4, `cmd/busmon/codexrelease.go`).**
  Beside the release check, under the same switch (`updates.check`, `AGENT_BUS_NO_UPDATE_CHECK=1`):
  the installed version is the oldest `client_fp` label among the project's Codex agents (no Codex
  agent, or no label: no request); the published one is `version` from
  `https://registry.npmjs.org/@openai/codex/latest`, asked at most once a day (cache
  `${XDG_CACHE_HOME:-~/.cache}/agentbus/client-codex.json`, on macOS `~/Library/Caches/agentbus/`,
  3 s, a 1 MiB answer at most, silent offline and cached for the day; only a plain version
  `N.N.N[-pre]` is kept or shown, from the registry, the cache or an agent's label), re-judged every hour in the background, never on the UI path. Newer: the header shows
  the whole line, `codex <new> available (installed <old>): npm install -g @openai/codex@latest`,
  after the release item and the first to go when space is short, and About's THIS BUILD a
  `codex` row with the command as its note. Nothing installs it; after the person does, the client
  fingerprint flow reloads the Codex agents. No line for Grok and agy (no version endpoint) or Claude
  Code (it updates itself).
- **`agentbus checkback <duration> "<what>"` (master-idle §4, part B).** An agent with work in flight off the bus
  (a test run, a build) says when to look again: one field per agent in `{p}:checkback` (a runtime kind), `{due_ms,
  what, set_id, count, first_set_id, norm}` written by a script on Redis `TIME` (`due_ms` = `TIME` + the duration,
  1m to 7 days). The same `what` again (trimmed, case folded: `norm`) adds one to `count` and keeps `first_set_id`;
  another `what`, or `--clear` then a new one, starts at 1. Plain one-line text (`health.SafeText`, no control
  character), at most 200 runes. An agent's write, no lease (as `status`); the identity guard checks set and
  `--clear`, the bare list (`agentbus checkback`, soonest due first, "due 3m ago" once past) is a read. It never
  expires by itself; `ForgetAgent` and `retire --forget`'s fenced script delete it. master's role and skill gain
  the rule: never end a turn with work in flight unless a tracked background task or a check-back covers it. The
  stall guard reading it (idle-master, and the person told about repeated postponements) is part C.
- **What master has not delivered is judged by git alone (master-idle §3.1, part A).** Each merge-queue pass
  (forge and `none` modes, the `gh` path too) reads origin's tips (`RemoteTips`), fetches the base, and writes
  `undelivered` in `{p}:mergequeue`: the approved deciding heads within 72 h that are no tip on origin, not
  dropped, whose plan task is not done, and that `mergequeue.Delivery` does not find in the base. Its test, per
  head, cheapest first: `InBase`; the branch's zero-context patch-id (`git diff -U0 <fork> <head> | git patch-id
  --stable`) among the base's (a squash); every non-merge commit's among them (a rebase-merge, only for a head
  with no merge commit in `<fork>..<head>`). The base's patch-ids are those of the non-merge commits since the
  judged forks, read once per pass; `Delivery` caches them by SHA across passes (a restart recomputes) and drops
  what left every range. Every diff runs `-U0 --no-ext-diff --no-textconv --no-renames` (and `show
  --no-show-signature`): no driver of the repository's config runs. Accepted risk (the person): a one-commit
  head whose `-U0` change equals an unrelated base commit's (the same version bump) reads delivered. A failed
  read fails the pass (the last list kept). `forge.None` (new): `auto` resolves an origin on an unknown host to
  it (was local); `deliver` pushes and prints no page. `agentbus merge-queue drop <head> "<reason>"` (only a
  `delivers` role or the person; a short SHA resolved against the approved heads, ambiguous refused) writes
  `{p}:mergequeue:dropped` (a record kind, `{reason, by, at}` from Redis `TIME`), `undrop` deletes it, the pass
  prunes drops past 72 h in its fenced write; `merge-queue` prints the undelivered and the dropped heads. The
  identity guard checks `drop`/`undrop` (no longer read-only).
- **`idle-master`, the stall guard for master with work in hand (master-idle §2-§5, part C).** A health check
  (every minute, `Handler: master`, never a fail) judging the escalation target: idle, `done` or its `waiting`
  answered (`WaitAnswered`) for `dispatch.master_idle_after` (15m, `0` off: `unknown`, `off`), a fresh observation
  not busy (unseen: `unknown`), no wakeable message (`Obligation(...).Wakes()`: the dispatcher's own wake covers
  it), not paused, its provider not held, and something in hand: the merge queue's `undelivered` heads (a pass
  older than three passes, or none, makes that source `unknown`: named beside other items, `unknown` alone), the
  requests addressed to it (board entries with a request, `requested` or `accepted`; a claimed task is not), its
  check-back once due. An open `waiting` is the person's turn (`ok`). The hook (`idle-master` joined the StallWake ids in `dispatch/health.go`) wakes master
  once per episode (8 bytes of SHA-256 of the sorted item keys `head:`/`req:`/`cb:`) through
  `bus.FencedCmdOnceAt`: mark `stall-master` and `stall-master:at` (the entry's id, Redis time) written with the
  entry in one script, so the cmd stream's cap or `busmon --reset` never loses the wake's time. Same episode, the
  wake at least `master_idle_after` old: one notice for the person (`stall-master-person`); the `:at` missing:
  none, the detail says `wake time unknown`; master cannot act (`dispatch.CannotAct`): the notice at once, no
  wake. On any pass, master's check-back set on the same `what` more than `dispatch.checkback_max` (3, `0`
  never) times is one notice (`checkback-person`, episode master + `what` + `first_set_id`).
- **A delivered PR is a For you row within seconds, and only once both reviewers approved it.**
  `agentbus deliver` refuses a head that two reviewers did not approve (`--single-approval <reason>`
  in an emergency: the reason goes in the request's body and on the bus). After a delivery it marks
  the merge queue due (`{p}:mergequeue:due`, the broker's time). The dispatcher reads the mark every
  5 s and passes at once, the 5-minute pass staying the fallback, so the PR is a row in For you
  before the person can merge it. The queue's base is the project's, as deliver's (the template's
  `[delivery] base`, else `delivery.base`). The reminder popup (`waiting.popup_after`, Shift+C)
  lists merges with the other decisions; 0 turns it off.
- **Dropping a head from the merge queue is master's (broker-hardening step 4, ACL answer e).**
  - `agentbus merge-queue drop` and `undrop` refuse any agent but master, even docs, which
    delivers. docs asks master instead.
  - Master and the person then pass deliver's own check, as before.
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives `{p}:mergequeue:dropped` write to
    master and to the dispatcher (which prunes old drops), never to `agent`. It is enforced only once
    `broker.acl=on` (step 4b, not built yet); until then the CLI's refusal is a courtesy message.
- **The conductor's operations are master's (broker-hardening step 4, ACL answer d).** `agentbus mission
  load`, `retry`, `skip`, `reassign` and `abort` refuse another agent's environment. Master and the person pass;
  `mission status` and `show` stay open to everyone.
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives `{p}:missionops` write to
    master and the dispatcher, never to `agent`, and that is enforced only once `broker.acl=on`
    (step 4b, not built yet). Until then the CLI's refusal is a courtesy message.
- **busmon's header counts what waits in the other projects (project-switch-ux item 3).**
  - **The counter:** "other: N for you · M merge (O)", summed over this broker's other projects.
    N is the for-you rows other than merges, M the merges waiting. It shows only when something
    waits.
  - **How it reads them:** every 2 minutes, 4 at a time, read-only, and only projects whose dispatch
    holds its lease. A stopped project is read on M and R as before.
  - **What counts:** a read that fails, or is older than the period plus its read time, drops out
    of the sum; it never counts as 0.
  - **`O`** opens the project with the most for-you rows as the header counts them (merges apart, then merges), by the same checked switch, so `B` comes
    back.
- **`person.language` (env `AGENT_BUS_PERSON_LANGUAGE`, default `en`) is read by master's role
  skill only, through `agentbus config`.** Master writes to the person in it (reports to the
  person, ask question text, its pane) and to every agent in English; ask choice labels, peers
  answering the person, and the dispatcher's notices stay English. No code translates anything.
  A value that is not an ISO 639-1 code shows as `invalid` in `agentbus config`, warned
  `master writes English`, and resolves to `en`. A change reaches
  master at its next boot.
- **`agentbus mem person` (person-memory step 3).** The person's memory on this host, no project:
  `remember` (reads the quoted entry from the broker `bus.Connect` reaches, then the project's
  history archive), `recall [--json]`, `show`, `list`, `check` (exit 1 on a finding), `forget <id or
  topic>` and `purge [--yes]`. `forget` and `purge` are the person's: `agentcaller.Refuse` turns down
  `AGENT_BUS_AGENT` and an agent's process tree. `purge` names the commits that go and wants a typed
  `yes`. Forgetting an observation also clears any `supersedes` that pointed at it, in the same commit;
  a topic that does not parse can still be forgotten. The store's lock waits only as long as the
  caller's context (`personmem.Store.lock`), and `.gitignore` (`.lock`, `*.tmp`) goes in with the first
  commit.
- **`typed=yes` marks what the person typed** (person-memory step 1, spec `2026-11-21-person-memory-design` §4.3.1): busmon's directives and broadcasts (`Bus.TypedCmd`, `TypedNotify`) and an ask's answer, which also carries `said`, the chosen label or the free text alone. `human=yes` alone means "written in the person's name" (resume-all's summary, busmon's close, the suspend's resume directive, the idea move line, a person's gate close, `PersonNotice`) and is never a source for the person's memory. The subscribe JSON says `"typed": true`, additively within v1. Entries written before this carry `human=yes` only.
- **`person.name` (env `AGENT_BUS_PERSON_NAME`, default empty, group Identity beside `person.language`)**
  is the person's own name, a bus name. busmon writes as `--as`, then `AGENT_BUS_HUMAN` / `identity.as`,
  then `person.name`, and only then `pilot` (busmon-cli-flags item 3, the person's option A; `pilot`
  also names the lease). An invalid `person.name` is skipped, never a refusal to start. The settings
  page shows it as "Your name"; `identity.as` became "Your name on the bus". busmon is its
  only reader.
- **A plan grouped by chantier (plan-chantier).** `docs/plans/<name>.json` may list `groups` once (`{id, title}`, at
  most 12, the id a `bus.ValidName`, the title one line of 1 to 40 characters) and give a task `group`: each named
  group is listed, and a listed group with no task is refused (a typo would hide it). busmon's Plan board then
  draws one block per group instead of NOW, NEXT and LATER: a header (title, a bar of done cells then approved
  rows still in review, `done/total` counting Done only, `in progress: <id>` by `planInHand`'s rule on the group's
  rows, `waits for you: <id>`, the latter kept when the header is too wide; narrow: the title and the count), its
  open rows in board order, done rows folded into one DONE line until `d` shows them under their group. Order: a
  group waiting for the person, then one with a task in hand, then the rest in list order, `Other` (no group), then
  finished groups as one line. Unknown reads and plans without `groups` draw as before. `plan.Parse` refuses
  unknown fields, so an older `agentbus plan load` refuses a grouped plan and an older busmon fails to parse one:
  add `groups` to a plan only once a release carrying this is installed and the dispatch service restarted on it
  (`agentbus --project <p> service restart dispatch`: `agentbus up` leaves a running service whose unit did not
  change in its old process, even when the binary was replaced in place): the service parses the plan too
  (`plan.Load` in the stall checks, review-queue, the notify facts), and a service still on the old binary fails
  those reads (team-stall, review-queue, plan-drift unknown).
- **busmon's Plan view, after the person's two reviews (views-redesign §4.1, §4.2).**
  - **The drawer:** `p` and `P` open it under For you, which takes the whole width with only its
    own rows (`shellDrawer`).
  - **The board:**
    - one header line (the plan, a bar, done/total, what is left and what is for the person), then a
      rule (`planHeader`);
    - only the groups with open tasks are expanded (`▼`), and every finished group folds into one
      line, `▶ N groups done · M tasks`, an item Enter or `d` opens;
    - no blank lines;
    - the rows are aligned (glyph, title, owner, state word, age since the task's newest fact).
  - **Colours:** bars and state words take the state's colour (`planStateWord`, `planGroupState`):
    orange waits for the person, blue in progress or in review, green approved, grey done, red
    blocked. `‖` marks a task waiting on another.
  - **The strip:** the selected task's detail is a strip under a rule at the bottom (`planStrip`).
    It shows at every tier but the narrow one: in the tab through `listState.SetBottom`, and in the
    drawer through `pageRows`. Its last line holds the keys, said once (`planStripKeys`): the tab's
    frame has no footer then, and with a fold selected the strip keeps its rule and its keys.
  - **Sizing:**
    - The tab takes the lower zone's full height (plan-tab-r3): no Activity under it. What arrives
      in Activity meanwhile is counted on its tab, `1 Activity · 3 new` (`tabNotes`, from the feed's
      `seq` when Activity was last drawn, `activitySeen`: never while the idle view or a page
      covers it, `activityOnScreen`); a too narrow bar drops the note first.
      One blank row sits between the tab bar and the header line.
    - The drawer's free height goes to the active tab under it (`fitDrawer`, the layout's
      `Drawer`), but not when that tab is the Plan.
  - **Releases column (plan-tab-r3 item 5):** from 130 columns the Plan tab has a column on its
    right (`withReleases`, the layout's `Side`, a quarter of the zone between 32 and 44 cells,
    after the Plan in the Tab order); `V` shows it below 130 and hides it from 130. Its rows: next
    with the decided ideas, the loaded plan highlighted, then the release tags newest first with
    their date and their plan's planned task count (a plan file holds no completion, so never
    "done"). Enter opens a release's plan read only (`ovRelease`). The source is the project's
    checkout on this machine (`projectCheckout`): `git for-each-ref` on `vX.Y.Z` tags and
    `docs/plans/*.json` decoded leniently, read every 5 minutes at the requests' cadence
    (`readReleasesDue`). A tag `vX.Y.0` closes plan `vX.Y`; a patch closes none. A busmon with no
    checkout here lists next and the current plan only.
  - **Lists:** a `ListItem` with `Wide` is drawn from the gutter. The tab removes a task row's mark
    with `dropCells` (two cells), because `FitLine` can merge the mark with a blank glyph.
- **The plan's writes are master's (broker-hardening step 4, ACL answer b).**
  - **Refused to other agents:** `agentbus plan load`, `link`, `done`, `note`, `ignore` and `clear` refuse
    another agent's environment (`AGENT_BUS_AGENT` other than master).
  - **Still open:** master and the person; `plan show` to everyone. `plan priority` keeps its
    person-only rule.
  - **docs' delivery:** when docs (or any deliverer but master) delivers, its plan link becomes a
    directive to master carrying the command to run (`agentbus plan link <task> --pr <n>`).
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives the `agent` user no write on
    `{p}:plan`, `{p}:plan:facts` or `{p}:plan:ignore`, and that is enforced only once
    `broker.acl=on` (step 4b, not built yet). Until then the CLI's refusal is a courtesy message: an
    agent calling the bus functions directly is not stopped.
- **busmon, after a project switch (M, o; project-switch-ux, PR 1).**
  - **Back:** `B` goes back to the project the switch left. The switch passes `--from=<p>`, so `B`
    toggles between the two.
  - **Stopped project:** when the project you switched to has no running dispatch, a banner under the
    header says "stopped project · read-only", with `S`, `M` and `B`.
  - **`S` starts its services:** it runs `agentbus service start` (dispatch, then missions), after a
    y. The agents stay down. It is the person's act, refused from an agent's environment, as `X`
    is.
  - **The stopped project is read-only:** while the banner shows, every write is refused with
    "stopped project: read-only, S starts services". That covers sends, answers, priorities,
    ideas, forget, resume, agentbus runs, settings, the notification set-up (notify.toml and
    config.toml's notify.*), the palette's follow, attach and the Projects page's delete. `S`,
    its y/n and navigation still work. It lifts as soon as dispatch runs.
  - **`o` alone still starts nothing.**
- **`agentbus update` removes the old release trees nothing needs** (quiet-update-auto spec §5, the first
  build step). After a successful update, right after the old snapshots, each
  `${XDG_DATA_HOME:-~/.local/share}/agentbus/src/vX.Y.Z` that its clone lists as a worktree is removed
  with `git worktree remove` (never `--force`) unless it is the installed version, a kept snapshot names
  it (a rollback needs it), a holder build of it is still on disk (a unit or a binary copy), or it has
  changes (yours: kept and named). The old holders that neither run nor answer are collected first, so a
  forgotten one does not pin its tree. An unreadable fact keeps every tree, a snapshots directory or manifest that cannot be read or decoded included (an older format's snapshot still pins its trees: its own binary restores it). Nothing else under `src` is
  touched. `agentbus update --list` shows each tree and why it is kept.
- **The automatic update's settings** (quiet-update-auto §6, §7, the second build step; the person's answers):
  `updates.auto` (`notify` by default, or `install`), `updates.window` (`03:00-05:00`, local time, may
  cross midnight) and `updates.max_wait` (7 days), the host's, no env variable. `auto` and `max_wait` are
  set in `config.toml` alone (`agentbus config set` refuses them, busmon shows them); the window is also
  editable in busmon's Shift+C, as the person. **Nothing reads them yet:** the updater and
  `agentbus update --auto` come in the next steps, and busmon's help and the registry say so.
- **A reset is written with its day (quota settings step 1, 2026-11-16).** Every reset and time to come is written
  by `quota.ResetLabel`: `today 13:59`, `tomorrow 13:59`, `Wed 18 Nov 13:59` (the year only when it is not this
  one), in the dispatcher's quota messages (master's warning, an agent's, the drain's announcement), busmon's
  account cards and task estimates, and `agentbus quota`'s RESETS column (`in 23h29m (tomorrow 13:59)`).
  Before, a seven-day window's reset tomorrow read `13:59`, as if it were today's. `display.date_format`
  (Settings, Display: `relative` default, `iso`, `clock12`, a sample of each shown) has no environment variable:
  the dispatch service reads config.toml at each message, so a change applies with no restart. The pace sentence
  under a card wraps instead of being cut.
- **Three quota thresholds, per project, read each minute (quota settings step 2, 2026-11-17).** `dispatch.quota_warn`
  (85) warns master and tells agents with work to finish their step; the new `dispatch.quota_drain` ("start nothing
  new above"; unset, it follows the warning: today's behaviour) drains the run, which runs again once every window is
  under it; `dispatch.quota_hold` (95) holds the provider. `warn <= drain <= hold` is a rule: a broken order uses the
  defaults for all three (logged once per episode). A project's own values live in `config.toml`:
  `[project.<name>.dispatch]` with `quota_warn`, `quota_drain`, `quota_hold`, `quota_max_age`, read before the
  project's env file (`AGENT_BUS_QUOTA_*` from the shell at `up`), then the machine keys, then the defaults. The
  dispatch service re-reads them from the files with the readings, every minute, with no restart, and never from its
  own start-time environment; an unreadable `config.toml` keeps the last values. `busmon --delete` (Projects page too)
  removes the project's table. Quota directives and the drain notice, stored and read later, carry the absolute date
  (`resets Tue 17 Nov 13:59`), never "today" or "tomorrow".
- **The holder's reaper waits 2 seconds before taking an exited child it did not start.** It
  reaps the orphans a holder inherits as a subreaper. Taking any zombie at first sight raced the
  process's own commands (os/exec), whose `Wait` then failed with `waitid: no child processes`.
  In the test suite, where the release test runs a holder in the same process, that was the
  intermittent failure of git-running tests (`TestPlanDriftRealGit`, `TestMissionCLIRoundTrip`
  and others). An orphan is now reaped once it has stayed a zombie for the grace, keyed by pid
  and start time; a sweep runs every second as well as on each SIGCHLD.
- **Reboot-resume, part 1: the run record (reboot-resume spec §2, `lifecycle/`).** Nothing is
  judged yet, so behaviour is unchanged. Each project's state directory now holds two local
  files, written 0600 and replaced atomically (a temp file, fsync, rename, then fsync of the
  directory):
  - `run.json` (`{"boot","state","cause","at"}`):
    - the dispatch service writes `running` at its first lease, and `stopped` (cause `signal`) when
      SIGTERM or SIGINT ends it, but only over `running` of this boot;
    - `agentbus up` writes `running` before it starts the services;
    - `agentbus down` writes `stopped` (`down`) before it stops them;
    - `agentbus suspend` writes `stopped` (`suspend`) after its pause record and before the stop,
      and puts `running` back when the suspend is cancelled.
  - `unplanned.json` is the automatic-restart counter, `[]` for now. It is created before the first
    `run.json`. The dispatch service never recreates a lost one; `agentbus up` does.
  The boot id is `dispatch.BootID` (Linux `boot_id`, macOS `kern.bootsessionuuid`). Where it is
  unknown (the BSDs), nothing is written. A failed write is said and never stops the command.
- **After a reboot, the dispatch service judges once (reboot-resume A, with holder hardening H1 and H1b).**
  - **The judgement:** once it holds the lease, before any other loop (`dispatch.Config.Reboot`,
    `internal/dispatchsvc/reboot.go`), the dispatch service reads `run.json` (`lifecycle.Judge`) and records
    the verdict in `{p}:reboot` (fenced, at from Redis TIME). Only then does it write `running`, so a
    stop in between judges again. The verdicts are `intentional`, `suspended`, `restarting`,
    `unplanned` (with `why`) and `stopped` (with `why`).
  - **An unplanned stop** restarts the team only when, in order:
    - `services.unplanned_restart` is on (default on);
    - self-repair is `on`;
    - the holder answers within 2 minutes of the dispatcher's start (H1b: polled every 2 s;
      nothing written, counted or relaunched meanwhile; never for `intentional` or `suspended`);
    - the repository is intact (`repoIntegrity`: no `index.lock` in the git directory or a
      worktree's, `git fsck` exits 0 within 5 min, run with `core.fsmonitor=false` and
      `core.hooksPath=/dev/null`; a directory with no `.git` passes);
    - the count, kept in `unplanned.json` and written before anything is relaunched, stays within
      `services.unplanned_max` (3) in `services.unplanned_window` (24 h).
  - **A failed check or the limit:** the project is stopped by a fenced suspend (`by` dispatch;
    the services keep running). A lost or garbled counter reads `unplanned` (`counter missing`,
    `counter not written`), never empty.
  - **Who is told:** the person, once per boot (mark `reboot`); for a restart, master too.
  - **Self-repair** holds every agent (`HoldReboot`) while this boot's record stands with any verdict
    but `restarting`. It relaunches a reboot death as `BeginRebootRelaunch` (an `r`-prefixed attempt:
    the project's pause, never the role's own budget), and deletes the record once every required
    agent is alive (another boot's at once).
  - **Resume:** `agentbus resume` empties the counter.
  - **H1:** with `services.start_at_login` on, `up` and `service login` enable the current build's
    holder with the other services and disable every other build's (`service.HolderLoginStart`).
  - **Not in A:** busmon's popup (B), `services.start_at_boot` (C), and the retirement of
    `suspend.auto_resume` (with B, which replaces it).
- **A failed relaunch names the holder's command; a launch starts only its own role (recovery-hints, 2026-11-16).**
  A self-repair or reload that fails now says `recover with agentbus --project <p> pop <role> [--profile <p>]
  [--resume <session>], or agentbus up <p> --resume`, never the herdr-era `agent-launch <role> <project>` in a new
  pane, which started an agent the dispatcher cannot wake (followed once, it started three agents as master).
  `agent-launch` refuses a role that is not its environment's `AGENT_BUS_AGENT` (typed in master's terminal it
  launches only master; the holder sets each terminal's own) and a `--resume` session that `{p}:resume` records
  for another role. A `--resume` whose records cannot be read (broker down) is refused; a fresh launch reads none.
- **busmon no longer depends on tcell** (`remove-tcell`): the theme keeps its colours in its own
  `Color`/`Style` (`cmd/busmon/color.go`, tcell's semantics); the goldens are unchanged. `go.mod`
  drops `gdamore/tcell`, `gdamore/encoding` and `golang.org/x/text`.
- **`report --needs answer|read` (triage-dedupe step 1).** A report says it needs the person with the
  field `needs` (`bus.NeedsAnswer`, `bus.NeedsRead`, `Bus.ReportNeeds`); each `--to` notice carries it too
  (`Bus.ReportNotice`), so `subscribe`'s JSON shows `"needs"` (additive in v1) and `agentbus history` prints
  `report (needs answer)`. Any other value is refused before anything is written, and a value a hand
  write put on an entry reads as none. busmon reads it (triage-dedupe step 2, `triage-by-target.md`): a
  report is a triage item only with `needs`.
- **A request nobody can accept can still be closed.** A tracked request whose body cmd retention
  removed (`availability: missing`), or that expired before anyone accepted it, refuses `accept`,
  and `done` needed an accept, so it stayed open forever (concierge-memory-opinion, 98 h). Its
  target, or its sender (usually master), now closes it with `agentbus request done <task> <why>`,
  unaccepted. The board entry records `closed_unaccepted` (`body missing` or `expired`), and the
  reply goes to the other side. A request whose body is there still needs an accept, and the sender
  never completes one its target accepted. The accept refusal names the command.
- **`rerere.enabled` is on in the shared repository, `rerere.autoUpdate` is not.** Master ran
  `git config rerere.enabled true` once (merge-conflicts spec §3, the person's yes of
  2026-11-21). The cache is shared by every worktree, so a conflict resolved once replays for
  the next agent who hits it. A replayed resolution stays unstaged: the agent still reads it,
  stages it, and runs the tests. Never set `rerere.autoUpdate`, and no agent resolves conflicts
  automatically. `git rerere forget <path>` drops a wrong recorded resolution.
- **A swap outside a role's profiles no longer costs its session on resume
  (resume-record-profile).** `agentbus swap --force` may put a role on any defined profile, and its
  SessionStart hook records that profile in `{p}:resume`; `up --resume`, the self-repair and `holder
  drain` used to refuse such a record (the role started fresh, or was not moved) (master lost its context on 2026-11-16). When
  roles.toml defines that profile for the role on the role's own client, the session now resumes, on
  the role's own profile (`rolecfg.ResumeProfile`, which the holder applies to every resume start): the recorded profile is a broker key and still never picks what runs, and
  up says so. A recorded profile of another client is still refused (its session is not this
  client's), as is one roles.toml no longer defines.
- **The run, counted (run-budget §2 and §5, step 1).** `{p}:run` (a hash, runtime kind) is written only by the
  merge-queue pass, after its own write, through `bus.FencedRunPass` (fenced, Redis `TIME`): `started_at`,
  `paused_ms`, `seen_at`, `deliveries`, `state` (`running`; draining and stopped come with step 2), `since`, `why`,
  `drains`, and one `d:<head>` per head counted. Each pass judges every approved deciding head within 72 h
  (`mergequeue.RecentApprovedHeads`; the window on the broker's clock, `bus.ServerTime`, as the approvals' times
  are, `undelivered`'s too, and an unreadable clock fails the pass) with the same git test as master-idle (`Delivery`, its cache shared), in every
  mode, local included, after `undelivered`. No record: the first pass that judged them writes one with every
  delivered head as its baseline, counting none; a pass that could not judge them starts nothing and counts
  nothing (the queue's record is unaffected). Each later pass adds `now − seen_at` to `paused_ms` when `{p}:pause`
  held at the interval's start (a suspend, or a pause whose `until` is past `seen_at`) or when the interval is
  longer than three passes (15 min, a host asleep), and counts each head not yet counted once (`HSETNX`).
  `agentbus run` prints the state, the elapsed time and the deliveries against `run.max_duration` and
  `run.max_deliveries` (registry, `0` = no limit, default) and names a limit reached; nothing acts on it before
  step 2. `agentbus run reset` deletes the record, refused when `AGENT_BUS_AGENT` is set (the person only, as
  `deliver` recognises them: defence in depth); the identity guard reads the bare command as read-only.
- **The run drains and stops (run-budget §3, §4 first part, step 2).** On each merge-queue pass, after the run's
  write: `run.max_duration` or `run.max_deliveries` reached moves `{p}:run` to `draining` (`bus.FencedRunDrain`:
  `drains` + 1, the episode `<started_at>:<drains>`), and master gets one waking directive (mark `run-drain`) and
  the person one notice (mark `run-drain-person`), each by its own once-mark, told again on each pass of that state until written (none twice). While it drains, `request send` is
  refused to an agent (`bus.ErrRunDraining`, in the publish script) and allowed to the person (no
  `AGENT_BUS_AGENT`, as `deliver` recognises them: defence in depth); team-stall reads `ok` (`run draining: ready
  plan tasks wait`); idle-master still wakes master for undelivered heads. A run draining for a limit stops
  (`bus.FencedRunStop`) when nothing is in flight (a requested or accepted task, an undelivered head, an open gate (any gate key, a reviewer offline included),
  any gate key, a reviewer offline included; an agent working) or `run.drain_max` (default 2h, `0` = until nothing is in flight) after the drain began: state
  `stopped`, a kind-`run` pause a year ahead (any pause but a suspend replaced), one notice (mark
  `run-stop-person`) listing what was still in flight if `drain_max` cut it short. A pass that could not read the
  queue stops nothing. No quota release lifts the run's pause; `agentbus run reset` (the person) lifts it with the
  record. Quota, for a client that reports it: at `dispatch.quota_warn` each agent on that provider holding work
  (an accepted task, or working) is told once per window (mark `warn:<p>:<window>:<agent>`) to finish its step,
  commit its work on its branch, report what is left and start nothing new; master's provider drains the run
  with `why = quota:<p>` (announced like any drain, never stopped by the pass: the quota's own pause holds the
  project), and the run goes on when that provider is clear (every window under the warn threshold, no hold) or
  its hold is released; a limit reached meanwhile replaces `why`, and the run then stays draining. Not in step 2:
  `{p}:held`, the held directives and the silent client's screen hold (`run.screen_hold_max`).
- **The dispatch service's escalation target reaches every check (service-escalate-to).** Under
  `agentbus service run dispatch --escalate-to <agent>` (or `AGENT_BUS_ESCALATE_TO` in the project's env
  file), the stall checks (team-stall notice, clear-stall, review-queue, idle-implementer, plan-drift,
  idle-master), the self-repair watcher and the reboot judge now escalate to that agent, as the loop
  did; before, they still went to master. The foreground `agentbus dispatch --escalate-to` was not affected.
- **busmon's Services page lists the project's holders, and `service stop holder <build>` stops
  an old one** (agent-screen-blocked §4, the person's Q5).
  - Each holder line shows its build, whether it runs (`running`, `not answering` when its unit
    runs but its socket does not answer, `inactive`), its uptime when known, and how many agents
    it hosts. They are read on busmon's own machine only.
  - A holder of another build than the dispatcher's that answers with no agent is flagged
    `old build, hosts no agent` with the commands:
    `agentbus --project <p> service stop holder <build>`, then `service gc`.
  - The stop refuses the current build's holder and a holder with a live terminal (`holder
    drain` moves them first), one that answers but cannot be listed, and one whose unit runs but
    whose socket does not answer (it may hold agents; `agentbus down <p>` stops every holder of the
    project, its agents with them), and one whose unit state cannot be read. It never kills an agent;
    `service restart holder --kill-agents` does.
  - When no holder of the dispatcher's build answers, the page says so and names `agentbus up`.
- **A service's log page reads (services-restart-buttons step 1).** F3, Enter on a card: each line has a level (a
  dispatch `escalate`, `blocked` or `unblocked` is attention, `none` muted; a text line saying `failed`, `error` or
  `panic` is an error), a decision reads `action  agent  reason  (rule N)` (the raw file keeps `high_id` and
  `latency_ms`), and the routine `none` decisions are folded (`a` shows them; the title counts them). `f` keeps the
  lines holding a word, Esc clears it. The page reads the log back, `.1` after it, until 200 lines that are not
  routine are found, within 8 MiB. The dispatcher now writes `"ts"` (RFC 3339, local zone) on every line it logs;
  older lines show no time.
- **Restart a service from busmon (services-restart-buttons step 2).** F3, on the dispatch or missions card: `X` asks
  `Restart <kind> of <p>? agentbus --project <p> service restart <kind>. y / n`; `y` runs that command as Z and the
  Projects page's `x` run agentbus (one run at a time, the output in the project's log, its last line the notice),
  and the card is read again when it ends. busmon refuses before asking in an agent's environment
  (`AGENT_BUS_AGENT`) or under a registered agent; the `agentbus service restart` command itself still accepts every
  caller (the sentinel's repair). Refused too: a service on another host (`restart it on <host>`), one not installed
  here, a run already going, and the Redis card (setup's job). Holders stay read-only.
- **`agentbus deliver --dry-run` prints the change's size class** (single-approval step 1, spec
  2026-11-21-single-approval-design §3.2): from `git diff --no-renames --numstat -z` against the
  merge-base with the base, `size: small: N lines, M files`, `small (docs only)` (every path
  `docs/**` or `*.md`; within the same limits, the person's answer to §6 Q2), or `not small (<reason>)`: over the project's `small_max_lines` or `small_max_files` (150, 5), a binary
  or unreadable row, or a never-small path (`bus/**`, setup, broker and ACL files, secrets, CI,
  `skills/**`, and the gate itself: `deliver.go`, the classifier `deliversmall.go`, the rule `deliversingle.go`, its glob matcher
  in `filepick/`, `mergequeue/**`, `config/**`). The single-approval rule reads it
  (`single-approval-2.md`).
- **One approval for a small change, per project** (single-approval step 2, spec
  2026-11-21-single-approval-design §3.1, §4; the person's §6 answers).
  - **The settings**, in `config.toml`'s `[project.<name>.delivery]` (then `[delivery]`, then the
    default; never an env variable): `single_approval` (`off`, or `small`), `small_max_lines` (150),
    `small_max_files` (5) and `same_provider_ok` (`off`, `docs`, `small`) are the person's:
    `agentbus config set` refuses them. `same_provider_ok` is read from the project's table only (a
    `[delivery]` line is ignored with a warning). `never_small` (globs added to deliver's defaults) only
    narrows the gate, so agents may set it.
  - **With `single_approval = "small"`,** `deliver` accepts one current approval on the head when:
    - no reviewer's latest verdict on the subject is a reject (`reject` or `changes`), on any head:
      an objection is answered by the one who raised it (§6 Q8);
    - the change is small (`size:` in `--dry-run`);
    - the approver is of another provider than the author (an empty provider is never "another"), or
      of the same one and the change is in `same_provider_ok`'s class: `docs` (every path `docs/**`
      or `*.md`) or `small`.
  - **The PR body and the bus** carry `single approval (small: N lines, M files; approved by R)`, or
    `single approval, same provider (small docs: …)`; `--dry-run` prints `approval: …`.
  - **Refusals** say which rule failed. Above the limits, two approvals or the person's yes with
    `--single-approval "<the person's yes, ask <id>>"`, as before. With two approvals nothing is read.
- **A stuck screen rings the desktop once per episode** (`dispatch.stuck_after`,
  `AGENT_BUS_STUCK_AFTER` in whole seconds; default 3 minutes, `0` off). A screen counts as
  stuck when it is settled, its client's signature reads it as neither ready, nor busy, nor a
  prompt, and it has not changed for that long in the same process. The ring says
  `<project>: <agent>'s screen has not changed for <d> and is not a prompt agentbus knows
  (<terminal>)`, never "waits on a prompt". It follows prompt-notify's suppressions (a declared
  blocked or waiting, an open ask, a swap, the pause), under its own mark `stuck:<agent>`. A
  client update that changes the idle screen can make several agents of that client stuck at
  once, one ring each. The observation gains `screen_since`, the time of the first reading of
  the current screen; no screen text is written (agent-screen-blocked §3).
- **The Tokens page (U) reads its 7-day column per row** (tokens-layout): each project's sparkline is scaled to its own busiest day, so its shape shows, while the bar beside it compares projects; the right-hand number is the seven days' in+out (`7d total`), and the in/out columns are today's. The sparkline is 14 cells of daily data: `{p}:tokens` keeps one row per day, so a 6-hour resolution would need a bus change, which waits for the person. Bar rows are one line with a blank line between them, and an account window's name (`7d-fable`) is shown in full.
- **For you holds one item per target (triage-dedupe step 2, `foldLines` in `cmd/busmon/tea_foryou.go`).**
  A merge item is keyed by its PR (`merge:pr:<n>`, else `merge:branch:<b>`), so a rebase keeps it; its
  page still opens by head (`attentionItem.Head`). A line naming exactly one live target (`#<n>`,
  `PR <n>`, a merge item's branch, an ask's id) attaches to that item (its mark, and "needs your answer"
  or "to read" when the line is a `--needs` report); a waiting agent's own newest report folds into its
  wait row. A report is an item only with `--needs`: naming no single target, its own `report:<id>`
  (rank `rankReport`, verb answer or read), replaced by the same sender's newer one naming the same
  targets; `answer` stays until the person writes to its sender, `read` until it is read. A plain
  report is Activity only. The header's unread badge counts Activity's unread marks
  (`activityUnread`), apart from For you's items.
- **An item's page ends with its Context (triage-dedupe step 3, spec §5).** The page of a For you
  item (a merge, an ask, a gate's thread, a waiting agent, a `--needs` report) lists, under its own
  fields, the messages attached to it: a report, directive or notice naming it (#<n>, PR <n>, its
  branch, the ask's id), a
  waiting agent's newest report, or a reply on its thread (its ref is the ask, the gate's challenge
  or the report). Newest first, at most 3, each "<n>  <sender>  <age>  <headline>"; a `--needs`
  report folded into the item comes first and says "needs your answer" or "for you to read"; more
  are counted ("n older in Activity"). `1`-`3` open one in full, and a click does; on an ask's page
  the digits choose an option, so only a click opens them. A page with nothing attached has no
  section. An agent page that is no waiting item's shows none. A page opened from its item keeps it, so
  a `--needs read` report read by opening it still shows its replies.
- **`T` opens a summary of the actions (triage-dedupe step 4, spec §4).** "Triage: N actions", what the
  walk did (answered, sent, read), one line per item in For you's order (as its row: kind, who, text,
  age), then the next plan task. ↑↓ choose, Enter opens the item's page above the summary, the walk at
  that item: `n`/`p` move through the items, a terminal act moves on by itself. Esc on an item's page
  returns to the summary; Esc on the summary ends triage. After the last item, or when the item on
  screen goes (its ask answered elsewhere), the walk returns to the summary, which replaces the end page
  (no skipped count: what still waits is listed). Enter on the plan line opens that task.
- **A project folder without Git starts without missions (up-git-repo, 2026-11-16).** A project needs no forge and no
  repository (the person's answer). Only the missions service (the conductor) needs Git, and it used to wait for
  one out of sight (`no Git repository in <dir>: run git init there`, seen only in its state). `agentbus up` (and
  busmon's picker, which runs it) now checks the template's `working_dir` and, without a repository, starts the
  project without missions and says once: `missions: not started: no Git repository in <dir> … To enable it: git
  init <dir>, then agentbus up <p>`. A missions service an earlier up left waiting is stopped. It never runs
  `git init` itself.
- **An install names the project services left on the previous build, and restarts them on your yes
  (update-other-projects, 2026-11-16).** Each dispatch and missions service records the build it runs
  (`build` in `{p}:services`, `holder.BuildID`). After `setup.sh install` or `update`, setup asks the installed
  `agentbus service stale` which of this host's running services recorded another build (a record from before
  builds were recorded, or of another process, counts as older), lists each with its
  `agentbus --project <p> service restart <kind>`, and asks once `Restart these N services now? [y/N]`. `--yes`
  and no terminal restart nothing. Holders and agents are never touched. Seen 2026-11-16: three other projects
  kept the old binary after an install. The services started before this build list as
  `no build recorded` until restarted once.
- **busmon's IDEAS lists the idea notes too (views-redesign step 1).** Beside the broker's ideas (`agentbus idea`),
  Work's IDEAS and the Kanban's Ideas card list the notes of `docs/memory/open-questions/` still `proposed` or
  `active` (`memory.OpenQuestions`), read in the project's checkout on this machine: its template's `working_dir`,
  at the ideas' cadence. A busmon with no checkout (a laptop) lists the broker's ideas only. A note shows once: not
  when a plan task names its id or title, nor when a broker idea has its id, its title or the note as its `source`.
  A note's row says `note`; Enter shows its path (its triage panel is step 5), `y` copies its title. Notes that
  cannot be read are said ("the idea notes could not be read"), never "none".
- **busmon's For you no longer says "Nothing expected from you." while decisions wait (views-redesign step 2).**
  With nothing owed but the person's plan tasks to decide (Work's decide rows), the empty For you panel's first line
  reads `N decisions in the plan (3 Work)`, attention role, and the "Next for you" pointer stays under it; an owed
  request keeps its `Owed by you: N requests (Requests)` line, above. "Nothing expected from you." only when nothing
  is counted. The Kanban's For you cards drop the repeated `decide · you`: the first line is the glyph and the title
  (a merge card's `merge PR N`), the second the age and the place (who asks, `#<plan task>`).
- **busmon's Board flags a late or lost tracked request (views-redesign step 3).** One line per task (TASK, OWNER,
  STATE, AGE), the alerts first, oldest alert first, then the open tasks newest first; done tasks are hidden, `d`
  shows or folds them, and the label says `N open · M done (d)`. An ALERT column appears only while a row has one:
  `no acceptance <age>` (requested, not accepted past `board.no_accept_after`, default 30m), `no reply <age>`
  (accepted, no reply past `board.no_reply_after`, default 2h), `body lost` (the request's body no longer in the
  stream, at once); both settings are in Shift+C's Display group, `0` off, applied at once. Enter on a tracked
  request opens its diagnostics (the request's page), on a plain task its task page. The frame's bottom border names
  who acts: `resend (dispatch)`, `cancel/reassign (master)`; busmon changes no request itself. With master's
  terminal gone (or no master entry), an alert row says `master is down; self-repair relaunches it` and
  cancel/reassign leave the footer. Work's `between agents` line still opens the Requests sub-view (its retirement
  waits for the person's decision on the untracked threads, notices, asks and their keys).
- **One Requests view, reached by `r`/`F2` from anywhere (views-redesign step 3b, the person's choice (a)).** `r`
  and `F2` now open the Requests sub-view (every thread: tracked requests, untracked threads, notices, directives,
  open asks, with `d`, `o`, `+`, `-`) from any panel, the Board included; the former read-only Requests page is
  retired. The sub-view is drawn under the tab it was opened from, and Esc, the tab bar and `←`/`→` go back to that
  tab; `d`, `o`, `+` and `-` do the same whatever opened it. Work's `between agents` line now opens the Board
  filtered to the rows between agents (no request to or from the person), its label `between agents · …`; Esc
  returns to Work on that line, and leaving the Board drops the filter.
- **busmon's Plan is the fourth tab, a roadmap (views-redesign step 4a).** `4` (`6` at the narrow tier) is the Plan
  tab, beside Activity, Board and Work (`←`/`→` step across the four); it is no longer an alias to Work's NEXT. `p`
  and `P` still open the same roadmap as a drawer. The tab's frame names the plan and its progress, never a for-you
  count; `d` shows DONE; Enter opens a task, Esc returns to the tab; `r` opened from it returns there. Sections: NOW (in progress, in review, answered,
  blocked), APPROVED (both reviewers approved, waiting for delivery or the merge; folded when empty), NEXT (not
  started and unblocked, the person's decisions among them, read `decide`), LATER (blocked by another task, then the
  low-priority tasks, blocked or not), DONE as a count. NOW and NEXT sort by priority, then file order. A plan task
  may carry `"priority": "high"` or `"low"` (`"normal"` and absent are the same); the row's first cell is `↑` or `↓`,
  else the state's glyph in NOW and `✓` in APPROVED. A task's dependencies read `after <first> +N`; the side panel
  lists them (`✓` when done), the tasks that wait on it (`unblocks`) and its priority. Below the wide tier (under 120 columns,
  no side panel) LATER and DONE fold to a line Enter opens (`LATER 3 · 1 ↑ (enter)`), in the tab and the drawer. A grouped plan keeps its groups, each group's rows in
  the roadmap's order. Raising or lowering a plan task's priority from busmon (`+`/`-`) is step 4b, not built.
- **A plan task's priority is the person's, on the broker (views-redesign step 4b).** `agentbus plan priority <task>
  high|normal|low --as <person>`, or `+`/`-` on a plan task in busmon (the Plan tab, the drawer `p`, a task's page),
  records it with the plan's facts in `{p}:plan:facts` (one script; `normal` is stored too, so it wins over the plan
  file's `priority`; the task's fact time is kept, since a priority is no progress; a reload keeps it while the task
  stays in the plan). The Plan reads the person's level over the file's. An agent is refused, as for `request
  priority`, and `roles.toml` denies `Bash(agentbus plan priority*)` to agents as a second layer. `request send`
  starts the request for that task at its level, in the same script: the plan task is the one whose `board` field
  names the request's task, else the one with its id and no `board` field. A request already sent keeps its own
  level (the command says so and prints the `request priority` line that moves it; nothing for a done request). The
  v0.29 plan's three `LOW:` titles are now `"priority": "low"`: a running plan takes it at its next `plan load`.
- **A title no longer hides an agent's screen (vt-osc-title, 2026-11-16).** x/vt v0.1.0 honours 8-bit C1 controls
  inside a string sequence: a byte 0x80-0x9F in an OSC, DCS, APC, PM or SOS payload ends or breaks it, and the rest
  is printed. Claude Code's turn-end title `✳ <project>:<agent>` (E2 9C B3, 0x9C is ST) landed on its input line,
  its ready signature failed, and the dispatcher deferred `agent unknown` until someone attached. `screen`'s
  `stringFilter` (strfilter.go), before x/vt and stateful across chunks and writes, drops every OSC 0/1/2 title
  and every string sequence whose payload holds such a byte (OSC 9 and 777 notifications with a glyph, an APC or
  DCS); everything else passes byte for byte. The holder tells every terminal it starts `TERM=xterm-256color` and
  `COLORTERM=truecolor` (what x/vt emulates), after its own environment and before the role's (a role's `TERM`
  wins): a service runs with `TERM=dumb`, and Claude Code under it drew its dim placeholder and prompt suggestions
  unstyled, which the ready signature reads as typed text. Agents started before the fix keep `TERM=dumb` until
  they are started again. The health check `screen-unknown` (For you, `agentbus attach
  <agent>`) names each agent declared idle whose screen the dispatcher has read `unknown` for 10 minutes
  (`health.ScreenUnknownAfter`), among the clients with a ready signature only: such an agent cannot be woken.
- **`board.wip_limit`: the work-in-progress limit's setting** (wip-limit r2 §3.3; the person's answers):
  requests an agent may hold accepted when it starts a new one, default 2, 0 no limit; per project
  (`[project.<name>.board]` over `[board]`), no env variable. **The person's alone, from busmon's
  Shift+C or `config.toml`:** a new `PersonEdit` flag makes `agentbus config set` and `unset` refuse it
  ("set it in busmon (Shift+C) or config.toml"); busmon's settings page writes through
  `config.SetAsPerson` / `UnsetAsPerson`, which no other package may call (a test scans for them).
  `PersonOnly` keys (`backup.after_*`) stay read-only in busmon. **Enforced by `agentbus request accept`:** a first
  accept is refused while the agent holds that many requests `accepted` (naming them). A resume, and a first accept of a request the person marked `high`, are never
  refused; once accepted, a `high` or resumed request still counts as held, like any `accepted`
  one. A request in `review` or `blocked` is not `accepted`, so it does not count. `agentbus request
  review <task> [--branch <b>] [<note>]` marks a step in review. `request send` to an agent at its
  limit is queued with one line on stderr naming what it holds and the agents under their limit. The
  row's detail text in Shift+C says what the value implies. **It is on by default (2):** upgrading
  enables the limit at once; `0` turns it off.
