# Backup and restore: the state before a disaster

Status: spec, not built. Plan v0.26 task `backup`, review subject `backup-spec`. Bernard's answers
to the four open questions recorded 2026-11-02 (subject `backup-answers`, see "Answers" at the end).

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
agentbus restore <archive> [--project <p>]... [--redis-only | --files-only] [--with-runtime] [--dry-run] [--force]
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

`manifest.json` is the archive's **first member**, so a reader learns what an archive covers by
reading its first bytes without unpacking the rest. **Coverage**: an archive *covers* a project
when its manifest lists that project. Only a complete archive (final name, after the rename)
counts. Freshness is always judged **per project**: the newest archive that covers it. An archive
of `--project A` says nothing about `B`.

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
before any write. That is the accepted cost of `DUMP` (answer 3): bringing a backup to an older
broker means upgrading that broker first.

## Secrets: only with `--secrets`, only encrypted

The secrets are the password file (`redis.env`) and the services' env files
(`services/<p>.env`, which can hold `REDIS_URL`/`REDIS_PASSWORD`). Without `--secrets` they are
listed as left out, never read.

With `--secrets`, they are encrypted with **age** to a recipient, the public half of a key the
person holds (answer 4: a recipient key, no passphrase; `age -p` is not offered, since it
prompts and cannot run unattended):

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

