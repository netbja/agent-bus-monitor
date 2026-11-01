# Backup and restore: the state before a disaster

Status: spec, not built. Plan v0.26 task `backup`, review subject `backup-spec`.

## Why

A team's state is spread over a broker and three directories: the Redis keys of each project
(board, verdict ledger, asks, plan facts, streams), the config directory, the project templates
and the history archive. A lost disk, a `busmon --delete` by mistake or a broken upgrade loses
the board and the 4-eyes ledger, which nothing else holds. Shared memory is already in Git.

Bernard's decisions (2026-10-31): a **complete** backup, enough to restore the state before the
disaster; **secrets optional, and then encrypted**, never in clear; a **configurable
destination** with a proposed default; simple and fast.

## Commands

```
agentbus backup  [--project <p>]... [--dir <d>] [--secrets] [--dry-run]
agentbus backup list [--dir <d>]
agentbus restore <archive> [--project <p>]... [--redis-only | --files-only] [--dry-run] [--force]
```

Both sit above the project-required exit (they name their projects, or all). `backup` with no
`--project` takes every project of this host: the broker's (`bus.Projects`) and every template
in the projects directory. `--dry-run` prints what would be read or written and touches nothing.

## What an archive holds

One file, `agentbus-backup-<host>-<UTC yyyymmddThhmmssZ>.tar.gz`, mode 0600, written to a
temporary name in the destination and renamed when complete (a crash leaves no half archive
under the final name):

```
manifest.json
redis/<project>.jsonl          one line per key
config/config.toml             (and busmon.toml, quota.toml, models.toml when present)
config/projects/<project>.toml the template
roles/<project>/roles.toml     the manifest the template's ROLES_TOML names, and its roles/*.md
state/<project>/history/...    the archive's day files, gaps.jsonl, cursor.json
secrets.age                    only with --secrets
```

`manifest.json`: format version, `created_at` (UTC), host, agentbus version, the broker's Redis
version, the projects, and one row per member (path, size, SHA-256, mode). It also lists what
was **left out** and why (`services/<p>.env: secret (use --secrets)`, `redis.env: secret`), so
a restore says what it cannot bring back.

**Not included**: logs and restart budgets (`<state>/<p>/*.log`, `*.restarts`), the cache
(`~/.cache/agentbus`: rebuilt), the clients' own files, and the repository (Git).

## Redis: a consistent read without stopping the broker

