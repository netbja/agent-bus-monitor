- **The plan's writes are master's (broker-hardening step 4, ACL answer b).**
  - **Refused to other agents:** `agentbus plan load`, `link`, `done`, `note`, `ignore` and `clear` refuse
    another agent's environment (`AGENT_BUS_AGENT` other than master).
  - **Still open:** master and the person; `plan show` to everyone. `plan priority` keeps its
    person-only rule.
  - **docs' delivery:** when docs (or any deliverer but master) delivers, its plan link becomes a
    directive to master carrying the command to run (`agentbus plan link <task> --pr <n>`).
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives the `agent` user no write on
    `{p}:plan`, `{p}:plan:facts` or `{p}:plan:ignore`, and that is enforced only once
    `broker.acl=on` (step 4b, not built yet). Until then the CLI's refusal is a courtesy message: an
    agent calling the bus functions directly is not stopped.
