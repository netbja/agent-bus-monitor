- **A role may be popped on another profile by name: alt_profiles (fable-profile-architect).** A
  role's `alt_profiles` in `roles.toml` list profiles `agentbus pop <role> --profile <p>` and a
  template tab's `--profile` may name, on the same provider too; unlike `fallback_profiles` they
  are never a quota escape (`swap` and the quota ask ignore them). A session started on one resumes
  on it (holder drain, `up`), never silently on the role's own. The shipped manifest adds
  `claude-fable` (Claude Code's `fable` alias, the models table's frontier) as architect's
  alternate: `agentbus pop architect --profile claude-fable`; plain `pop architect` stays on Opus.
  Fable counts against Anthropic's separate `7d-fable` window (`agentbus quota`), and a full window
  holds every Claude agent of the project today (the hold reads the provider's tightest window).
