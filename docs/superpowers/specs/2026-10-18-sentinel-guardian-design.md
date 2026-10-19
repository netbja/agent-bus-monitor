# Sentinel guardian, health checks, and Redis password design

Status: approved for implementation planning by Bernard. This is a specification, not an
implementation. The sentinel is a judge awakened by the dispatch service; deterministic checks
remain code and never call an LLM.

## Goal

Give each project a bounded, inspectable health picture without adding a polling agent or a second
service supervisor. The existing dispatch service runs deterministic checks on individual
schedules, records current results and transitions, and wakes the unarmed sentinel only for a new
failure episode and once a day. The sentinel investigates, applies a small allow-list of safe
repairs, annotates checks, and otherwise escalates to master or the person.

Generate a private Redis password during install, resolve it consistently across clients, and
migrate the historical default without cutting off running agents or the remote SSH-tunnel client.
Health is detection and alerting. Processes run as the same Linux user as the agents, so this is not
a security boundary or a lock against a compromised process.

## Architecture

1. `dispatch.Run` owns the `{p}:dispatch` lease and remains the only automated process that writes
   into agent panes. A health scheduler runs alongside its existing reconciliation and observation
   work. It schedules each check by its own interval; a slow check cannot delay wake reconciliation.
2. Each check returns a pure result: id, `ok|warn|fail|unknown`, safe detail, Redis-time
   `observed_at`, `since`, and a stable fingerprint. Filesystem, process, service-manager, Redis,
   and external-command reads sit behind small interfaces so evaluators can be tested with
   fixtures.
3. A lease-fenced Lua write updates one check. If its status changes, the same script appends one
   transition to `{p}:health:log`; entering `fail` also appends one ordinary addressed directive
   to sentinel in `{p}:cmd`. These effects are atomic. A retry of the same result updates its
   observation but creates neither a duplicate transition nor a second wake.
4. Unknown data stays unknown. A read error never means `ok`; it is recorded as `unknown`, with a
   bounded, sanitized explanation. A stale record is presented as unknown by readers after two
   configured intervals, even if the last stored result was `ok`.
5. The service checks its own lease before every write through the Lua fence. A lost lease cancels
   check processes and prevents all later writes. Checks have per-check deadlines and an overall
   bounded pass budget. External commands run in their own process group, killed as a group on
   timeout or service cancellation, following `mission/git.go`.
6. Health data is operational state in Redis. The sentinel does not write a measured result and the
   dispatcher never executes a proposed repair or project command. The OS service manager remains
   the only process supervisor.

Health keys are deliberately not project-liveness evidence. `{p}:health` and
`{p}:health:log` must be excluded from `projectKinds` and ignored by
`HasKeysBesidesDispatch`, as `{p}:observed` is today. Health writes are fenced by the dispatch
lease and require the project services marker. `busmon --delete` removes them with the other
project keys; a raced health write may leave an orphan key, but it must never make `busmon --list`
discover or a dispatcher resurrect a deleted project. Readers tolerate absent or malformed keys.

## Checks catalogue

Intervals below are initial defaults. Cheap reads run about once a minute; repository scans and
version inventory run less often. A per-check interval is stored in code, except project-defined
checks, whose interval is declared in their file. Thresholds are constants in the first version;
they must be named and unit-tested, not scattered through the loop.

