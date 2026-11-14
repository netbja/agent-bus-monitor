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
