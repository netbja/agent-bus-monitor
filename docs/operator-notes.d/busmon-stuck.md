- **busmon reads `stuck` for an agent whose screen nobody recognises and that has not changed for
  `dispatch.stuck_after`** (3 minutes by default; busmon reads the same setting as the
  dispatcher). It is judged from the dispatcher's observation of the agent's current process:
  settled, its client's signature reads neither ready, nor busy, nor a prompt. The state column
  says `stuck`, For you lists it as "stuck: its screen is not recognised and has not changed for
  <d>", and the agent's page heads its live screen with those words, the last 6 lines below. A
  declared blocked or waiting, or an open ask, is not covered (agent-screen-blocked §3, 3b-2).