| Check family | Method and initial cadence | `warn` / `fail` | Suggested remediation | `unknown` |
| --- | --- | --- | --- | --- |
| Services | Every minute, read `service.Manager.State`, `{p}:services`, the local restart budget, and a bounded tail of the rotated service log. | Warn on a recent recorded error or low remaining restart budget; fail when an installed dispatch or missions service is inactive/failed, or its budget is spent. An uninstalled conductor is not a failure. | Restart the affected service with `agentbus service restart` if its restart budget allows; otherwise ask the person to inspect the service log. | Manager unavailable, unreadable budget/log, or Redis service state read failure. Do not parse or retain raw log lines that may contain credentials. |
| Agent identity | Every minute, compare each applicable snapshot's `pid`, `pid_start`, pane, and terminal with a live process and `Herdr.Inspect`; verify the pane still matches that process. Check launcher evidence (`AGENT_BUS_ROLE` key presence or another proof) without printing process environment values. | Fail only on a positive contradiction (PID reuse or a different process occupying the verified pane); warn when an active/recent agent lacks sufficient proof. An ended/dormant agent is not failed merely because its old PID exited. | Re-check with `agentbus agents --json` and herdr; if identity still conflicts, ask the person to inspect the pane before any action. | `/proc`, herdr, or snapshot unavailable, or identity could not be checked. |
| Agent launch and identity | Every five minutes, verify active client argv has the expected launcher/settings evidence, that required role tool rules are present, and that the bus snapshot is keyed by the agent name rather than the legacy default identity. Inspect only required argv markers; never print argv or environment values. | Warn for a role not launched through `agent-launch`, missing required role rules, missing Claude `--settings`, or a default/mismatched bus identity. A non-Claude role is not required to have Claude settings. | Relaunch through `agentbus` with the configured role; do not edit a live pane's command or rules. | Client/profile cannot be established from a live process. |
| Model drift | Daily, reuse the `roles doctor` resolution and the fresh local model catalogue. | Warn for the same `drift`, `newer`, or `gone` verdicts the doctor publishes. | Review the role's configured model with `agentbus roles doctor`; update roles.toml only after human review. | Catalogue stale/partial or doctor source failed; never convert missing evidence to `gone`. |
| Runaway (works, nothing moves) | Every minute, for agents declared `working`: time since their last status, report, bus message or commit on their branch; growth of their context tokens (`{p}:usage`) over the same span; and, from the existing read-only pane observation, the same screen content repeating. Bernard decided: no per-task time or token caps (unrealistic to estimate); this check replaces them. | Warn only, never fail and never stop or interrupt an agent: after 30 minutes with no visible progress, or context growing fast (named threshold) while nothing visible moves, or the same pane content repeated over several observations. Thresholds are named, configurable and unit-tested. | Sentinel inspects the pane read-only, then asks master or the person; never restart or interrupt an agent for this alone. | No usable timestamp, usage or observation read failed. |
| Cmd delivery | Every minute, inspect consumer groups, pending-entry age, and group lag. | Warn for a pending entry older than 10 minutes or lag above 100; fail above one hour pending or 1,000 unread entries. Report the group and count, never message bodies. | Inspect the named consumer group with `agentbus`; recover delivery only through its normal owner/consumer. | `XINFO`/`XPENDING` failed or stream disappeared during inspection. |
| Cmd targets | Every five minutes, compare retained addressed entries with registered agents and reserved protocol targets. | Warn on an unregistered target after excluding `people`, service identities, and the internal canary target. Do not escalate or rewrite the entry. | Correct the target through the normal sender; do not replay or delete the entry automatically. | The cmd stream or agents hash could not be read. |
| Project key inventory | Hourly, use bounded `SCAN` over the validated exact project prefix and compare suffixes with the documented key catalogue. | Warn on an unknown suffix. Never delete, repair, or classify another project's key. | Ask the person to identify the owner before changing or deleting the key. | Scan incomplete, Redis unavailable, or project deletion in progress. |
| Quota freshness | Every minute, use the existing quota reader and its freshness rules. | Warn when an expected provider's last usable reading is older than 30 minutes. Quota policy and holds remain owned by dispatch/quota. | Check provider connectivity and account status; leave hold/release decisions to dispatch. | No configured reader, an unsupported provider, or a failed/partial read. |
| Tracked secret-like files | Hourly, run `git ls-files -z` and match path names only (`.env` and `.env.*` except documented examples, private-key extensions, keypair/wallet JSON names). Never open candidate files. | Fail when a tracked path matches. Report path and kind only. | Remove the file from the repository and rotate any credential that may have been committed; never inspect its contents in the report. | Git metadata unavailable or the repository is not readable. |
| Secret scanner | Daily, run `gitleaks` or `betterleaks` only when installed, in redacted/report mode. Parse a report in memory; retain only finding count and path:line, with rule/kind if safe. No report body or secret value is logged, stored, or sent to the bus. | Warn on findings; this check is never a release or service blocker. | Review the redacted path/rule report and rotate a confirmed exposed credential; scanner absence is not a blocker. | Scanner absent or failed. Absence is not `ok` and does not fail the gate. |
| Redis exposure and password | Every five minutes, inspect the local Docker port mapping and the effective credential source without displaying it. | Fail if the published Redis port is not loopback-only, or if the resolved password is the historical default. | Restrict the port to loopback; run `./setup.sh rotate-redis-password` (v0.19.1). | External broker, Docker unavailable, or credential source cannot be resolved. |
| Service env permissions | Every five minutes, stat the installed project's `~/.config/agentbus/services/<p>.env`. | Fail if an installed service's environment file is missing or readable by group/others; require `0600` (parent directory `0700`). | Recreate the env file with `agentbus up` and verify mode `0600`; do not print its contents. | Services are not installed, or the path cannot be inspected. |
| Installed versions | Daily, read the installed `agentbus` version and the existing update-check cache; verify installed role-skill links against this checkout; check herdr against one documented minimum-known-good version and check the herdr-plus plugin/version. | Warn when agentbus is behind the cached latest release, a skill link is missing/mismatched, herdr is below minimum, or herdr-plus is absent/too old. Do not flag a newer herdr release. | Run the documented `agentbus update` or skill installer after human review; upgrade herdr only to a known-good release. | Cache stale, executable missing/unreadable, or plugin/version command failed. No network request is made by this check. |
| herdr command probe | Every five minutes, run the same bounded read-only herdr probes the dispatcher needs, without sending text or changing focus. | Fail on a conclusive incompatible/error response; one transient failure is warn and repeated failures become fail. | Check herdr status and restart herdr only through its documented service command after confirming the affected workspace. | herdr is not available in this service environment or its response is unparseable. |
| Project-defined checks | Interval and timeout from `.agentbus/checks.toml`; run each entry as a separate argv/process group with no shell, bounded output, and a small allow-listed environment without Redis or provider credentials. | Exit codes map to `ok`, `warn`, or `fail` in the file. Store the exit code and only a sanitized first-line summary; never retain raw output. | Follow the reviewed remediation text declared alongside the check; otherwise ask the person. | File malformed, timeout, or runner unavailable. |
| End-to-end canary | Every five minutes, append one internal `wake=no` probe through cmd delivery to a reserved health consumer group; measure append→read→ack with Redis time. | Warn over 2 seconds; fail over 10 seconds. It never wakes a pane. | Inspect Redis and cmd delivery health; do not replay or acknowledge another consumer's entry. | Any step cannot be proved, including a stale pending probe. |

