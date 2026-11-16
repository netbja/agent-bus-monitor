- **A person named `anonymous` is nobody too (identity-anonymous-human, 2026-11-17).** With `AGENT_BUS_AGENT` unset,
  `AGENT_BUS_HUMAN=anonymous` or `[identity] as = "anonymous"` in config.toml no longer passes as the person's
  identity: a write from the shell is refused with the fix, as with no identity at all (reviewer-codex on #571).
