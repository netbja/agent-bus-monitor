- **`board.wip_limit`: the work-in-progress limit's setting** (wip-limit r2 §3.3; the person's answers):
  requests an agent may hold accepted when it starts a new one, default 2, 0 no limit; per project
  (`[project.<name>.board]` over `[board]`), no env variable. **The person's alone, from busmon's
  Shift+C or `config.toml`:** a new `PersonEdit` flag makes `agentbus config set` and `unset` refuse it
  ("set it in busmon (Shift+C) or config.toml"); busmon's settings page writes through
  `config.SetAsPerson` / `UnsetAsPerson`, which no other package may call (a test scans for them).
  `PersonOnly` keys (`backup.after_*`) stay read-only in busmon. The CLI that reads it (`request accept`,
  `request send`) and the row's detail text come in the next steps.