Questions waiting for the person are not a health check: busmon's v0.18 attention (answered, reminders,
recap) already owns them, and counting them here would keep the health pill red for a normal wait.

`fail` is reserved for a concrete, reproducible fault; uncertain evidence is `unknown`, not `fail`.
No check changes service state, agent state, quota marks, gates, board ownership, files, or repo
contents. Repo scans are read-only. Health details are short, sanitized, and secret-free; counts,
path:line, and kinds are sufficient.

Project-defined commands are executable code from the project checkout and run as the same user as
the services. The first version therefore requires a human trust step for a checks-file content
digest before the service executes it; a changed digest disables execution until reviewed again.
The file uses argv arrays (no shell), per-entry timeout, process-group cancellation, and a minimal
environment. This is defense in depth, not a sandbox.

## Data model

`{p}:health` is a hash keyed by stable check id. Each JSON value has:

```json
{
  "id": "dispatch-service",
  "status": "warn",
  "detail": "restart budget low",
  "observed_at": 1800000000000,
  "since": 1799990000000,
  "fingerprint": "opaque-stable-digest"
}
```

Remediation is static catalogue metadata, not a dispatcher-generated instruction. `agentbus health`
and the busmon detail overlay show the same one-line remediation for each warn/fail result; a
project-defined check supplies its remediation in `.agentbus/checks.toml`. It is displayed as
guidance only and never executed by the dispatcher.

`observed_at` and transition timestamps come from Redis `TIME` inside the write script. `since`
starts on the first observation of a status and remains while that status is unchanged; it resets
only when status changes. The fingerprint is a stable digest of normalized, non-secret evidence;
it lets operators distinguish causes without retaining raw input. On the same status, a changed
fingerprint updates the current record but does not create a status-transition event or a new
sentinel wake.

`{p}:health:log` is a capped stream of transitions only: check id, previous status, new status,
fingerprint, and Redis timestamp. It contains no raw detail. `{p}:health:notes` is a separate hash
keyed by check id; a note has author, Redis timestamp, and bounded sanitized text. This separation
ensures the dispatcher can replace measured records without erasing the sentinel's latest note,
and a note can never change the measured status. Notes are not transitions and do not append to
`health:log`.

