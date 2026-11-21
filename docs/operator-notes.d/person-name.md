- **`person.name` (env `AGENT_BUS_PERSON_NAME`, default empty, group Identity beside `person.language`)**
  is the person's own name, a bus name. busmon writes as `--as`, then `AGENT_BUS_HUMAN` / `identity.as`,
  then `person.name`, and only then `pilot` (busmon-cli-flags item 3, the person's option A; `pilot`
  also names the lease). An invalid `person.name` is skipped, never a refusal to start. The settings
  page shows it as "Your name"; `identity.as` became "Your name on the bus". Master's role skill may
  read it with `agentbus config`.
