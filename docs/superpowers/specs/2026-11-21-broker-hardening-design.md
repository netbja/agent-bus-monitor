# Harden the broker, lightly

Status: spec r2, for review (subject `broker-hardening`). Not built. Base: origin/dev `4f095697`.
r2 corrects three claims of §1: `CONFIG SET` is used, `EVAL` is sent, and `docker` has more callers (foureyes).
Author: architect, from the person's directive of 2026-11-21 (thread `1795222562295-0`), relayed by
master (cmd `1795222577984-0`). There is no new dependency. It must work on Linux and macOS, with
podman, and without systemd. The person wants to discuss it: §5 lists what they need to decide.

## 1. What is configured today, point by point

Read from `docker-compose.yml`, `setup/redis.go`, `setup/rotate.go`, `bus/bus.go`,
`service/env.go`, `remote-bus.sh`, `backup/` and the health checks, at `4f095697`.

### Durability

| Point | Today | Gap |
|---|---|---|
| AOF | `redis-server - --appendonly yes` (compose `command`) | none |
| `appendfsync everysec` | Not set. Redis's own default is `everysec`, so it holds, implicitly. | Not stated. The health check `broker-persistence` (`health/evaluate.go:485`) reads `appendonly` and `save`, not `appendfsync`. |
| RDB snapshots | No `save` line. Redis's default schedule applies when no `save` directive is given. | Implicit, as above. |
| Valkey | Not supported. The image is fixed to `redis:8-alpine`. Clients would connect (same protocol). The restore's version check reads `redis_version` (`backup/restore.go:96`); Valkey reports a compatibility value there. | No way to choose it, and the version check is untested on Valkey. |
| Data directory in backups | The data is the compose volume `redis-data` (`RedisVolume`, `setup/redis.go:128`). `agentbus backup` is **logical**: a `DUMP` of every key of each project (`bus/backupdump.go`, `backup/run.go:129`), classified by `bus/keykinds.go`. Every key is project-prefixed, so nothing of agentbus is missed. A restore refuses an older broker. | The raw data directory is not copied. §5 Q2 asks whether it should be. |

### Access

