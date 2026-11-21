- **The plan's writes are master's (broker-hardening step 4, ACL answer b).**
  - **Refused to other agents:** `agentbus plan load`, `link`, `done`, `note`, `ignore` and `clear` refuse
    another agent's environment (`AGENT_BUS_AGENT` other than master).
  - **Still open:** master and the person; `plan show` to everyone. `plan priority` keeps its
    person-only rule.
  - **docs' delivery:** when docs (or any deliverer but master) delivers, its plan link becomes a
    directive to master carrying the command to run (`agentbus plan link <task> --pr <n>`).
