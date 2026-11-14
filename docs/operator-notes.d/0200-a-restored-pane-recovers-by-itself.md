- **A restored pane recovers by itself** (spec `docs/superpowers/specs/2026-11-08-codex-identity-recovery.md`).
  A new process fails `identityOK` until it proves its pid through the bus; Claude Code's
  `SessionStart` hook (`roles turn end`) does it at once, and Codex's launch now carries the same
  `SessionStart` hook. For any client that does not, the wake decision accepts a pane whose
  registered process is gone when one of its foreground processes carries this agent's own
  `AGENT_BUS_PROJECT`/`AGENT_BUS_AGENT` (`restartedInPlace`, `/proc/<pid>/environ`): the fixed wake line
  only, its turn proves the pid; `/clear`, the observation and pop-done stay strict; another agent,
  or no `/proc`, is still a mismatch.
