- **`busmon --reset` is `XTRIM`, not `DEL`.** `Bus.Purge` trims each stream to `MAXLEN 0`, so the
  visible history is wiped but consumer groups (cmd at-least-once cursors) and the
  `armed`/`pilot`/`gate` keys **survive** — don't switch it to `DEL`, which would reset cmd delivery
  state. The confirmation reads stdin **before** the TUI starts; a piped/non-TTY stdin counts as
  "no", so `--reset` never purges unattended without `--yes`. **`busmon --delete <p>` is the `DEL`**
  — the deliberate opposite: it retires the project, so consumer groups, leases and every hash go
  with it. `--reset` clears a project you are keeping; `--delete` removes one you are not.
