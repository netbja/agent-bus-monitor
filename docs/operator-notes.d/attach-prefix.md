- **An attach names its way out on screen (attach-prefix, 2026-11-16).** `agentbus attach` (and busmon's `t`,
  which runs it) starts on a banner, `To leave: press F12 then d`, for 2 s or until a key. That key is not sent
  (the person has not seen the agent's screen: an Enter would answer a dialog unread), unless it is the prefix,
  so `F12 d` works during the banner. The outer terminal's title names the same way out for the whole attach. Ctrl-C goes to the
  agent; it never leaves. An agent page's Screen line reads `t attaches, then F12 d detaches` with the prefix in
  effect. F12 stays the default: on the person's machine it was the outer herdr, not the desktop, that took F12.
  `attach.prefix` is set in busmon's Settings (Display), with attach's own rule for a prefix from the setting:
  refused unless every client the project's roles run on (own and fallback profiles) records it `free`, naming
  each other client and its free keys. Today only F12 is recorded free, for Claude Code, Grok and Codex (codex-cli 0.169.0, its
  `f12-at-input` probe, 2026-11-16): with a Kimi or agy role, attach to it still needs `--prefix`.
