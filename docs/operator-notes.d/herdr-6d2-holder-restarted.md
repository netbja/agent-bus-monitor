- **`holder` warns `holder_restarted` (herdr exit step 6d2, `health/holder.go`).** When the
  current build's holder answers, the `holder` check also looks at every agent whose last proved
  identity is a holder terminal (its pane `<build key>/<terminal>`, a verified pid). An agent is
  lost when the holder of that build started after its proof and its terminal is no longer live
  there: the holder crashed or was stopped and came back empty. The holder's start is its socket's
  age on this host; the proof's age is on Redis's clock, each measured on its own clock. The warn
  names the build, how long ago it restarted and the agents lost. Nothing is relaunched by itself;
  `agentbus up` or self-repair brings them back, and sentinel only escalates (its runbook and the
  check's remediation say so). With the agents unread, the check judges as before. No agent runs
  on a holder before the cutover, so it stays OK until then.
