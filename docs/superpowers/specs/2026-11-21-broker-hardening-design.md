# Harden the broker, lightly

Status: spec r2, for review (subject `broker-hardening`). Not built. Base: origin/dev `4f095697`.
r2 corrects three claims of §1: `CONFIG SET` is used, `EVAL` is sent, and `docker` has more callers
(foureyes). r3 follows the directive where r2 departed from it (reviewer-codex):
- the data directory goes into backups (step 5);
- the logical dump's omission of keys with a TTL is stated;
- key-level ACLs are part of step 4, with the proposed table and the inventory that confirms it;
- a `master` user is added;
- busmon's grants are listed;
- the compose healthcheck gets its own user.

r4 makes these changes (foureyes, reviewer-codex):
- `scripts/bootstrap` is listed as a `docker` caller, and `agentbus up` already starts the broker;
- `RunKey` is named among `publishRequest`'s keys;
- under step 4 the rotation is `ACL SETUSER`.

r5 makes step 5's copy the finished RDB alone, a point-in-time snapshot, never the live AOF
files; it also names the `aclfile` line (reviewer-codex, foureyes). r6 keeps `users.acl` through
the restore, and puts `user default off` in it (foureyes).
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
| Valkey | Not supported (step 3 adds `broker.image`, §5 Q5 as amended). The image is fixed to `redis:8-alpine`. Clients would connect (same protocol). The restore's version check reads `redis_version` (`backup/restore.go:96`); Valkey reports a compatibility value there. | No way to choose it, and the version check is untested on Valkey. |
| Data directory in backups | The data is the compose volume `redis-data` (`RedisVolume`, `setup/redis.go:128`). `agentbus backup` is **logical**: a `DUMP` of each project's keys (`bus/backupdump.go`, `backup/run.go:129`), classified by `bus/keykinds.go`. **Only keys without a TTL are dumped** (`keepDump`: `PTTL == -1`). A key that expires is listed as skipped (`Expiring`) and not archived. That covers the leases, intents and marks with a TTL, by design, so the logical backup is not a full copy of the broker. A restore refuses an older broker. | The data directory is not copied, and the directive asks for it (step 5). |

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
    (`cmd/busmon/services_cards.go:130`);
  - **`scripts/bootstrap`**, on the main path: `ensure_broker` is `docker compose up -d`
    (`:70`). It is called by the verbs `new` and `recall` (`:120`, `:125`), and `agentbus up`
    runs bootstrap every time (`cmd/agentbus/up.go:205`). Under podman without the shim,
    `agentbus up` stops there;
  - `scripts/busmon-demo.sh`, the docs' screenshot tool.

  Podman works only through its docker shim. Without it, the installer says to start the broker
  by hand, and those checks and pages read `unknown`.
