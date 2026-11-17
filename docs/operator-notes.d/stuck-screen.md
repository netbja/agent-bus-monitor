- **A stuck screen rings the desktop once per episode** (`dispatch.stuck_after`,
  `AGENT_BUS_STUCK_AFTER` in whole seconds; default 3 minutes, `0` off). A screen counts as
  stuck when it is settled, its client's signature reads it as neither ready, nor busy, nor a
  prompt, and it has not changed for that long in the same process. The ring says
  `<project>: <agent>'s screen has not changed for <d> and is not a prompt agentbus knows
  (<terminal>)`, never "waits on a prompt". It follows prompt-notify's suppressions (a declared
  blocked or waiting, an open ask, a swap, the pause), under its own mark `stuck:<agent>`. A
  client update that changes the idle screen can make several agents of that client stuck at
  once, one ring each. The observation gains `screen_since`, the time of the first reading of
  the current screen; no screen text is written (agent-screen-blocked §3).
