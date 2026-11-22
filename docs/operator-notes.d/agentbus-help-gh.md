- **`agentbus --help` is gh's layout** (agentbus-help-gh step 1): USAGE, nine UPPERCASE groups
  (agent, message, review, work, terminal, project, usage, admin, internal last), one
  `name: brief` line per command, then FLAGS, EXAMPLES and LEARN MORE, no color, nothing over 80
  columns. A new command needs a `Brief` (2 to 6 words) in `cmd/agentbus/commands.go` and its
  name in exactly one of `helpSections` (`cmd/agentbus/help.go`), or the suite fails; its detail
  stays in `agentbus help <command>`.
