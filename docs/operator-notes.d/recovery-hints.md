- **A failed relaunch names the holder's command; a launch starts only its own role (recovery-hints, 2026-11-16).**
  A self-repair or reload that fails now says `recover with agentbus --project <p> pop <role> [--profile <p>]
  [--resume <session>], or agentbus up <p> --resume`, never the herdr-era `agent-launch <role> <project>` in a new
  pane, which started an agent the dispatcher cannot wake (followed once, it started three agents as master).
  `agent-launch` refuses a role that is not its environment's `AGENT_BUS_AGENT` (typed in master's terminal it
  launches only master; the holder sets each terminal's own) and a `--resume` session that `{p}:resume` records
  for another role. A `--resume` whose records cannot be read (broker down) is refused; a fresh launch reads none.
