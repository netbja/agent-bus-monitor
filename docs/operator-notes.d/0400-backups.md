- **Backups** (spec `docs/superpowers/specs/2026-11-01-backup-design.md`, package `backup/`, being built step by
  step): `bus/keykinds.go` classes every `{p}:<kind>` record (restored) or runtime (archived, restored only with
  `--with-runtime`), a ":*" kind one `ValidName` tail only; `TestEveryWrittenKeyKindIsClassified` scans the module for
  kinds written (literals joined with `+`, and literal kinds given to any bus function that routes a parameter into
  `StreamKey`, found to a fixpoint), so a new key is classified when it is added. `bus.DumpProject`: one `MULTI` of
  `TYPE`/`PTTL`/`DUMP` per project (`ProjectKeys`' exact prefix), only `PTTL = -1` kept. `backup.Writer`: a `.tar.zst`
  (klauspost zstd, default level, one encoder goroutine, frame checksum), `coverage.json` first, every member hashed,
  `manifest.json` last; any failed `Add` abandons the archive; `Verify` checks every member before anything is
  restored; `Run` writes a temporary file in the folder and links it to its final name (never replacing one).
  `agentbus backup` (`backupcmd.go`, above the project-required exit; `--project` only from the command line, never
  `AGENT_BUS_PROJECT`): every project of the host (broker + templates), `backup.dir`/`backup.keep` (group Backup),
  `backup list`, `--dry-run`; secrets (`redis.env`, `services/<p>.env`) only stat'ed, left out and listed.
  Retention never removes the newest archive covering a project. `agentbus restore` (`restorecmd.go`,
  `backup.Restore`): `Verify` first (member names must be clean relative paths), the broker's version not older than
  the manifest's in any part, the project's `upLock`, then `projectLive` (dispatch/conductor lease, a service active,
  the herdr workspace open, a registered agent's verified pid alive; a read that fails refuses, and so does no service
  manager unless `AGENTBUS_SERVICE_MANAGER=none`) whatever `--force`; `bus.RestoreProject` is one Lua script (leases
  absent, no archive key present unless `--force` deletes it, checked before any delete; `--force`'s keys `DUMP`ed with
  their `PTTL` then deleted; `RESTORE` under `pcall`; on error every written key deleted and every forced key put back,
  `REPLACE` with its TTL, so the project is exactly as it was); records only unless `--with-runtime` (unknown keys too); files
  after a successful Redis part, each verified against the manifest before its rename (`Walk`, `Verified`), a
  symlink's target replaced, the roles destination only an absolute clean `.../roles.toml` from the archive's
  template; the host's settings only without `--project`. Secrets (`backup/secrets.go`): `--secrets` needs `age` on
  PATH and a recipient (`--recipient` or `backup.age_recipient`, `config.ValidAgeRecipient`: an `age1…` public key),
  else refused before anything is written; a tar of `redis.env` and `services/<p>.env` is streamed into `age -r` (no
  clear text on disk) and the ciphertext is the member `secrets.age`; `restore --identity <file>` pipes it into
  `age -d -i` (agentbus never reads the key), each secret restored 0600 by the file rules, the password file only
  without `--project`. `backup.after_command` (`backup/after.go`, `RunAfter`): config.toml alone (`Key.PersonOnly`: no env,
  `MachineOnly`, `config.Set`/`Unset` refuse it; `after_env` a TOML list kept as a comma-joined string), split by
  `rolecfg.ShellSplit` and run with no shell in `backup.dir`, its own process group (SIGTERM at `after_timeout`, SIGKILL
  10 s later), the `AGENTBUS_BACKUP_*` variables, an environment scrubbed of secret-named or `health.Sensitive`
  variables but those `after_env` names; output capped at 64 KiB, each line `health.Redact`ed; run after the rename and
  before the retention, never on a refused run, its outcome in `<state>/backup-last.json` `after` per project
  (`RecordAfter`, 0600); a failure only warns. Every role denies `Edit` of `~/.config/agentbus/config.toml` (an Edit rule covers the Write tool; Claude Code never matches a `Write(path)` rule and warns about one at each launch, `neverMatches` in `rolecfg/configdeny_test.go` keeps them out)
  (shipped `[tool_rules]`), and a Claude Code launch adds the path `XDG_CONFIG_HOME` gives it (`rolecfg.ConfigDenies`,
  `//` absolute form; a relative one adds nothing and says so). `backup.schedule = daily` (`backupschedule.go`, `scheduledBackupPass`;
  `dispatch.BackupConfig`, an hourly pass only while `HoldsDispatch`): due when a project of the host has no archive
  covering it under 24 h (`backupDue`, per project), a host try-lock `<state>/backup.lock` (`backupLock`, `LOCK_NB`),
  due judged again under it, then `makeArchive` (the same as by hand, `Scheduled`: `AGENTBUS_BACKUP_SCHEDULED=1`);
  secrets only with `backup.age_recipient`, without `age` the archive is made without them and `SecretsError`
  recorded; `backup-last.json`'s `scheduled` part (`RecordScheduled`) is written only by the chore. `agentbus backup`
  warns about a defaulted `backup.dir` while `daily` is on. busmon's C page shows the three `backup.after_*` keys (Backup group, `settingOnPage`: only its
  `PersonOnly` keys) read-only: Enter says "edit config.toml yourself", `d` offers nothing. Below them (backup-page,
  `settings_backup.go`, read with the rows in the page's command: `readBackupFacts`, nothing without a HOME or XDG folder):
  the folder, `Back up now` (Enter, then `y`: `agentbus backup`, `--secrets` when `backup.age_recipient` is set and `age` is on PATH (else left out and said, as the schedule does), run as Z
  runs suspend, `startRun` in `sleep.go`: one child at a time, its own session, output in `<state>/agentbus/backup.log`,
  its last line the notice, its end reading the page again), one row per archive of this host (`backup.List`: date, size,
  projects, secrets or not), and the last runs from `backup-last.json` (errors through `health.Redact`); Enter on an
  archive opens its coverage, the restore dry runs to copy (`y`, the path shell-quoted) and what takes each project offline first (`agentbus down <p>`, its herdr workspace closed: a suspend keeps the agents and the workspace, which restore refuses even with `--force`; `backupOfflineSteps`); busmon restores nothing. Health `backup` (`health/backup.go` pure, `EvaluateBackup`; `cmd/agentbus/backupcheck.go` the reader; 15 min,
  for you, `agentbus backup`): per project, warn on no covering archive younger than `backup.warn_after` (168h, `0` =
  this reminder off), on the latest scheduled attempt covering it failed while `daily` (cleared by a newer covering
  archive; the age case only by one with secrets, read from `coverage.json`'s `secrets`), on its latest
  `after_command` run failed while one is set; unknown when the folder or the record cannot be read; the detail names
  the folder.
