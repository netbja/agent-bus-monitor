- **Dropping a head from the merge queue is master's (broker-hardening step 4, ACL answer e).**
  - `agentbus merge-queue drop` and `undrop` refuse any agent but master, even docs, which
    delivers. docs asks master instead.
  - Master and the person then pass deliver's own check, as before.
  - **Where the barrier is:** the broker's ACL. `bus.ACLRules` gives `{p}:mergequeue:dropped` write to
    master and to the dispatcher (which prunes old drops), never to `agent`. It is enforced only once
    `broker.acl=on` (step 4b, not built yet); until then the CLI's refusal is a courtesy message.