- **Without systemd:** the broker does not need systemd. The container's `restart:
  unless-stopped` is the docker daemon's job. **`agentbus up` already starts a stopped broker**,
  through `scripts/bootstrap`'s `ensure_broker`, above. Under rootless podman, a container restarts after
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

**Step 3, podman without the shim:** a `broker.runtime` setting (`docker`, `podman`).
- It is used by every `docker` caller in §1's list: the installer, status, preflight, uninstall,
  the rotation, the two health checks, busmon's Redis page and Services card, and
  `scripts/bootstrap`'s `ensure_broker`, which reads it through `agentbus config`.
- In Go it goes through one helper that names the binary, with `podman compose` as the compose
  command.
- "Without systemd" needs nothing new: `agentbus up` already starts the broker (§1). Only the
  runtime's name changes there. A reboot is covered by the person's own `agentbus up`.
- The dispatcher service never runs compose.

**Step 4, ACL users: commands and keys.**

**The users,** the person's four roles plus `admin` for setup:

| User | Who connects with it | Commands | Keys |
|---|---|---|---|
| `admin` | `./setup.sh`, `agentbus backup` and restore, run by the person (refused from an agent's process tree, `agentcaller.Refuse`) | all but `FLUSHALL`, `FLUSHDB`, `SHUTDOWN`, `DEBUG` and `MODULE`; `ACL SETUSER` for the rotation | `~*` |
| `master` | the agent whose identity is `master` (`AGENT_BUS_AGENT=master`) | data commands, `EVALSHA` and `EVAL`, `SCAN`, `INFO`; not `@dangerous` | read `*`; write everything `agent` writes, plus the master-only kinds below |
| `dispatcher` | the services (dispatcher, conductor, holder), from their env file | data commands, `EVALSHA` and `EVAL`, `SCAN`, `INFO`, `CONFIG GET`, `MEMORY USAGE`; not `CONFIG SET`, `FLUSH*`, `KEYS` or `SHUTDOWN` | read `*`; write everything except the master-only kinds |
| `agent` | every other agent's CLI | data commands, `EVALSHA` and `EVAL`, `SCAN`; not `@dangerous` (which covers `FLUSHALL`, `FLUSHDB`, `CONFIG`, `KEYS`, `DEBUG`, `SHUTDOWN`, `ACL` and `MODULE`, and `INFO`) | read `*`; write everything except the master-only and dispatcher-only kinds |
| `busmon` | busmon and the person's own CLI outside agents | data commands, `EVALSHA` and `EVAL`, `SCAN`, `INFO` (`RedisDetail`, `RedisInfo`), `MEMORY USAGE` (`ProjectMemory`), `CONFIG GET`, `DEL` (`--delete`), `XTRIM` (`--reset`); not `CONFIG SET`, `FLUSH*`, `KEYS`, `SHUTDOWN` or `ACL` | `~*` |
| `ping` | the compose healthcheck (`redis-cli --user ping`) | `PING` only | none |

`EVAL` is granted wherever `EVALSHA` is, because it is the fallback after a restart.

**Where the users live:** an ACL file. The compose config on stdin gains `aclfile
/data/users.acl`, on the data volume, so the file outlives the container and `ACL SAVE` can write
it. Every `user` line goes in that file, `user default off` included. The server refuses `user`
lines in its config when `aclfile` is set, and refuses to start when the file is missing. The
installer creates the file, 0600, before the first start with users.

**The rotation under step 4.** Today `./setup.sh rotate-redis-password` runs `CONFIG SET
requirepass` (`setup/rotate.go:31`), which sets the default user's password. With the default
user off, that rotates nothing anyone uses. Step 4 therefore changes the rotation:
- for each user in turn, it writes the new password file first (0600, as today), then runs
  `ACL SETUSER <user> resetpass ><new>` as `admin`, then `ACL SAVE` to the ACL file;
- `admin` itself goes last, with its new password used for the commands that follow;
- `CONFIG SET requirepass` stays only for a broker without ACL users, the step 1-3 state.

**The key kinds, proposed** (`<p>` is any project, so a pattern is `%W~*:<kind>`):
- **master-only writes:** `pilot` (the pilot lease); `plan`, `plan:facts`, `plan:ignore` (the plan,
  loaded by `planLoad`); `mergequeue:dropped` (a head master withdrew, `mergeDrop`);
- **dispatcher-only writes:** the leases and records its fenced scripts own: `dispatch`,
  `conductor`, `ci`, `mergequeue`, `mergequeue:due`, `missionops`, `missionops:results`, `run`,
  `reboot`;
- **everything else** (the streams, `board`, `asks`, `ideas`, `gate:*`, `agents`, `usage`, and the
  rest of `bus/keykinds.go`): any of `master`, `dispatcher`, `agent` and `busmon`.

This table is a proposal. Several scripts touch keys across these lines: `publishRequest` lists
`PlanKey`, `PlanFactsKey` and `RunKey` among its keys (`bus/requests.go:74`), although any agent
may send a request. `reserveWake` and the run scripts list `PauseKey`. **Step 4's first
PR is therefore the writer inventory, as a test.** On the disposable broker, it runs every
exported `bus` write under each user's ACL, and fails on a write the table forbids to a role
that does it. The table is corrected from that result before any ACL is enabled. That is also
where the person confirms "master's keys" (§5 Q4).

**Choosing the user.** The client reads its identity:
- `AGENT_BUS_AGENT=master` connects as `master`;
- any other agent as `agent`;
- the services' env file as `dispatcher`;
- busmon and the person's CLI as `busmon`;
- setup, backup and restore as `admin`.

There is one password file per user, 0600, in `~/.config/agentbus/`. `REDIS_URL` gains the user
name.

**Step 5, the broker's snapshot in backups** (the directive's "data directory"):
- after the logical dump, `agentbus backup` runs `BGSAVE`;
- it waits, bounded, until `INFO persistence` shows `rdb_bgsave_in_progress:0`, a
  `rdb_last_save_time` later than the request, and `rdb_last_bgsave_status:ok`;
- it then copies **only the RDB file** that save completed (`dbfilename`, in `CONFIG GET dir`)
  into the archive as `broker.rdb`:
  - the container: `<runtime> cp agent-bus-redis:/data/<dbfilename> -`, with the runtime of
    step 3;
  - a native broker: the file itself.

The RDB is written whole to a temporary file and renamed by Redis, so the copied file is one
complete point-in-time snapshot. It holds the keys with a TTL that the logical dump skips. **The
AOF files are never copied**: Redis keeps appending to them while the copy runs, so they are not
a snapshot. If the save does not finish within the bound, or reports an error, the archive is
logical only, with the reason.

**The restore of `broker.rdb` is manual,** a procedure in the operator notes, never automatic:
1. stop the broker;
2. in the data volume, remove the AOF directory and the old RDB, **keep `users.acl`**, and put
   `broker.rdb` in as `<dbfilename>`. A broker with `aclfile` set and the file missing does not
   start, and without it every user is gone;
3. start it once with `--appendonly no`, so that Redis loads the RDB rather than an AOF;
4. run `CONFIG SET appendonly yes`, which writes a fresh AOF from memory;
5. restart it with the normal config.

The logical restore stays the everyday one.

**The recommendation:**
- step 1 now;
- steps 2 and 3 next;
- step 4 begins with the inventory test, then the users and key patterns;
- step 5 with step 3, since both need the runtime helper.

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
  real runtime runs in tests). This includes `scripts/bootstrap` (a shell test with a stubbed
  `podman`). A test fails on any new literal `docker` outside the helper, in the Go code and in
  `scripts/` except `busmon-demo.sh`.
- Step 4:
  - the inventory test above;
  - on a disposable broker with the ACL file: `agent` is refused `FLUSHALL`, `CONFIG SET` and
    `KEYS`, and a write to `pilot` or `plan`; `master` may write them; `dispatcher` may write
    `dispatch`, and `agent` may not;
  - busmon's reads (`RedisDetail`, `ProjectMemory`, project discovery) pass under `busmon`;
  - the compose healthcheck passes as `ping`, and `default` is refused;
  - every fenced script the agent CLI runs passes under `agent`, the suite's own scripts
    included, both by `EVALSHA` and after `SCRIPT FLUSH` (the `EVAL` fallback);
  - the rotation, with `default` off, changes each user's password: the old one is refused and
    the new one accepted, `admin` last; a broker without ACL users still rotates through
    `requirepass`.
- Step 5:
  - the archive holds `broker.rdb`, copied only after `rdb_bgsave_in_progress:0` and status `ok`,
    and no AOF file;
  - a `BGSAVE` that does not finish in time, or fails, leaves a reason and a logical-only archive;
  - the restore procedure, run on a disposable broker in a test: `broker.rdb` loaded with
    `--appendonly no`, then AOF switched on, gives back the keys, TTL keys included. The users
    of `users.acl` still authenticate, and `default` is still refused;
  - no real runtime runs in tests (fake runner).
- Tests never touch the production broker.

## 5. What the person needs to decide

1. **macOS and the socket.** Keep loopback TCP with the container (recommended: the macOS
   container lives in a VM, and a socket does not cross it), or run Redis or Valkey natively,
   from Homebrew under launchd, with a socket.
2. **The data directory, as its snapshot:** step 5 copies the RDB that `BGSAVE` completed, not the whole directory, because the live AOF files cannot be copied consistently while the broker runs. Is that the "data directory in backups" you meant? And is restoring it manual only? Recommended: yes. An automatic
   swap of a live broker's files is the riskiest act in this spec.
3. **Users:** the five of step 4, with `admin` kept apart from `busmon` so that day-to-day
   watching cannot `CONFIG SET`? Recommended: yes.
4. **"Master's keys":** the proposed list is `pilot`, `plan`, `plan:facts`, `plan:ignore` and
   `mergequeue:dropped`. Should the asks, the board or the merge queue's record be added? The
   inventory test then confirms it.
5. **Valkey:** a `broker.image` setting (`redis:8-alpine` by default, `valkey/valkey:8`
   accepted). The person answered yes, with step 3. **Amended at the build (master's decision,
   cmd `1795225390975-0`):** the restore's check is not taught `valkey_version`.
   - **Why:** a DUMP payload's compatibility is the RDB format, not the product's version number.
     Valkey reports `redis_version` 7.2.4 for compatibility, so today's check already refuses a
     Redis 8 archive on Valkey. Read `valkey_version` instead, and Valkey 8 would pass a Redis 8
     archive whose keys it then refuses one by one (RDB 12 against 11).
   - **Instead:** the manifest records the server's product (`server`: `redis` or `valkey`, from
     INFO's `server_name` or `valkey_version`). A restore across products is refused with that
     reason. An archive from before the field is a Redis broker's. Within a product, the version
     check stays as it was.
   - **The image:** `broker.image` is compose's `image: ${AGENT_BUS_BROKER_IMAGE:-redis:8-alpine}`.
     Every compose call passes it from the setting (`brokerrt.ComposeEnv`: setup, the rotation's
     recreate, uninstall, `scripts/bootstrap`). A recreate without it would bring a Valkey broker
     back as Redis.
   - **Left open, not verified at the build:**
     - that the `valkey/valkey:8` image carries the `redis-server` and `redis-cli` names compose's
       command and healthcheck call (Valkey installs them as compatibility links by default);
     - the `redis-version` health check, which reads `redis_version` (7.2.4 on Valkey).

6. **The rotation now:** run `./setup.sh rotate-redis-password` on this host, to end the
   repeated warning? It needs only the person.
