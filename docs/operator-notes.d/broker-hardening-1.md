- **The broker's durability is stated, and its access has a stated limit (broker-hardening step 1).**
  - **Durability:** `docker-compose.yml` gives the broker its persistence in the config on stdin,
    not on the argv and not left to Redis's defaults: `appendonly yes`, `appendfsync everysec` (a
    crash loses at most about a second of writes) and `save 3600 1 300 100 60 10000`.
    `bus.Persistence` reads `appendfsync` too. The `broker-persistence` check now also warns when
    AOF is on with `appendfsync no`.
  - **The limit:** agents run as the person's OS user, so they can read every credential the
    person's files hold, ACL passwords included. ACL users protect against mistakes (a wrong
    command, a wrong key), not against a hostile agent. A separate OS user for agents is out of
    scope for now.
  - **The password:** the broker's historical default password ends only when the person runs
    `./setup.sh rotate-redis-password`; no agent runs it.