Each check id has one current record. Transition stream retention is bounded (initial cap 1,000).
Readers expose the oldest retained timestamp and label intervals that start before it. Missing or
stale health is unknown. No credential, command output, prompt, transcript content, or environment
value is stored in health, transition, or note data.

## Sentinel duties and rights

Sentinel is a normal one-shot Haiku agent, not a daemon and never armed between wakes. The
dispatcher wakes it with an ordinary fenced directive when a check enters `fail`, once per fail
episode, and once a day for a round. A `warn` or `unknown` alone does not wake it immediately; the
daily round can review those. A failed check that remains failed does not produce a wake storm.

At each wake it reads `agentbus health` (list, detail, and retained log), checks the implicated
service log and pane state read-only, and compares the check's current fingerprint with its
transition. It writes its conclusion using `agentbus health note <id> <text>`. A note is
annotation only; sentinel cannot set `ok`, clear a failure, or write a measured result.

Safe repairs are an explicit allow-list:

- restart a failed dispatch or missions service once with `agentbus service restart`, only when the
  service's restart budget has room and the current check identifies it as inactive/failed;
- refresh the local models table with `agentbus models --refresh` when its source check failed and
  the reader is installed.

After a repair, sentinel waits for the next measured result; it never writes the success itself. It
does not repeatedly restart a service, run a project-defined command, change Redis credentials,
edit role/configuration files, close or type into panes, or change project code. Anything outside
the allow-list is escalated as a directive to master or an ask to the person. The daily round keeps
the useful shared-memory/request review and master context nudge; cron wake, budget collection,
dispatcher status polling, index warm-up, and peer relay are removed. Quota collection remains in
the dispatch service. Per-agent usage refresh also moves there: current code calls
`collectAgentUsage` only from `agentbus refresh`, invoked by the sentinel, and writes `{p}:usage`
with an unfenced `SetUsage`; the service must publish each snapshot through a dispatch-lease-fenced
writer. Models refresh is once daily per host under a local lock, so multiple project services do
not refresh the machine-wide catalogue concurrently. Usage refresh is at most every 15 minutes per
project and uses a fenced writer.

## Wake path

The dispatcher computes a check result and applies it with one fenced script. The script checks the
current dispatch token, reads Redis time, updates the hash, and compares old/new status. On a
transition it appends one `health:log` event. On a transition into `fail` it also appends one
addressed `directive` to the registered sentinel's cmd queue, with the check id and fingerprint,
not the raw detail. If sentinel is not registered, the dispatcher directs the escalation target
instead and records that outcome; it never starts another service or agent process itself.

The ordinary cmd entry wakes an unarmed sentinel through the existing dispatch reconciliation and
herdr wake-line path. Sentinel drains via `agentbus subscribe --drain sentinel`. Its reply, note,
and escalation stay on the existing bus. A daily directive is sent through a separate fenced
once-per-day mark, using Redis time; it is not a polling loop. Lost lease, failed atomic script,
or deleted project means no wake is published. A later healthy dispatcher retries the due check.

The canary uses one reserved recipient and consumer group, never an agent's group. Its message is
`wake=no`, carries a reserved internal marker/type, and is excluded exactly by that marker from
unknown-target checks and busmon Activity. The health consumer reads and acknowledges it through
the normal delivery path. It cannot change agent PELs or wake a pane. Canary write, delivery, and
ack are individually timed; a pending probe is recovered or marked unknown on the next pass.

## Redis password

At first install, generate a cryptographically random password from the OS random source and store
it in `${XDG_CONFIG_HOME:-~/.config}/agentbus/redis.env`, in an atomically replaced `0600` file
inside a `0700` directory. Do not print the value, place it in a unit/plist/command line, or send it
over the bus. Compose receives it through an explicit env-file mechanism; `docker-compose.yml` has
no built-in password default. The Redis healthcheck uses a protected environment variable, not a
literal argument in command output.

Every client resolves credentials in this order:

1. `REDIS_URL` (including its username/password) wins as a complete connection definition;
2. `REDIS_PASSWORD`;
3. `redis.env` from the XDG config directory;
4. the historical default only during the documented migration window, with a warning.