`backup.dir` in `config.toml` (answer 2), default `$XDG_DATA_HOME/agentbus/backups` (else
`~/.local/share/agentbus/backups`), created 0700. `--dir` overrides it for one run. The guide says
plainly that a backup on the same disk does not survive the disk, and how to point `backup.dir` at
another one (a mount, a synced folder). Bernard's own machine sets `backup.dir =
"/data/backups/agentbus"` in its `config.toml`; that is his setting, not the default.

`backup.keep` (default 7): after a successful backup, the oldest archives of this host beyond
that count are removed; `0` keeps every one. Only files matching the archive name pattern are
ever removed. An archive that is the newest covering some project is never removed, whatever the
count: a run of `--project A` archives cannot push out the only archive of `B`.

## Schedule: by hand by default, daily when the person asks

Answer 1: backups are made by hand by default, with the health reminder after 7 days (below). The
person can make them automatic in one setting:

`backup.schedule = off | daily` (default `off`).

- **`off`**: nothing runs a backup but the person's `agentbus backup`.
- **`daily`**: the dispatch service runs one. This is a chore on its own goroutine, beside the
  reconciliation, as the history archive's is. It runs only while the service holds its dispatch
  lease (`HoldsDispatch`).
  - **When:** the chore checks once an hour. It runs when **any** project of this host (the
    broker's and the templates', as `backup` with no `--project` takes them) has no archive
    covering it that is younger than 24 h. A backup made by hand counts for the projects it
    covers, and only those: repeated `--project A` archives never hold back the run while `B`
    goes stale. No clock time needs configuring. A failed run is tried again at the next hourly
    check.
  - **One per host:** it takes a host-local flock, `<state>/backup.lock`, as the model and
    Redis-release refreshes do (`refreshModelsUnderLock`). The lock is a try-lock (`LOCK_NB`):
    when another chore holds it, this one skips its turn. After taking the lock, it judges the
    coverage again, so a chore that waited behind another's backup finds every project covered
    and makes none.
    Every project's dispatch service runs the chore, but only one backup is made.
  - **What it runs:** the same code as `agentbus backup` with no `--project`, so every project of
    this host, followed by the retention (`backup.keep`).
  - **Secrets:** never unattended unless an age recipient is configured. The chore includes the
    secrets only when `backup.age_recipient` is set: encryption to a recipient needs no private key
    and no prompt. With no recipient, the secrets are left out and listed as left out in the
    manifest. With a recipient but no `age` on the service's PATH, the archive is still written
    without the secrets, and the run records the error. A backup without its secrets beats no
    backup, and the error does not go unseen (health, below).
  - **Record:** the run's outcome goes to `<state>/backup-last.json`, 0600, never a secret. It
    holds `at`, `scheduled`, the archive's name or the error, the projects, and the `EXEC` time per
    project. The service log carries the same line.
  - **Settings:** read through `config.Machine`: the environment, then that environment's
    `config.toml`. The service's environment is not the person's shell, so a default that
    depends on `XDG_DATA_HOME` can resolve to another folder there. Archives made by hand would
    then land in one folder, while scheduled ones, and the health check, would use another. So
    with `daily`:
    - the guide asks for `backup.dir` set explicitly in `config.toml`;
    - `agentbus backup` warns when `backup.dir` comes from the default while `daily` is on;
    - the health check's detail always names the folder it read.

What the person accepts with `daily`: a service now writes outside its state directory, into
`backup.dir`. Once a day the broker serves nobody else for each project's `EXEC` (see "Redis"
above); its length is in the record. Restore is never scheduled: only the person runs it.

## Settings

All of them are in the registry (`config/keys.go`), group Backup, read by `agentbus backup` and the
dispatch service:

| Key | Default | Meaning |
|---|---|---|
| `backup.schedule` | `off` | `off` or `daily` (above) |
| `backup.dir` | `$XDG_DATA_HOME/agentbus/backups` | where archives go |
| `backup.keep` | `7` | archives of this host kept; `0` keeps every one |
| `backup.warn_after` | `7d` | the health reminder; `0` = off |
| `backup.age_recipient` | none | an `age1…` public key: not a secret |

## Speed

One `MULTI` per project, files streamed into the tar as they are read, gzip at its default
level. The history archive dominates (capped by `history.max_mb`, 256 MiB by default); the rest
is small. No step waits on the network.

## Sentinel and health

A health check `backup`. It runs in each project's health loop and judges **that project**, from
two independent facts:

- **Age:** **warn** when the newest archive covering the project is older than
  `backup.warn_after` (default 7 days), or there is none. `backup.warn_after = 0` turns off this
  reminder only.
- **Failure** (only while `backup.schedule = daily`): **warn** when the latest scheduled attempt
  whose scope included the project failed, whatever the age of older archives. A successful
  backup yesterday followed by a failed run today warns today. A run that left out the secrets
  because `age` was missing (with a recipient set) counts as failed for this purpose. The
  warning clears when a newer attempt or archive succeeds for the project: a later scheduled run
  that succeeds, or a complete archive covering the project created after the failed attempt,
  including one made by hand. For the `age` case, that newer archive must hold `secrets.age`.
  `warn_after = 0` never silences a failure. With `schedule = off`, the attempt record is not
  read: a failure from an earlier `daily` period no longer warns.

**Ok** when neither warns; **unknown** when the directory or the record cannot be read. The
detail names the folder read (always), the age of the covering archive, and the failed attempt's
time and error. Its place is **for you**, with the command `agentbus backup`. Sentinel only relays
it as any warn; it never runs a backup or a restore.

## What it never does

- Write a secret in clear, anywhere; read a secret file without `--secrets`; create or read an
  age private key.
- Read or restore another project's keys (`ProjectKeys`' exact prefix).
- Restore over a live project, with or without `--force`; over existing keys or files without
  `--force`; leave half a project's keys after a failure.
- Bring back a runtime key (the `installed` marker, old pids, wake records, holds) without
  `--with-runtime`.
- Stop, restart or reconfigure the broker; write a service unit or the `installed` marker.
- Run a backup unattended unless `backup.schedule = daily`; include secrets in an unattended run
  without `backup.age_recipient`; run a restore unattended, ever.

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
- **Retention by coverage**: `keep = 2`, an old A+B archive then three newer `--project A` ones:
  the A+B archive is kept (the newest covering B), only the oldest A-only archive is removed.
- **Health**: none, old, fresh, unreadable → warn, warn, ok, unknown (per project, by
  coverage). Failure, independently of age:
  - a fresh earlier success plus a failed scheduled attempt: warn;
  - a newer success (scheduled, or by hand covering the project): ok;
  - an `age`-missing run: warn until an archive with `secrets.age`;
  - `warn_after = 0` with a failed attempt: still warn;
  - `schedule = off` with an old failed record: ok.
- **Schedule** (a fake clock, a temporary `backup.dir`, a fake bus): `off` never runs;
  - `daily` runs when a project has no covering archive younger than 24 h, or none at all, and
    does not run when every project is covered (an archive made by hand covers its projects);
  - with projects A and B, a fresh `--project A` archive and B stale or never backed up: it runs;
  - a manifest that is not the first member, or an unfinished archive under its temporary name,
    covers nothing;
  - not without the dispatch lease;
  - two chores (two projects' services) under one host lock make one archive: the second either
    skips while the lock is held, or judges the coverage again after taking it;
  - the health detail names the folder read, and `agentbus backup` warns about a defaulted
    `backup.dir` while `daily` is on;
  - without `backup.age_recipient`, no secret file is opened (the fake file system records the
    opens);
  - with one and a fake `age`, the secrets are in `secrets.age` only;
  - with one and no `age`, the archive is written, the error recorded and the health check warns;
  - `backup-last.json` is 0600 and holds no secret.

## Answers (Bernard, 2026-11-02)

The four questions this spec left open, as Bernard answered them. The text above is written to
match.

1. **Automatic backups?** By hand by default, with the 7-day health reminder, but everything is
   configurable to become automatic. Hence `backup.schedule = off | daily`, default `off`. `daily`
   is run by the dispatch service, never with secrets unattended unless an age recipient is
   configured. See "Schedule".
2. **The default destination.** `~/.local/share/agentbus/backups` (`$XDG_DATA_HOME/agentbus/backups`),
   configurable with `backup.dir`. On Bernard's machine, `backup.dir = "/data/backups/agentbus"` is
   set in `config.toml`.
3. **`DUMP` or a portable export?** `DUMP`/`RESTORE`. A restore onto an older broker stays
   refused; upgrading that broker first is the way through.
4. **Secrets by key or by passphrase?** An age recipient key, with no prompt (`backup.age_recipient`
   or `--recipient`). No passphrase mode.