| Point | Today | Gap |
|---|---|---|
| Listening | TCP, published on `127.0.0.1:6380` only (compose `ports`). There is no unix socket. | Not socket-only. |
| Clients | `Connect` (`bus/bus.go:96`): `REDIS_URL` if set, which go-redis parses as `redis://`, `rediss://` or `unix://`, with a user name allowed. Otherwise `REDIS_HOST`, `REDIS_PORT` and `REDIS_PASSWORD`, else the password file, else the legacy default. The services copy `REDIS_*` from `agentbus up`'s shell (`service/env.go:23`). The test suite already runs over `unix://` (`docs/testing-redis.md`). | The socket needs only a URL; the client needs no code. |
| Remote busmon | `remote-bus.sh` runs `ssh -NL <local>:localhost:6380`, a tunnel to the TCP port. | A socket would need `ssh -L <port>:<socket path>` (OpenSSH supports forwarding to a unix socket). |
| Password | One `requirepass`, for the default user. The file is written 0600 by `./setup.sh install` on a fresh broker, or by `./setup.sh rotate-redis-password`. **On this host there is no `~/.config/agentbus/redis.env`, so the broker still uses the historical default.** That is the `redis-password` warning sentinel keeps raising (`health/evaluate.go:243`). The fix exists, and only the person runs it: the rotation. | None in code. The person's rotation clears the warning. |
| ACL users | None. Every client uses the default user with every command: agents, dispatcher, conductor, busmon and the person's CLI. | Everything. |
| Commands the code uses beyond data | `CONFIG GET` (persistence); **`CONFIG SET requirepass`** (the password rotation, `setup/rotate.go:31`); `INFO` (version, memory); `MEMORY USAGE` (busmon's project page); `SCAN *` (project discovery, `bus/projects.go:183`); `DEL` of a project (`busmon --delete`); `XTRIM` (`--reset`); `DUMP`/`RESTORE` (backup). The fenced scripts are sent as `EVALSHA`, **falling back to `EVAL`** on `NOSCRIPT`, which is go-redis's `Script.Run`, after any broker restart empties the script cache. No `FLUSHALL`, `FLUSHDB` or `KEYS`: restore's key listing is `bus.ProjectKeys`, a `SCAN`. | The inventory ACLs need. |

### Platforms

- **The `docker` CLI is assumed in many places:**
  - **the installer:** `docker inspect`, `docker volume inspect` and compose `up` in
    `setup/redis.go`; `docker ps -a` in `setup/status.go:58`; `setup/preflight.go:53` and `:78`;
    and compose calls in `setup/uninstall.go:139` and `setup/rotate.go:237`;
  - **the health checks:** `docker ps` in `redis-password`
    (`internal/dispatchsvc/healthchecks.go:554`), and `LookPath`, `docker info` and
    `docker context inspect` in the disk check (`internal/dispatchsvc/diskcheck.go:82`, `:86`,
    `:131`);
  - **busmon:** the Redis page's `docker port agent-bus-redis 6379/tcp` and `docker logs`
    (`cmd/busmon/redis_page.go:74`, `:108`), and the Services card's `docker inspect`
    (`cmd/busmon/services_cards.go:130`).

  Podman works only through its docker shim. Without it, the installer says to start the broker
  by hand, and those checks and pages read `unknown`.
- **Without systemd:** the broker does not need systemd. The container's `restart:
  unless-stopped` is the docker daemon's job. Under rootless podman, a container restarts after
  a reboot only through podman's own restart unit, which is systemd again. On macOS, podman and
  Docker Desktop run in a VM that the user's login starts.
- **macOS and a unix socket:** containers run inside a VM. A socket created in the container and
  bind-mounted to the host does not work across that boundary. A socket-only broker on macOS
  therefore means a Redis or Valkey running natively, outside any container (§5 Q1).

## 2. The stated limit (operator note, in the build's first PR)

> Agents run as the person's OS user, so they can read every credential the person's files hold,
> ACL passwords included. ACL users protect against mistakes (a wrong command, a wrong key),
> not against a hostile agent. A separate OS user for agents is out of scope for now.

## 3. What is missing, and the recommended order

**Step 1, durability stated (small, no behaviour change on Redis 8):**
- The compose config on stdin gains `appendfsync everysec` and an explicit
  `save 3600 1 300 100 60 10000`.
- `bus.Persistence` also reads `appendfsync`. `EvaluateBrokerPersistence` warns when AOF is on
  with `appendfsync no`, and when neither AOF nor `save` is set (the latter already warns).
- The operator note of §2 is added.
- The person rotates the password: `./setup.sh rotate-redis-password`. That is not code, and it
  ends today's repeated warning.

**Step 2, unix socket on Linux:**
- compose bind-mounts a host directory, `${XDG_RUNTIME_DIR}/agentbus/redis/` (mode 0700, made by
  the installer). It sets `unixsocket /run/agentbus/redis.sock`, `unixsocketperm 700` and
  `port 0`. The TCP publish is dropped.
- The installer writes `REDIS_URL=unix://…/redis.sock` into `redis.env`, which the clients
  and services already read.
- `remote-bus.sh` forwards to the socket path.
- The `redis-password` check reads the socket's directory mode instead of `docker ps`.
- busmon's Redis page stops reading `docker port … 6379/tcp`, which has nothing to report with
  `port 0`, and shows the socket path.
- macOS keeps loopback TCP (§5 Q1).

**Step 3, podman without the shim:** a `broker.runtime` setting (`docker`, `podman`). It is used
by every `docker` caller in §1's list (installer, status, preflight, uninstall, rotation, the two
health checks, busmon's Redis page and Services card), through one helper that names the
binary, with `podman compose` as the compose command.
"Without systemd": `agentbus up` starts the broker when it is down, with the same compose call
as the installer. A reboot is then covered by the person's own `agentbus up`. The dispatcher service
never runs compose: the compose file is the repository's.

**Step 4, ACL users,** the largest. It is built only after §5 Q3 and Q4:
- the default user disabled (`user default off`);
- `admin` for the person's setup, backup, restore and project delete, keeping
  `CONFIG SET requirepass` for the rotation (or the rotation becomes `ACL SETUSER`, a change of its
  own);
- `service` for the dispatcher and conductor: data commands, `EVALSHA` **and `EVAL`** (the
  fallback after a restart; without it every fenced write fails), `CONFIG GET`, `INFO` and `SCAN`;
- `agent` for every agent's CLI: data commands, `EVALSHA` and `EVAL`, without `@dangerous` (which covers
  `FLUSHALL`, `FLUSHDB`, `CONFIG`, `KEYS`, `DEBUG`, `SHUTDOWN`, `ACL` and `MODULE`), and without
  `DEL` of `*`;
