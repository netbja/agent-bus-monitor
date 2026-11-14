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
