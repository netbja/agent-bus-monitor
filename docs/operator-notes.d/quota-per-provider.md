- **Quota thresholds per provider (the person, 2026-11-25).** `dispatch.quota_warn`, `_drain` and `_hold`
  stay the defaults for every provider; a provider may have its own in config.toml's
  `[quota.<provider>]` table (`warn`, `drain`, `hold`; any valid provider id, a bus name), per project
  in `[project.<name>.quota.<provider>]`, project first, then the machine's. 0 or unset is the
  default's. The person's alone (busmon's Shift+C, which now also shows the three defaults, or
  config.toml; `agentbus config set` refuses them; no env variable); the dispatcher reads them each
  minute. Shift+C always lists anthropic, google, moonshot, openai and xai, and any other id
  config.toml sets. A provider no usage reader reads (none built in, none in quota.toml) keeps its
  thresholds, shown and inert: Shift+C labels it "(no usage reader)", `agentbus quota` gives it a
  "no usage reader" row. A provider's own value wins over the default wherever each is set. An unset
  drain follows the warning in force (the provider's); a drain the files set stays, raised to a
  provider's warning above it. Broken defaults are the registry's (85/85/95), a provider's own values
  still over them. `warn <= drain <= hold` is checked per provider once resolved: a broken set runs
  that provider on the defaults, logged once (`quota-order` with `provider`). A hold was already per
  provider (only its agents held; master's provider still pauses the project and drains the run).
  `agentbus quota` gains a WARN/DRAIN/HOLD column naming the source of each row's thresholds; with
  `--project` it reads that project's values, else the machine's. `[quota.claude] probe` is another
  key: `claude` is not a provider id.
