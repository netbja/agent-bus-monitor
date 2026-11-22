- **A closed pop that owns open requests is no longer silent (board-drift step 1, spec §6).** The
  dispatcher keeps such a pop (its board tasks still owe work), as before, but now tells master once per
  episode, woken: "<role> has no terminal and owns open requests: <task (state)>, …. Bring it back:
  agentbus --project <p> pop <role> --resume <session>" (the session `{p}:resume` holds; without one,
  plain `pop <role>`). It sends it through `FencedCmdOnce` under the `{p}:quota` mark
  `popstranded:<role>` (episode = the pop's snapshot time), only when the pop is closed (no terminal,
  process gone), and not when a read failed. The dispatcher still never relaunches a pop (Q4).
