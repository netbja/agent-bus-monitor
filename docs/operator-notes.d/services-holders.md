- **busmon's Services page lists the project's holders, and `service stop holder <build>` stops
  an old one** (agent-screen-blocked §4, the person's Q5).
  - Each holder line shows its build, whether it runs (`running`, `not answering` when its unit
    runs but its socket does not answer, `inactive`), its uptime when known, and how many agents
    it hosts. They are read on busmon's own machine only.
  - A holder of another build than the dispatcher's that answers with no agent is flagged
    `old build, hosts no agent` with the commands:
    `agentbus --project <p> service stop holder <build>`, then `service gc`.
  - The stop refuses the current build's holder and a holder with a live terminal (`holder
    drain` moves them first), one that answers but cannot be listed, and one whose unit runs but
    whose socket does not answer (it may hold agents; `agentbus down <p>` stops every holder of the
    project, its agents with them), and one whose unit state cannot be read. It never kills an agent;
    `service restart holder --kill-agents` does.
  - When no holder of the dispatcher's build answers, the page says so and names `agentbus up`.
