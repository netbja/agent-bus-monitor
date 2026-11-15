- **`holder` warns `holder_restarted` (herdr exit step 6d2, `health/holder.go`).** When the
  current build's holder answers, the `holder` check also weighs every agent whose last proved
  identity is a holder terminal (its pane `<build key>/<terminal>`, a verified pid). An agent is
  lost when all of these hold:
  - the holder of that build answers and started after the agent's proof;
  - the agent's proved process is proved gone on this host: no such process, another start time
    for its pid, or another boot (a reboot ends it too); a lookup that fails otherwise proves
    nothing and never makes an agent lost;
  - it was not declared `done`.
  A holder restart kills every process it held, so a terminal id the new holder reuses never hides
  a lost agent, and a terminal that ended by itself is not called lost. The holder's start is its
  socket's age on this host; the proof's age is on Redis's clock, each measured on its own clock.
  The warn names the build, how long ago it restarted and the agents lost. Nothing is relaunched by
  itself: `agentbus up` or self-repair brings them back, and sentinel only escalates (its runbook
  and the check's remediation say so). With the agents unread, the check judges as before. No agent
  runs on a holder before the cutover, so it stays OK until then.
