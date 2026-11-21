- **The broker user per process (broker-hardening step 4b, part 1; off by default, nothing changes yet).**
  - **The setting:** `broker.acl` (`off` or `on`).
  - **With it on,** each process logs in as its role's user, with the password of
    `~/.config/agentbus/redis-<user>.env`:
    - the services (`service run`, the holder service) as `dispatcher` when no registered agent is
      among their ancestors (a service manager's launch); run by an agent by hand, that agent's role;
    - `setup`, `backup` and `restore` as `admin`, and only from the person's shell;
    - busmon and every other command by `AGENT_BUS_AGENT` and the process tree: under a
      registered agent other than master, `agent` whatever it claims; master's as `master`; the
      person's as `busmon`. A process whose ancestry cannot be read gets `agent`.
  - **A missing password file is an error,** never the default user.
  - **Not yet:** `./setup.sh acl on|off` creates the users and the files (part 2), and the rotation
    per user (part 3). Until then, leave `broker.acl` off.
