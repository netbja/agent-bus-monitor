- **A Claude Code agent boots by itself (fresh-agent-boot).** Its launch passes one fixed first
  turn on the command line, after the role's system prompt: "Run the On boot steps of your role
  now." (`rolecfg.BootLine`), fresh or resumed (`--resume`), so an agent started by `up`, a pop, a
  reload or the self-repair no longer sits at an empty prompt, unbooted and "agent unknown", until a
  person types into it. Nothing types it: the dispatcher's lines stay the wake line and `/clear`.
  Codex and agy already take the role prompt as their first turn; Kimi and Grok are unchanged.
