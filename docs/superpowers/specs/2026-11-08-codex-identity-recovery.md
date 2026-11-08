# A restored pane recovers by itself (codex-identity-recovery)

Status: implemented (subject `codex-identity-recovery`). The person, 2026-11-08: after a reboot herdr
restored reviewer-codex's pane (w2:p14); the dispatcher escalated `reviewer-codex: identity
mismatch` for about 1h30 with 9 messages waiting, and nothing woke it until the person typed into
the pane. Claude Code panes restored by the same reboot came back fine.

## 1. The cause

The dispatcher trusts a pane only when the pid the agent registered (`{p}:agents`, `PID` and
`PIDStart`) is among the pane's foreground processes with that start time (`identityOK`,
`dispatch/decide.go`). A restored session is a new process: the check fails until the agent proves
its new pid, and it proves it only by writing through the bus (`agentbus status`, or a turn hook's
`TurnState`).

- **Claude Code** launches carry a `SessionStart` hook, `roles report-resume` then `roles turn end`
  (`rolecfg/launch.go`, `settingsJSON`). A restored session runs it at once, the turn end proves the
  new process, and the next pass wakes it. That is why the Claude panes came back fine.
- **Codex** launches carried only `UserPromptSubmit` (turn start) and `Stop` (turn end)
  (`codexHookArgs`). A restored Codex said nothing until a turn ran, and no turn ran: the
  dispatcher does not wake an unproven pane (rule 6, `identity mismatch`). A deadlock that only the
  person's typing broke (their input started a turn, whose hook proved the pid).

## 2. The fix

1. **Codex proves itself at start.** Its launch adds `hooks.SessionStart` running `roles turn end`,
   as Claude Code's does. Codex's `SessionStart` fires on `startup` and `resume` (its hooks
   reference: "How the session started: startup, resume, clear, compact, or fork"). Codex asks once
   to trust a new hook command; the same command on every launch keeps that trust.
2. **The dispatcher recovers any restored pane** (a client without such a hook, or a session herdr
   restored without the launch's flags). The pane's inspection also reads each foreground
   process's own `AGENT_BUS_PROJECT` and `AGENT_BUS_AGENT` (`/proc/<pid>/environ`, the same user,
   read only; `procAgent`, `agentFromEnviron`). When the registered process is gone but a process of
   the pane names itself as this very agent of this project, the agent restarted in place
   (`restartedInPlace`): the wake decision goes on to the pane's readiness, and a ready pane gets
   the fixed wake line (`ready; restarted in place`). The agent's turn then proves the new pid.

What does not change:

- **The dispatcher types only the fixed wake line**, and writes nothing new: no snapshot, no
  identity. The proof stays the agent's own (`putSnapshot` through `status` or a turn hook).
- **A real mismatch still escalates**: a process of another agent or another project, or one whose
  environment cannot be read (macOS has no `/proc`), is `identity mismatch` as before.
- **Only the wake trusts a restart.** `/clear` after a hand-off, the observation of a pane, and a
  pop's end keep the strict pid check: nothing else is typed into, or concluded from, a pane until
  the new process is proved.
- A restarted agent that never proves itself is woken within the usual retry window and escalates
  after `MaxTries` without progress, as any agent.

## 3. Tests

- `TestCodexHookArgsAreTOML`, `TestCodexDryRunCarriesTheTurnHooks`, `TestLaunchCommandLines`: the
  Codex launch carries `SessionStart`.
- `TestRestartedInPlaceIsWoken` (decision): same agent restarted is woken; another agent, another
  project, no environment, no name to compare escalate; the registered process alive stays plain
  `ready`; a restarted agent that works is deferred.
- `TestAgentFromEnviron`: both variables, valid names, NUL-separated.
- `TestRunWakesAnAgentRestartedInPlace` (the loop on a disposable Redis): a restored session gets
  the wake line; another agent's process in the pane gets nothing.
