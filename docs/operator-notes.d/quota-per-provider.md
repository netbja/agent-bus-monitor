- **Quota thresholds per provider (the person, 2026-11-25).** `dispatch.quota_warn`, `_drain` and `_hold`
  stay the defaults for every provider; a provider may have its own in config.toml's
  `[quota.<provider>]` table (`warn`, `drain`, `hold`; providers anthropic, google, moonshot, openai,
  xai), per project in `[project.<name>.quota.<provider>]`, project first, then the machine's. 0 or
  unset is the default's. The person's alone (busmon's Shift+C, which now also shows the three
  defaults, or config.toml; `agentbus config set` refuses them; no env variable); the dispatcher reads
  them each minute. A provider's own value wins over the default wherever each is set. An unset drain
  follows the warning in force (the provider's); a drain the files set stays, raised to a provider's
  warning above it. `warn <= drain <= hold` is checked per provider once resolved: a broken set runs
  that provider on the defaults, logged once (`quota-order` with `provider`). A hold was already per
  provider (only its agents held; master's provider still pauses the project and drains the run).
  `agentbus quota` gains a WARN/DRAIN/HOLD column naming the source of each row's thresholds; with
  `--project` it reads that project's values, else the machine's. `[quota.claude] probe` is another
  table: `claude` is not a provider id.
