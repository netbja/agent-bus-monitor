- **Claude Code's hooks now report background work (background-work-visible step 1).** The turn hook reads
  `background_tasks` and `session_crons` from Claude's `Stop` and (newly installed) `SubagentStop` payloads and
  writes `{p}:bgtasks:<agent>`: each task's type, status, sub-agent name and first-seen time, a digest of its id, the
  agent's proved pid; never a command, a description, a cron's prompt or a raw id. `SessionStart` drops it; it lives an
  hour; `busmon --delete` removes it with the project. The dispatcher's background judgement reads it as the
  `hooks` source, for the agent's own process only: a Claude agent whose last Stop listed nothing now reads
  background none when no process works either (before, its hooks were never read, so with no busy process it read
  unknown, which skips a `/clear` after a hand-off and a pop-retire as running work does: `background work: unknown`
  in the dispatch log). Takes effect at the agents' next launch (the hook) and the dispatcher's restart.
