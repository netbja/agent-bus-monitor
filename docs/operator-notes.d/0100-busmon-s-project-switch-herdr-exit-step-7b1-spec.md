- **busmon's project switch (herdr exit step 7b1; spec `2026-11-14-busmon-project-switch-design.md`).**
  `o` on the Projects page (M) and `/project <name>` (completions: the broker's projects, read once
  at start) call `switchProject`: refused, flash only, for the current project, a project with no
  keys on this broker ("agentbus up <p> first"), and any draft (a typed line, the compose
  window's text or held drafts, an answer being typed). Otherwise the target's keys are read in a
  command and busmon ends (`switchTo`); `main` writes the old project's cache synchronously
  (`finishSwitch`; the model's `saveCache` is a command `tea.Quit` may outrun), then `execFn`
  re-executes busmon with `switchArgv`: rebuilt from the parsed flags (`optPtrs`), never edited as
  text, one-shot flags (`reset delete yes list pick version`) never carried, `--project=<target>`
  last. A failed cache write means no exec; both failures print the command to type and exit 1.
  A switch is also an upgrade (the installed binary is re-executed). **Start order**:
  `--project`, `AGENT_BUS_PROJECT`, then the project last used against this broker
  (`last-project` beside the per-project caches, one line per broker, written at start; the key is
  the connected client's network, address, db and tls, never `-H` text or credentials), skipped
  when it has no keys. Without any, busmon shows the picker's screen in open-here mode
  (`newOpenHerePick`, `pickToOpen`): the same list and the same check on the broker, but Enter
  records the project and busmon re-executes on it (`openPicked`), never `agentbus up` (that stays
  `--pick`'s, reviewer-codex); without a terminal it prints how to name one.
