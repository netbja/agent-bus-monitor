- **The broker user per process (broker-hardening step 4b, part 1; off by default, nothing changes yet).**
  - **The setting:** `broker.acl` (`off` or `on`).
  - **With it on,** each process logs in as its role's user, with the password of
    `~/.config/agentbus/redis-<user>.env`:
    - the services (`service run`, the holder service) as `dispatcher` when no registered agent is
      among their ancestors and they carry no identity (a service manager's launch); otherwise the CLI's rule;
    - `setup`, `backup` and `restore` as `admin`, and only from the person's shell;
    - busmon and every other command by `AGENT_BUS_AGENT` and the process tree: under a
      registered agent other than master, `agent` whatever it claims; master's as `master`; the
      person's as `busmon`. A process whose ancestry cannot be read gets `agent`.
  - **A missing password file is an error,** never the default user.
  - **Turn it on with `./setup.sh acl on`** (part 2), never by editing `broker.acl` by hand: the
    users and their files come from there. Rotation per user is `./setup.sh rotate-redis-password` (part 3).
