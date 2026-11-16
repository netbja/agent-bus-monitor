- **An attach names its way out on screen (attach-prefix, 2026-11-16).** `agentbus attach` (and busmon's `t`,
  which runs it) starts on a banner, `To leave: press F12 then d`, for 2 s or until a key (that key still reaches
  the agent), and the outer terminal's title names the same way out for the whole attach. Ctrl-C goes to the
  agent; it never leaves. An agent page's Screen line reads `t attaches, then F12 d detaches` with the prefix in
  effect. F12 stays the default: on the person's machine it was the outer herdr, not the desktop, that took F12.
  `attach.prefix` is set in busmon's Settings (Display); a key a client's profile records as `bound` is refused
  there, naming the client, as attach refuses it for that client's terminals.
