- **busmon reads `stuck` for an agent whose screen nobody recognises and that has not changed for
  `dispatch.stuck_after`** (3 minutes by default; busmon reads the same setting as the
  dispatcher). It is judged from the dispatcher's observation of the agent's current process:
  settled, its client's signature reads neither ready, nor busy, nor a prompt. The state column
  says `stuck`, For you lists it as "stuck: its screen is not recognised and has not changed for
  <d>", and the agent's page heads its live screen with those words, the last 6 lines below. A
  declared blocked or waiting, or an open ask, is not covered (agent-screen-blocked §3, 3b-2).
  busmon reads the setting on its own machine and the dispatcher on its own (its env file): with
  different values there, busmon says stuck at another time than the desktop ring. An agent being
  swapped still reads stuck in busmon, since its screen is what it is; only the ring waits.
