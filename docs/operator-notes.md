# Operator notes

The "Things that bite" section of [CLAUDE.md](../CLAUDE.md), moved here unchanged on 2026-11-06
(claude-md-split). The architecture is in [architecture.md](architecture.md).

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
  master may run `agentbus up agent-bus-monitor --resume` without a prompt, `roles.toml`). A tab's command counts as gone only when
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
  (Linux only, like every verified identity: `/proc`; on macOS no agent is verified, so nothing is proved).
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
  session replaces it. No chain (macOS) records nothing. It is a backup record and a project kind. The hook
  never prints on stdout and always exits 0 (spec
  `docs/superpowers/specs/2026-10-21-launcher-resume-design.md`). Units always run `buildinfo.Bin` (setup's
  `$BIN_DIR/agentbus`) when it exists. `agentbus projects` and `agentbus identity` sit above the
  project-required exit. `missions` without a Git repository publishes `waiting-git` and waits.
  **Trust gate** (`trust.go`, specialist roles spec part B): when `up` opens a workspace whose
  folder a client (Claude Code, Codex) does not trust yet, it starts the first agent of that client
  alone, says to answer the client's own dialog in its pane, polls the trust (2 s, 10 min) and only
  then starts that client's other agents; a timeout starts nothing more and keeps the workspace
  (`errTrustPending`); its message names the pane to answer in (A10). The readers (`claudeTrusted`, `codexTrusted`) read only the trust flag, keyed
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
- **The holder's services (herdr exit step 6b, `service/holder.go`; not wired yet).** One unit per
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
  `llm-endpoint` (step 2; `health/llmendpoint.go` pure, `cmd/agentbus/llmcheck.go` the reader, every minute) probes
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
  `cmd/agentbus/redislatest.go`) compares the broker's INFO version with the latest Redis release
  **of its own major** (the major it runs, read from INFO; no claim about the compose tag): the dispatch
  service's `RedisRefresh` chore reads GitHub's recent `redis/redis` releases (5 s, drafts and
  prereleases skipped) once a day under a host-local lock into `~/.cache/agentbus/redis-latest.json`
  (major -> newest release, 0600; a failed fetch moves only `checked_at`, silently); warn when behind
  in its major; a newer major is never a warning, only named in the detail (changing major is the
  person's decision); unknown when the broker is unread, no release of its major is found or no check
  succeeded within 48 h. The person updates the broker; sentinel never restarts or upgrades it. `disk` (disk-check,
  `health/disk.go` pure, `cmd/agentbus/diskcheck.go` the reader behind injected `diskDeps`, so no test touches a real disk;
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
  (busmon-github-check, `health/github.go` pure, `cmd/agentbus/githubcheck.go` the runner; 15 min, 10 s) runs only
  when a remote is on github.com (origin first): `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com` and
  `git -c credential.helper= -c core.askPass= ls-remote https://github.com/<o>/<r>` with `GIT_TERMINAL_PROMPT=0`, in parallel, every command
  under `LC_ALL=C` and without any askpass program (`probeEnviron`: `GIT_ASKPASS`/`SSH_ASKPASS` removed, `SSH_ASKPASS_REQUIRE=never`;
  github-askpass: the terminal switch alone left KDE's ksshaskpass asking for a username every 15 minutes) (the answers are read by git's English messages); any answer from GitHub is reached (auth unknown),
  matched before the network errors since it may quote a repository named like one (TLS by its own phrases), only a network error failed, an unrecognised answer or a missing binary not
  judged. Both reached ok, one warn (for you), neither unknown then warn after `GitHubNeitherWarnAfter` (1 h, tracked
  in the check's memory: a restart starts it over); never fail, so it never wakes sentinel. The stall guard (spec
  `docs/superpowers/specs/2026-11-01-stall-guard-design.md`; judgement pure in `health/stall.go`, readers in
  `cmd/agentbus/stallchecks.go`; `dispatch.stall_after`/`clear_stall_after`/`quiet_after`, 15 min, 0 = off):
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
  `docs/superpowers/specs/2026-11-04-review-queue-design.md`; `health/reviewqueue.go` pure, `cmd/agentbus/reviewqueue.go`
  the reader) warns (master) when the same idle team (`teamIdle`, shared with `team-stall`), nothing else open (gates
  aside), owes a review: an open gate whose challenge the reviewer read, older than `stall_after` (a challenge no longer retained is
  never proved read, a group skipping trimmed entries: not judged, unknown when nothing is owed); or a subject approved
  within 72 h (fixed) and not done in the plan, where a reviewer who engaged with it (a verdict on it, an open gate on
  it, or a challenge under a ref a verdict on it carries) has no approve of the deciding head; the `StallWake` hook
  wakes master once per owed set (mark `stall-review`, episode a hash of the owed keys); never fail. `idle-implementer`
  (spec `docs/superpowers/specs/2026-11-08-idle-implementer-design.md`; `health/idleimplementer.go` pure,
  `cmd/agentbus/idleimplementer.go` the reader; `dispatch.idle_after`, 15 min, 0 = off) warns (master) for each
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
  alert only, the person approved; `health/plandrift.go` pure, `cmd/agentbus/plandrift.go` the read) warns master,
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
  archive opens its coverage, the restore dry runs to copy (`y`, the path shell-quoted) and what takes each project offline first (`agentbus down <p>`, its herdr workspace closed: a suspend keeps the agents and the workspace, which restore refuses even with `--force`; `backupOfflineSteps`); busmon restores nothing. Health `backup` (`health/backup.go` pure, `EvaluateBackup`; `cmd/agentbus/backupcheck.go` the reader; 15 min,
  for you, `agentbus backup`): per project, warn on no covering archive younger than `backup.warn_after` (168h, `0` =
  this reminder off), on the latest scheduled attempt covering it failed while `daily` (cleared by a newer covering
  archive; the age case only by one with secrets, read from `coverage.json`'s `secrets`), on its latest
  `after_command` run failed while one is set; unknown when the folder or the record cannot be read; the detail names
  the folder.
- **Notifications** (spec `docs/superpowers/specs/2026-11-03-notifications-design.md`, package `notify/`): the
  dispatch service's chore (`cmd/agentbus/notifychore.go`, `dispatch.NotifyConfig`, 30 s, only while
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
  collected_at; unknown/unlimited) and readers — Claude via `agentbus statusline` (Claude Code's
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
  row. Any `agentbus models` form refreshes a table whose last attempt is 24 h old first: under
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
  the title and the rest its text, s: the state picker, y sets it) and flags a state set by someone
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
  older head), never silent. Sources: `gh pr list --state open --json … --limit 1000` (`ReadGitHub`; a list of exactly
  the limit is `ErrIncomplete`, a pass read as unknown), else the git fallback (GitHub without `gh`, GitLab and
  Bitbucket for now, local): `RemoteTips` (`ls-remote --heads`), `FetchBase` (`--no-write-fetch-head --no-tags
  --refmap=`: the empty refmap stops git moving `refs/remotes/origin/<base>`; no ref written, tested byte for byte),
  `InBase` (an ancestor, or no diff: "contents in base", a squash or a revert); local mode reads `LocalTips` (a local
  branch whose tip is the head; no origin with `delivery.forge = auto` is local, as for `deliver`). Every command runs
  with `mergeQueueEnv` (the github check's `probeEnviron`: no askpass, `GH_PROMPT_DISABLED=1`, `GIT_TERMINAL_PROMPT=0`,
  `LC_ALL=C`), 10 s each; agentbus reads no token. The dispatch service's chore (`cmd/agentbus/mergequeuechore.go`,
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
