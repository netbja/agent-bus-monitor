- **Dropping a head from the merge queue is master's (broker-hardening step 4, ACL answer e).**
  - `agentbus merge-queue drop` and `undrop` refuse any agent but master, even docs, which
    delivers. docs asks master instead.
  - Master and the person then pass deliver's own check, as before.