- `busmon` for the person's dashboard.

The client picks its user from its identity (`AGENT_BUS_AGENT` is an agent; the services' env
is the service), with one password file per user, 0600. The key-level part ("no agent writes
master's keys") needs an inventory of each key kind's writers. `bus/keykinds.go` lists the kinds,
not who writes them. Every fenced Lua script runs under the caller's ACL, and the keys it
touches must match that user's patterns.

**The recommendation:** step 1 now; steps 2 and 3 next; step 4 command-level first, key-level only
if a mistake shows the need.

## 4. Tests a build owes

- Step 1:
  - the compose file's config holds `appendfsync everysec` and `save` (a test reads the file);
  - `EvaluateBrokerPersistence`: AOF with `appendfsync no` gives a warning; `everysec` gives OK;
    a refused `CONFIG GET` gives unknown.
- Step 2:
  - on a disposable broker started by the test with a socket and `port 0`: `Connect` over
    `unix://` works, and TCP 6380 is closed;
  - the installer makes the directory 0700 (fake runner);
  - `remote-bus.sh` builds the socket forward (a shell test with `ssh` stubbed).
- Step 3: every caller in §1's list uses `podman` when `broker.runtime=podman` (fake runner; no
  real runtime runs in tests). A test fails on any new literal `"docker"` outside the helper.
- Step 4:
  - on a disposable broker with the ACL file: `agent` is refused `FLUSHALL`, `CONFIG SET` and
    `KEYS`;
  - every fenced script the agent CLI runs passes under `agent`, the suite's own scripts
    included, both by `EVALSHA` and after `SCRIPT FLUSH` (the `EVAL` fallback);
  - the rotation passes under `admin`;
  - `default` is refused.
- Tests never touch the production broker.

## 5. What the person needs to decide

1. **macOS and the socket.** Keep loopback TCP with the container (recommended: the macOS
   container lives in a VM, and a socket does not cross it), or run Redis or Valkey natively,
   from Homebrew under launchd, with a socket.
2. **The data directory in backups.** The logical dump already restores every project's keys,
   and checks the broker's version. Copying the raw directory (`BGSAVE` then a copy of the RDB
   and the AOF directory, out of the container's volume) adds the runtime's commands to every
   backup. Recommended: no; the logical dump stays the backup.
3. **ACLs: command-level first?** Four users, with `agent` stripped of `@dangerous`, and key-level
   rules later? Recommended: yes.
4. **"Master's keys":** which keys are they? The pilot lease, the plan, the asks, the merge queue's
   record? Without a list, key-level ACLs need the writer inventory first.
5. **Valkey:** a `broker.image` setting (`redis:8-alpine` by default, `valkey/valkey:8`
   accepted), with the restore's version check taught Valkey's version field? Recommended: yes,
   with step 3.
6. **The rotation now:** run `./setup.sh rotate-redis-password` on this host, to end the
   repeated warning? It needs only the person.
