- **The ACL writer inventory (broker hardening step 4, first PR; no ACL user is enabled yet).**
  - **The check:** `bus.ACLRules` holds the proposed users. `TestACLInventory` runs every command
    shape the suite's production callers sent (`bus/testdata/acl-inventory.json`) through
    `ACL DRYRUN` on the disposable broker, as each user that sends it.
  - **Rebuild it** after adding a writer: record the suite, then fold the recording.
    - Record:
      `GOFLAGS=-tags=aclrecord AGENTBUS_ACL_RECORD=/tmp/acl.jsonl AGENTBUS_TEST_REDIS_URL=… go test ./... -count=1`,
      with `AGENT_BUS_AGENT` unset.
    - Fold: `go test ./bus -run TestACLInventory -acl-update=/tmp/acl.jsonl`.
    - Without the tag the recorder is not built.
  - **Not covered:** a writer no test reaches, and a key a script builds itself. The check logs
    both.
