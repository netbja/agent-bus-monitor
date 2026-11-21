- **The person is never an agent.** `agentbus ask <agent>` and `agentbus status <agent>` name the
  agent that asks or declares, never the person the questions go to. From another agent's terminal,
  the person's recorded name (`identity.as`) is refused for both, before any broker call (an
  agent of that name, the person's own shell and `ask --withdraw` still pass). `agentbus ask
  <person>` run in master's terminal once wrote the person into `{p}:agents` with master's terminal.
  busmon then listed them under Agents, and the dispatcher woke and escalated them. An entry under
  the person's `identity.as` is no agent, whatever terminal it names (person-never-agent): one
  written before that guard from an old holder's terminal no agent shares now once made the
  dispatcher escalate "pane gone" for every report to the person. The dispatcher skips it (it
  reads `identity.as` at start), busmon drops it, and `agentbus agents` leaves it out of the list
  (`agentbus agents <person>` still shows it). A human sender still in the capped cmd stream is
  skipped when its entry borrows another agent's terminal. A leftover entry is removed with
  `agentbus agents forget <person>` (`--force` if its status is recent), or busmon's d.
