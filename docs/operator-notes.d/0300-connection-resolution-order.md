- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT` (defaults `localhost`/`6380`) with the password from
  `REDIS_PASSWORD`, else the password file, else `AgentBus2025!` (redis-password, `bus/password.go`:
  `PasswordFile` = `$XDG_CONFIG_HOME/agentbus/redis.env`, else `~/.config/agentbus/redis.env`, one
  `REDIS_PASSWORD="…"` line, 0600 in 0700, written by a rename; a file that exists but cannot be read
  is an error, never the default, and no error quotes its content; `LegacyPasswordInUse` reads the
  same order). Compose reads the file through `env_file` (`required: false`) and gives redis-server
  its password as a config on stdin (a shell `REDIS_PASSWORD` at `compose up` wins, then the file,
  then the default; `"` and `\` escaped; the healthcheck uses `REDISCLI_AUTH`), never on an argv.
  `setup` writes it (`password` in `setup/redis.go`) only for a fresh broker: no file, none exported nor in the checkout's `.env` (compose would use it;
  status warns about it), no
  `agent-bus-redis` container and no `agent-bus_redis-data` volume, each read for sure. `./setup.sh
  rotate-redis-password` (`setup/rotate.go`, the person's act, never sentinel's) checks everything
  first (none exported, this checkout's broker running, no `REDIS_PASSWORD` in the checkout's `.env`
  nor in any project's services env file, the current one logs in), asks (or `--yes`;
  `--dry-run`), then `RotatePassword`: new file (old kept as `.prev`), `CONFIG SET requirepass`, a
  fresh login, else the broker set back and the file put back byte for byte (an error from CONFIG SET is checked against the broker first: a login with the new one means applied; if neither logs in, or the set-back fails, the
  new file stays, the old in `.prev`: the broker may hold either); then `docker compose up -d
  --force-recreate redis` from the checkout (foureyes: the container keeps the env_file it was created
  with, so a restart would come back on the old password), a fresh login waited for; then the running
  project services restart. Its rollback test starts private `redis-server` processes (CONFIG SET
  changes a whole server, never the shared test broker), opt-in through `AGENTBUS_TEST_REDIS_URL`.
  `scripts/setup-legacy.sh` neither writes nor rotates it; its help is compared to the Go one without
  `RotateHelp`.
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` sender identity defaults to `anonymous`,
  overridable via `AGENT_BUS_AGENT`; inside a herdr pane (`HERDR_PANE_ID`) with `AGENT_BUS_AGENT`
  unset, a command that writes as the sender is refused with the fix (A8, `identityGuard`:
  relaunch through the launcher or export it; read-only commands still run, an unknown command
  counts as a write).
