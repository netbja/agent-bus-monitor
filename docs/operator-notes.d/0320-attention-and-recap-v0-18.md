- **Attention and recap (v0.18)** — busmon derives answered `waiting` declarations only from a
  later addressed `human=yes` command or a reply in the agent's thread; open asks remain separate
  attention items. Due reminders wait until drafts and other modals clear, then group oldest-first
  and snooze locally. `R` opens a recap built from current snapshots and retained events; capped
  history boundaries and failed reads are labelled instead of treated as empty. The optional
  summary directive requires an explicit `y` confirmation and uses the normal send path (`send.go`).
  Global settings are in `~/.config/agentbus/config.toml`, then the legacy `busmon.toml` (or
  `$XDG_CONFIG_HOME/agentbus/`), and
  local last-visit/snooze data is atomically cached under `~/.cache/agentbus/` with mode `0600`.