Per project: `bus.ProjectKeys` (the validated `SCAN MATCH <p>:*`, so a sibling project is never
read), then **one `MULTI`** holding, for each key, `TYPE`, `PTTL` and `DUMP`: the server runs it
as one step, so the keys are read at one instant and no write lands between two of them. A key
created after the SCAN is not in the archive (said in the manifest's note); a key deleted after
it reads as absent and is skipped. The cost: the server serves nobody else for the whole `EXEC`
(every `DUMP` serialised in it). Small today (streams capped near 1000 entries, the verdict
ledger at 10000), it grows with the project; the backup prints the time the `EXEC` took.

**A key with any expiry is skipped**: only `PTTL = -1` (no expiry) is kept, so a key a
millisecond from expiring (`PTTL = 0`) is skipped too, and no lease can come back persistent.
They are live state by design: the dispatch and conductor leases, the delivery lease, the pilot,
swap intents, `observed`, `context` and health's daily-wake marker. Every other key goes in as
`{"key", "type", "dump": <base64>}`. `DUMP` keeps a stream's entries, consumer groups and their
cursors and pending lists, so the at-least-once delivery state comes back with it.

### Record keys and runtime keys

Every key kind is in one table (`bus`, beside `projectKinds`), each **record** or **runtime**; a
test fails when a kind written anywhere in `bus/` is in neither, so a new key is classified when
it is added.

- **Record** (restored): the four streams and their groups, `verdicts`, `board`, the requests,
  `asks`, `plan` and `plan:facts`, `gate:*`, `swaps`, `health`, `health:log`, `health:notes`,
  `quota:hist`, the missions' `change:*` and `missions:*`.
- **Runtime** (in the archive, **not** restored by default): `services` (the `installed` marker
  and each service's state with its old pid), `agents` (old verified pids and sessions), `wake:*`,
  `handoff`, `quota` (the hold and once marks), `pause`, `fallback`, `usage`, `budget`, `tokens`,
  `sessions`, `doctor`. They describe processes and readings that died with the disaster; the
  services and the agents write them again within a minute of `agentbus up`. `--with-runtime`
  restores them too, for a person who knows why.

The `DUMP` payload carries the RDB version, which changes within a major (7.0, 7.2, 7.4): a
restore onto a broker **older than the manifest's version**, by any part of it, is refused
before any write (see open question 3).

## Secrets: only with `--secrets`, only encrypted

The secrets are the password file (`redis.env`) and the services' env files
(`services/<p>.env`, which can hold `REDIS_URL`/`REDIS_PASSWORD`). Without `--secrets` they are
listed as left out, never read.

With `--secrets`, they are encrypted with **age** to a recipient, the public half of a key the
person holds:

- `age` must be on PATH; without it `--secrets` refuses before anything is written.
- The recipient is `backup.age_recipient` in `config.toml` (an `age1…` public key: not a secret)
  or `--recipient <age1…>`; none = refused.
- Each secret file is read and piped straight into `age -r <recipient>`; the clear text never
  touches the disk, the archive or a temporary file. The result is the member `secrets.age` (a
  tar of the secret files, encrypted whole).
- agentbus never creates, reads or stores the private key. The person keeps it where they keep
  their other keys (a password manager, `~/.config/age/key.txt` on another machine); without it
  `secrets.age` cannot be opened, by anyone, which is the point. The guide says so in one line.
- Restoring secrets: `restore` pipes `secrets.age` into `age -d -i <identity>` only when given
  `--identity <file>`; otherwise it restores everything else and names the secrets it skipped.

## Restore: never over a live project without `--force`

`restore` first verifies every member against the manifest's SHA-256 (a changed archive is
refused whole), takes the project's `upLock` (so no `up` or `start` runs beside it; held = refused,
fail closed), then checks per project.