An explicitly supplied but invalid `REDIS_URL` fails; it does not fall through. Connection errors,
diagnostics, `--version`, status output, and health details never print a URL or password. Service
env files remain `0600`; `agentbus up` resolves the password and writes it into the per-project
service env so restarts use the same credential. Remote clients behind the SSH tunnel use their
own local `redis.env` or explicit `REDIS_URL` and keep the tunnel topology unchanged.

Keep the migration simple (review decision). Every `agentbus` call is a fresh process that resolves
the credential at start, and agents only ever reach Redis through such calls, so once the new
binaries read `redis.env`, a running agent picks up the new password on its next command. No
dual-password ACL machinery is needed. The migration is one explicit, operator-started command
(`./setup.sh rotate-redis-password`, also run by `install` on the historical default only after
a `[y/N]`):

1. Generate and store the new password atomically in `redis.env` (`0600`, directory `0700`).
2. Recreate the Redis container with it (data volume and AOF unchanged). Armed `subscribe` calls
   and the services drop for a few seconds: the services restart through their manager, and a
   `subscribe` that fails exits with its normal error and is re-armed by its agent, now with the
   new credential.
3. Restart the project services and verify with non-disclosing probes (`agentbus dispatch
   --status` for every installed project).
4. Print one line for the remote SSH-tunnel client: copy `redis.env` there (never the value on
   screen). Until it is copied that client fails to authenticate, which the person sees at once.

If the rotation fails after step 1, the previous password file is kept beside the new one and the
command prints how to roll back. Any process that still carries an explicit `REDIS_PASSWORD` in its
environment (an old unit, a shell profile) is reported by the health check, never edited.

If the old credential is not the known historical default, setup must not rotate it silently; it
reports the situation and leaves it to the person. Tests use their own isolated Redis and do not
inherit these production settings.

## busmon

- The header reads `{p}:health` and shows `✓` only when all applicable current checks are `ok`.
  Any warn/fail/unknown or stale result shows `! N`, where N counts non-ok checks; unknown is never
  folded into healthy.
- F5 opens a health overlay (F1 help, F2 requests, F3 services and F4 asks are already used). Rows
  are sorted by status severity then id and show status, age/since, safe detail, the catalogue's
  one-line remediation, and the sentinel's latest note. Enter opens the existing related
  service/agent detail when there is a stable link.
  No row action repairs or writes to the bus.
- The `R` recap adds health-log transitions whose Redis time is after the previous local visit.
  It shows the oldest retained health-log time and labels a truncated interval; an unavailable
  read is rendered unavailable, never as no failures.
- Health rows use busmon's `Line`/`Seg` and theme roles; colored overlay text uses `tag()`/`tg()`.
  Use only the closed glyph set in `theme.go`; preserve all-width/palette alignment tests. Draw
  performs no I/O and never calls `SetFocus`.

## Migration

This release updates `setup.sh`, Compose, `bus.Connect`, `service.CollectEnv`, the project service
environment writer, README/user docs, and busmon together. Install is additive: it creates the
password file only if absent, preserves an existing private file, and never overwrites an
operator-supplied `REDIS_PASSWORD` or `REDIS_URL`. Dry-run reports paths and actions but no
credential values. Existing service units continue to reference the same env-file path; after the
file changes, the human explicitly restarts each service. Agent panes are not silently cleared or
closed. Reusing the known historical credential for one compatibility window is a deliberate
migration state and is surfaced as `fail` by the password health check.

Health migration is also additive. A missing `{p}:health` hash reads as unknown until its first
checks complete; no empty hash or old `{p}:observed` entry is treated as a healthy result. Projects
with dispatch but no registered sentinel still get health data and a master/person escalation.

## Edge cases

- Dispatcher lease lost while a child check runs: cancel its process group; the fenced script
  rejects any late write. No fail wake or transition is claimed unless the atomic write succeeded.
- Dispatcher restarts while a check is `fail`: its stored status preserves the episode; it does
  not wake sentinel again. `fail → warn → fail` is a new episode.
- Source read fails after `ok`: record `unknown`, keep the last note, and do not claim healthy.
- Check runs longer than its interval: do not overlap instances of that check; one timed-out result
  becomes unknown and the next scheduled run can retry.
- Health hash is missing, malformed or stale: busmon shows unknown and allows all other panels and
  the recap to work.
