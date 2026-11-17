- **The holder's health and its two commands (herdr exit step 6d1).** One evidence per project
  (`cmd/agentbus/holderhealth.go`, `holderEvidence`): per build key, its unit and whether the
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
