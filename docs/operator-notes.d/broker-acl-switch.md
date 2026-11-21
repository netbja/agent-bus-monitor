- **`./setup.sh acl on|off` (broker-hardening step 4b, part 2): the person's act**, refused from an
  agent's environment or process tree before any password file is read.
  - **On:**
    - a new password file per user, `~/.config/agentbus/redis-<user>.env` (0600);
    - `users.acl` (passwords hashed, `user default off` last) and the healthcheck's `ping.pass` put
      in the broker's data volume;
    - `broker.acl=on` in `config.toml`;
    - the broker recreated, proved by an `admin` login with the default user refused;
    - the running project services restarted.
    - A broker that does not take its users is put back: setting off, files removed, recreated.
  - **The compose file switches on the file:** with `/data/users.acl` present it loads it instead of
    `requirepass`, and the healthcheck logs in as `ping`; `docker compose up` by hand keeps that.
  - **Off:** removes both files from the volume, recreates the broker on `redis.env`'s password,
    sets `broker.acl=off`. The user password files stay; they are rewritten at the next `on`.
  - **On is refused while the broker's ACL is live** (it would replace the files its users log in with): to remake the users, `acl off` then `acl on`.
  - **Refused while** `REDIS_URL`, `REDIS_PASSWORD` or `AGENT_BUS_BROKER_ACL` is exported (each wins over the files).
  - **`rotate-redis-password` does not rotate the users yet** (part 3): with ACL on it refuses,
    since the default user no longer logs in. Run `acl off` before `uninstall --purge`, or set
    `broker.acl` back to `off`: a fresh volume has no `users.acl`.
