- **The conductor's operations are master's (broker-hardening step 4, ACL answer d).** `agentbus mission
  load`, `retry`, `skip`, `reassign` and `abort` refuse another agent's environment. Master and the person pass;
  `mission status` and `show` stay open to everyone.
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives `{p}:missionops` write to
    master and the dispatcher, never to `agent`, and that is enforced only once `broker.acl=on`
    (step 4b, not built yet). Until then the CLI's refusal is a courtesy message.
