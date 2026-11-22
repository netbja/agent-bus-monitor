- **The automatic update's settings** (quiet-update-auto §6, §7, the second build step; the person's answers):
  `updates.auto` (`notify` by default, or `install`), `updates.window` (`03:00-05:00`, local time, may
  cross midnight) and `updates.max_wait` (7 days), the host's, no env variable. `auto` and `max_wait` are
  set in `config.toml` alone (`agentbus config set` refuses them, busmon shows them); the window is also
  editable in busmon's Shift+C, as the person. **Nothing reads them yet:** the updater and
  `agentbus update --auto` come in the next steps, and busmon's help and the registry say so.