- A cap trims older transition events: show the history floor. Never infer that no event occurred
  before it.
- Project deletion races a health write: fenced/lifecycle checks may leave an orphan key, but health
  keys cannot discover a project or cause lease reacquisition. `DeleteProject` removes known keys.
- Secret scanners may find credentials in history. Redact before parsing; only count, rule/kind and
  path:line survive. If redaction cannot be guaranteed, report scanner failure as unknown and keep
  no output.
- A user-defined check changes after trust: stop executing it and mark it unknown until its digest
  is explicitly reviewed again.
- Redis can be reachable through a remote SSH tunnel; local bind inspection is unknown in that
  topology rather than a false failure. The local server must still be loopback-only.
- Health checks and sentinel actions run as the same user as the agents. A compromised agent can
  alter local files, spoof process facts, or read the secret; this release detects and alerts but
  does not contain that user.

## Tests

- Pure evaluators with fixtures for each status, boundary, threshold, stale input, malformed data,
  and read error. Unknown never passes as ok; stable inputs produce stable fingerprints.
- Fenced Redis tests: current-token success; stale token rejection; health/status/log/wake atomicity;
  duplicate same-status writes; one wake per fail episode; daily once mark; Redis `TIME` stamps;
  log contains transitions only; notes never overwrite status.
- Project lifecycle tests: `{p}:health` and `{p}:health:log` do not create a listed project or make
  `HasKeysBesidesDispatch` true; delete/write races do not resurrect it.
- Canary tests: real cmd delivery write/read/ack measurement on isolated Redis, `wake=no`, reserved
  consumer only, no agent pending entries, no dispatcher prompt, and no busmon Activity row.
- Service/command tests with fake systemd, launchd, herdr, docker, `/proc`, and process runners;
  check timeout kills the entire process group and no service is started by busmon.
- Secret tests use path names and synthetic redacted fixtures only; assert candidate files are
  never opened, scanner output never contains a fixture secret, and Redis password values never
  appear in logs/errors. Installer tests stub Docker and systemd and use temporary XDG homes.
- Password resolution tests cover `REDIS_URL > REDIS_PASSWORD > file > migration fallback`,
  malformed URL, file modes, atomic generation, existing-file preservation, Compose env-file use,
  the rotate command with fake docker (never the live container) and rollback. Test Redis stays explicitly isolated and
  unchanged.
- busmon tests cover header states, unknown/stale rendering, F5 overlay, notes, recap transitions
  and history floor, plus alignment/glyph tests across supported widths and palettes.
- Full validation uses `go build ./...`, `go vet ./...`, tests with
  `AGENTBUS_TEST_REDIS_URL` set only to a disposable broker, and `bash tests/run.sh` with fake
  Docker/systemd. Never use production Redis or the user's systemd in tests.

## Release split (review decision, keep each release small)

1. **v0.19.0 — health core**: the scheduler, `{p}:health`/`:health:log`/`:health:notes`, fenced
   writes, `agentbus health` (list, detail, log, note), busmon pill + F5 + recap, the sentinel
   wake/notes and its rewritten skill, the usage/models chores moved into the service, and the
   checks that need no new machinery: services, agent identity, agent launch and identity, model
   drift, runaway, cmd delivery, cmd targets, quota freshness, tracked secret-like files, Redis
   exposure and password (detects the default), service env permissions, installed versions,
   herdr command probe.
2. **v0.19.1 — Redis password**: generation at install and `rotate-redis-password` as above.
3. **v0.19.2 — extended checks**: secret scanner, end-to-end canary, project key inventory, and
   project-defined checks with their digest trust step (how the person's approval of a digest is
   stored is decided in that release's plan).

## Out of scope

- A sentinel daemon, crontab, polling loop, second pane writer, or health-driven autonomous code
  changes.
- Treating health as an authorization boundary, secret vault, or substitute for OS isolation.
- Automatically rotating a non-default operator password (rotation is always started by the person)

- Project-specific health policy beyond the optional reviewed checks file; no LLM-defined checks.
- Healing arbitrary agent, model, Redis, repo, or network failures; unlisted repairs are escalated.
- Changing quota/hold policy, delivery semantics, project deletion semantics, service-manager
  restart policy, or remote SSH tunnel topology.
- A separate health database, global dashboard, phone notification, or unbounded audit stream.
