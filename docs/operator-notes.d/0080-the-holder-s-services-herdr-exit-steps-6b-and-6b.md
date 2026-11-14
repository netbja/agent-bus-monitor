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
