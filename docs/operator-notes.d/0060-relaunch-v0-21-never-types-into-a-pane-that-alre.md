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
