- **The conductor is the only process that runs repository code, and the only writer of mission
  state.** `agentbus missions` runs the missions' test commands (arbitrary code from agents' commits,
  as any CI does) and every write to `{p}:change:*`/`{p}:missions:*` goes through a fenced script in
  `bus/conductorkeys.go`; the CLI (`agentbus mission load|retry|reassign|skip|abort`) only queues operations on
  `{p}:missionops` and waits for the conductor's answer. Never add an unfenced mission write, and
  never let the dispatcher run code. The conductor writes only to `change/<change>` (validated at
  load) and never pushes or touches `main`.
