- **A reset is written with its day (quota settings step 1, 2026-11-16).** Every reset and time to come is written
  by `quota.ResetLabel`: `today 13:59`, `tomorrow 13:59`, `Wed 18 Nov 13:59` (the year only when it is not this
  one), in the dispatcher's quota messages (master's warning, an agent's, the drain's announcement), busmon's
  account cards and task estimates, and `agentbus quota`'s RESETS column (`in 23h29m (tomorrow 13:59)`).
  Before, a seven-day window's reset tomorrow read `13:59`, as if it were today's. `display.date_format`
  (Settings, Display: `relative` default, `iso`, `clock12`, a sample of each shown) has no environment variable:
  the dispatch service reads config.toml at each message, so a change applies with no restart. The pace sentence
  under a card wraps instead of being cut.
