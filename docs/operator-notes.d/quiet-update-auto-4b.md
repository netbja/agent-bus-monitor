- **The automatic update, step 4b: `agentbus update --auto` installs** (quiet-update-auto §4.3 step 5)
  when `updates.auto = install` in `config.toml` (the person's yes given ahead); otherwise it is the
  check pass. A held update lock defers it, an unfinished update stops it (`--rollback` first), a
  skipped release (a `.migrate`, or a failed automatic install of that tag) is not installed, and any
  project in work or unreadable defers it. Then it installs as `agentbus update` does, restarting
  **every** project's running `dispatch` and `missions`, judging each project again right before its
  first restart: one now in work keeps its old services, listed with their `service restart` command,
  and nothing rolls back. A red service rolls the whole update back; the history line's kind `auto`
  with `ok=false` keeps that tag from being retried. Each pass ends in `auto.json`'s `last_pass`
  (installed, current, deferred, skipped, failed, rolled_back). The updater service and its window
  are step 4d; master's notices come with 4c.
