- **A request nobody can accept can still be closed.** A tracked request whose body cmd retention
  removed (`availability: missing`), or that expired before anyone accepted it, refuses `accept`,
  and `done` needed an accept, so it stayed open forever (concierge-memory-opinion, 98 h). Its
  target, or its sender (usually master), now closes it with `agentbus request done <task> <why>`,
  unaccepted. The board entry records `closed_unaccepted` (`body missing` or `expired`), and the
  reply goes to the other side. A request whose body is there still needs an accept, and the sender
  never completes one its target accepted. The accept refusal names the command.