**A live project is never restored, with or without `--force`.** Live means any of: its
dispatch or conductor lease held, its services active (the manager's own status), its herdr
workspace open, or a registered agent's verified pid alive. An empty keyspace is not proof of a
dead project (keys can be gone while agents run): each of these is checked whatever the keys.

- **No key** for the project (`bus.ProjectKeys` empty): restored.
- **Keys present**: refused unless `--force`; with it, the prompt states, as `busmon --delete`
  does, the key count, agent count and last activity of what it replaces (a non-TTY stdin without
  `--yes` is no), and the keys found are deleted in the same script as the restore (below).

**All or nothing, per project.** The Redis part is one Lua script (atomic: nothing interleaves):
it checks again that both leases are absent, deletes the keys `--force` named, checks that none
of the archive's keys exists, then `RESTORE`s each one (no `REPLACE`) under `redis.pcall`. On any
error (a key that appeared, a payload refused, memory) it deletes every key this script wrote
and returns the error: the project is as it was before the script, except the keys `--force`
asked to delete. Like the backup's `EXEC`, the script holds the server for its length. After it,
a fresh `ProjectKeys` lists any key that is neither restored nor in the archive (written by
something between the preflight and the script) as a warning.

Files come after a successful Redis part.
- **Files**: an absent file is written (its mode from the manifest, 0600 under 0700); an identical
  one is skipped; a different one is listed and kept, unless `--force`, which replaces it through
  a temporary file renamed over it (a symlink's target, as `config.Set` does).
- After a restore, it prints the next step: `agentbus up <project>` (the services and the
  `installed` marker are `up`'s; restore writes no unit).

Each file is its own temporary file and rename, so a failure leaves some files written and the
rest not: a second `restore` finishes it (written ones read identical and are skipped).

`--dry-run` prints, per member and per key, `write`, `skip (identical)`, `skip (runtime)`,
`keep (differs)` or `refused (<why>)`, and writes nothing.

## Destination and retention

`backup.dir` in `config.toml`, default `$XDG_DATA_HOME/agentbus/backups` (else
`~/.local/share/agentbus/backups`), created 0700. The guide says plainly that a backup on the
same disk does not survive the disk, and how to point `backup.dir` at another one (a mount, a
synced folder).

`backup.keep` (default 7): after a successful backup, the oldest archives of this host beyond
that count are removed; `0` keeps every one. Only files matching the archive name pattern are
ever removed.

## Speed

One `MULTI` per project, files streamed into the tar as they are read, gzip at its default
level. The history archive dominates (capped by `history.max_mb`, 256 MiB by default); the rest
is small. No step waits on the network.

## Sentinel and health

A health check `backup`: **warn** when the newest archive in `backup.dir` is older than
`backup.warn_after` (default 7 days; `0` = off) or there is none, **ok** otherwise, **unknown**
when the directory cannot be read. Its place is **for you**, with the command `agentbus
backup`: only the person decides to run one (it reads secrets with `--secrets`, and it writes to
a place the person chose). Sentinel only relays it as any warn; it never runs a backup or a
restore. See open question 1.

## What it never does

- Write a secret in clear, anywhere; read a secret file without `--secrets`; create or read an
  age private key.
- Read or restore another project's keys (`ProjectKeys`' exact prefix).
- Restore over a live project, with or without `--force`; over existing keys or files without
  `--force`; leave half a project's keys after a failure.
- Bring back a runtime key (the `installed` marker, old pids, wake records, holds) without
  `--with-runtime`.
- Stop, restart or reconfigure the broker; write a service unit or the `installed` marker.

## Tests (a disposable Redis only)

Through `AGENTBUS_TEST_REDIS_URL`, never the person's broker:

- **Round trip**: a project with a stream that has a consumer group and pending entries, a hash,
  a string, a key with a TTL and runtime keys (`services`, `agents`, `wake:x`); backup;
  `DeleteProject`; restore: every record key back byte for byte (`DUMP` compared), the group's
  cursor and pending list intact, the TTL key absent, the runtime keys absent (present with
  `--with-runtime`).
- **Expiry boundary**: a key whose `PTTL` reads 0 (set with `PEXPIRE` and read in the same
  `MULTI` through a fake that returns 0) is not in the archive; `PTTL = -1` only is kept.
- **Classification**: every key kind written in `bus/` is record or runtime in the table.
- **Prefix**: a sibling `demo-2` is neither read nor touched when `demo` is backed up or
  restored.
- **Refusals**: restore onto a project with keys (no `--force`); a live project with no key at
  all (a live agent pid, an open workspace, active services), with and without `--force`; a held
  dispatch **or conductor** lease, with and without `--force`; `upLock` held; a member whose
  SHA-256 differs; a broker older than the manifest's version by a minor or a patch. Each writes
  nothing (the project's keys compared before and after).
- **All or nothing**: a key created before the script, after the preflight (a hook in the fake
  between the two), and a corrupt payload in the middle of the list: the script returns an
  error and the project's keyspace is exactly what it was before (no key from the archive left).
- **Files** (temporary `XDG_*` dirs): absent written with its mode, identical skipped, differing
  kept, `--force` replaces through a rename and keeps a symlink a link.
- **Secrets**: default run reads no secret file (the fake file system records the opens) and
  lists them as left out; with `--secrets` and a fake `age` on PATH, the archive's bytes never
  contain the test password (`pw-SECRET`), and without `age` or a recipient it refuses before
  writing.
- **Retention**: `keep = 2` with four archives and one foreign file: the two oldest archives
  removed, the foreign file kept.
- **Health**: none, old, fresh, unreadable → warn, warn, ok, unknown.

## Open questions for Bernard

1. **Automatic backups?** Proposed: only by hand, with the health reminder after 7 days. The
   alternative is a daily backup by the dispatch service (without `--secrets`), at the cost of a
   service that writes outside its state directory.
2. **The default destination.** `~/.local/share/agentbus/backups` is on the same disk; is there a
   folder you want as the default instead (a synced folder, another mount)?
3. **`DUMP` or a portable export?** `DUMP`/`RESTORE` is exact and fast but tied to the Redis
   version (a restore onto any older broker is refused). A logical export (streams by `XRANGE`,
   hashes by `HGETALL`, groups rebuilt) survives any version but is more code and slower.
   `DUMP` is proposed.
4. **Secrets by key or by passphrase?** Proposed: an age recipient key (no prompt, scriptable).
   `age -p` (a passphrase typed at each backup and restore) needs no key file but cannot run
   unattended.
