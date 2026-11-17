- **Three quota thresholds, per project, read each minute (quota settings step 2, 2026-11-17).** `dispatch.quota_warn`
  (85) warns master and tells agents with work to finish their step; the new `dispatch.quota_drain` ("start nothing
  new above"; unset, it follows the warning: today's behaviour) drains the run, which runs again once every window is
  under it; `dispatch.quota_hold` (95) holds the provider. `warn <= drain <= hold` is a rule: a broken order uses the
  defaults for all three (logged once per episode). A project's own values live in `config.toml`:
  `[project.<name>.dispatch]` with `quota_warn`, `quota_drain`, `quota_hold`, `quota_max_age`, read before the
  project's env file (`AGENT_BUS_QUOTA_*` from the shell at `up`), then the machine keys, then the defaults. The
  dispatch service re-reads them from the files with the readings, every minute, with no restart, and never from its
  own start-time environment; an unreadable `config.toml` keeps the last values. `busmon --delete` (Projects page too)
  removes the project's table. Quota directives and the drain notice, stored and read later, carry the absolute date
  (`resets Tue 17 Nov 13:59`), never "today" or "tomorrow".
