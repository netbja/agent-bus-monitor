- **The person is never an agent.** `agentbus ask <agent>` and `agentbus status <agent>` name the
  agent that asks or declares, never the person the questions go to. From another agent's terminal,
  the person's recorded name (`identity.as`) is refused for both, before any broker call (an
  agent of that name, the person's own shell and `ask --withdraw` still pass). `agentbus ask
  <person>` run in master's terminal once wrote the person into `{p}:agents` with master's terminal.
  busmon then listed them under Agents, and the dispatcher woke and escalated them. An entry that
  borrows another agent's terminal is no agent: the dispatcher skips a human identity with such an
  entry (a human sender still in the capped cmd stream, or the person's `identity.as`, which it
  reads at start), and busmon drops the person's. A leftover entry is removed with `agentbus agents forget
  <person> --force` (`--force`: its status is recent), or busmon's d.
