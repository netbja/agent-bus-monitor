- **master runs in Claude Code's auto mode** (`claude-sonnet-auto`, the person, 2026-11-08: they
  switched it by hand at every start); since roles-auto-mode (2026-11-24) every Claude Code role
  but security and infra starts in auto too. Checked against Claude Code's permission-modes page before the change: in auto mode
  an explicit ask rule still prompts (master's `agent-spawn deploy` and `infra`, the person's
  approval) and deny rules block in every mode; broad allow rules are dropped on entering it, and
  master's are narrow. rolecfg refuses ask rules only with `bypassPermissions`, where nothing prompts.
  `TestClaudeRolesStartInAutoMode` holds the list, master's ask rules kept.
