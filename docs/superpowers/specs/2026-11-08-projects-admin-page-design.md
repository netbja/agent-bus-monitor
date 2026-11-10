# busmon's Projects page: list, sleep, stop, back up and delete projects (projects-admin)

Status: steps 1–4 merged (PRs 373, 383). The person answered part 10 on 2026-11-09 (task
`projects-progress-answers`); the answers are recorded there, and three changes follow from them:
the memory size in the detail (§3.3), the hosts named in the delete panel (§5.2), and the two
options that also delete files (§5.5). They are steps 6–8 (§11). The person answered Q6 and Q7 on
2026-11-10 (task `projects-admin-answers-3`): `roles.toml` is never removed, and the archive made
just before a delete is kept even with `delete_backups` on (§5.5, part 10). This spec names functions and
seams, not line numbers.

## 1. The gap

Everything the person needs to manage projects exists, but on the command line and in pieces:

| Need | Today | Where |
|---|---|---|
| Which projects exist | `busmon --list` (broker keys); `busmon --pick` (templates on disk + broker present/absent) | `bus.Projects`, `projlist` |
| A project's footprint | the F3 Redis card's page: keys and XLEN per project | `bus.RedisDetail` → `ProjectRedis` |
| Sleep / wake | `agentbus suspend` / `resume`; `Z` in busmon, on the **open** project only | `sleep.go` `startSleep` |
| Stop services | `agentbus down <p>` ("stop a project's services; deletes nothing") | cmd/agentbus |
| Back up | `agentbus backup --project <p>`; the C page's backup group runs it | `startRun` |
| Delete | `busmon --delete <p>`: services removed on this machine, then `DEL` of every `{p}:*` key, after a `[y/N]` | `main.go` `deleteProject`, `bus.DeleteProject` |
| herdr workspace | a workspace whose label is the project (`findWorkspace`, `herdr workspace list`) | cmd/agentbus |

There is no screen that shows them side by side. busmon acts only on the project it has open,
and deleting a project means leaving busmon and typing a destructive command. The person wants
one page that lists every project and offers the actions.

## 2. The change in one paragraph

A new full page, **Projects**, opened with `M` (manage, §7) from the shell. It lists every project
this host knows of: on the broker, from a template, or both. Each row shows its state, agents,
footprint, services, herdr workspace and last activity. Five actions work on the selected row:
open its detail, put it to sleep or wake it (`Z`), stop its services (`x`), back it up (`b`), and
delete it (`d`). Delete is the only destructive action. It offers a backup first, refuses while
the project is live, asks the person to type the project's name, and asks a second time for the
project busmon has open. All writes run through the existing CLI paths (`agentbus …` as a child,
or the Go functions `busmon --delete` already uses), so the page adds no new write path to Redis.

## 3. What the page lists

### 3.1 The rows

There is one row per name in the union of:

- `bus.Projects` (the broker's SCAN, `ValidName` already applied);
- `projlist.Templates` (the `*.toml` templates, also `ValidName`).

A name that fails `ValidName` is never shown, never acted on, and never put into a glob
(CLAUDE.md: "a project name is validated before any `SCAN MATCH` glob is built from it").

### 3.2 The columns (one reader, `adminRows`)

| Column | Source | Shown as |
|---|---|---|
| project | the name | bold if it is busmon's open project, with `◂ open` |
| state | `{p}:pause` (`PauseRecord`, `Suspended()`), the services, the agents | `live` · `paused` · `suspended` · `stopped` (no service active, no live agent) · `template` (no broker key) |
| agents | the `{p}:agents` hash and the bus's own liveness rule (the one the Agents panel uses) | `live/total`, e.g. `3/7` |
| keys | `ProjectRedis.Keys` (one `SCAN {p}:*` per project, as the Redis page does) | a count |
| size | not shown by default (§10 Q2) | — |
| services | `service.Manager.List()` + `State(project, kind)` **on this machine** | `dispatch ● missions ○`; `–` when none is installed; `?` when no manager |
| herdr | `herdr workspace list`, label == project, **on this machine** | `open (N panes)` or `–`; `?` when herdr does not answer |
| last | `ProjectSummary.LastTS` (newest of status/report/notify/cmd) | `lastSeen` (`3m`, `2d`) |
| template | `projlist` | `✓` or blank |

The order is the open project first, then the newest activity (`sortProjects`), then template-only
projects by name.

### 3.3 Reading it

- **Read once when the page opens, after each action, and on `R`.** No polling. A SCAN of the
  whole keyspace every second would cost more than the page is worth, and nothing on it needs to
  be live. The header shows `read 12s ago`.
- **Off the update path.** The rows are read in a `tea.Cmd`, as `openSettings` and the Redis page
  do, with a deadline of 5 s. Each part can fail without the others: the broker unreachable makes
  every broker column `?` and keeps the template rows; herdr or the service manager missing makes
  their column `?`, never `–`. An unknown is never shown as an empty value (the Redis page's rule).
- **Memory, in the detail only** (the person, Q2). On `enter`, the detail adds `memory about 1 MB
  (1204 keys)`. The read runs in the detail's own `tea.Cmd`, bounded on the client **and on the
  broker** (reviewer-codex):
  - **Each key is sampled, never walked.** `MEMORY USAGE <key>` with Redis's default sample of 5
    nested values, never `SAMPLES 0`, which walks every element of a large stream or hash (O(N))
    and keeps working on the broker after busmon has given up. Each command's server work is
    therefore bounded, and the total is an **estimate**, so it reads `about`.
  - **The cap holds while scanning.** The `{p}:*` SCAN stops at 50 000 keys; it never collects
    every key before applying the cap.
  - **Small batches, one deadline.** Pipelines of 100 commands; 2 s for the whole read, scan
    included.
  - **What a cut read shows:** cut while reading the keys, `about 800 KB for 900 of 1204 keys`;
    cut while scanning (the total is then unknown), `about 800 KB for the first 900 keys; the scan
    stopped (2 s)` or `(50 000 keys)`. A broker that refuses `MEMORY` (a restricted ACL) shows
    `memory ? (the broker refuses MEMORY)`; another error, `? (<why>)`.

  The list never shows memory: one call per key is too dear for every row.
- **"This machine".** Services and herdr are local. When busmon runs against a remote broker
  (`--host` through a tunnel), the header says `services and herdr: this machine (<hostname>)`,
  and a project with no local service shows `–`, which means "not here", not "none".

## 4. Actions

All actions work on the selected row. The page never acts on a name the person typed, except to
compare it with the selected row's name (§5.2).

| Key | Action | How | Allowed when |
|---|---|---|---|
| `enter` | detail: every column, the five streams' XLEN, the memory the project's keys take (§3.3), the pause or suspend record (by, reason, since), the template path, the workspace id | read only | always |
| `Z` | sleep or wake: `agentbus --project <p> suspend --yes`, or `resume`, after the same `y` confirmation as the shell's `Z` | `startRun`, one run at a time, log `suspend.log` | the project is on the broker |
| `x` | stop its services: `agentbus down <p>`, after a `y` | `startRun` | it has a local service that is active |
| `b` | back it up: `agentbus backup --project <p>`; the archive path, from the last line, becomes the notice | `startRun` | always (a template-only project backs up its template and roles) |
| `d` `del` | delete (§5) | §5.3 | §5.1 |
| `R` | read again | §3.3 | always |
| `esc` | back to where `M` was pressed | the page stack | always |

- **One run at a time, shared with the shell.** `startRun` already refuses a second run whatever
  it is (`Z`, the C page's backup), and the page uses it unchanged. While a run goes, its last log
  line is the row's notice, and the row shows `⋯`.
- **No new agentbus verb.** `suspend`, `resume`, `down` and `backup` exist. The agent denies in
  `roles.toml` (`agentbus suspend*`) are about agents. busmon is the person's process and runs
  them as the shell's `Z` already does.

## 5. Delete

### 5.1 Refused while the project is live, on any host

A live project recreates its keys: the next `agentbus status` from any of its agents writes
`{p}:status` again, and a dispatcher or conductor takes its lease again (`{p}:dispatch`,
`{p}:conductor`). A delete then leaves a half-project that the list shows again a second later.
So `d` is **refused**, with the reason and the way out on the notice line, while any of these
holds. The broker-side checks see every host, because they read the broker and not this
machine's service manager (reviewer-codex, foureyes):

| Evidence | Read from | Notice |
|---|---|---|
| a dispatcher holds the lease, on whatever host | `Bus.DispatchAlive` (`EXISTS {p}:dispatch`, a TTL lease) | `a dispatcher runs for demo (host <h>, from {p}:services): agentbus down demo there, or Z` |
| a conductor holds its lease | `Bus.ConductorAlive` | the same, naming the conductor |
| a service publishes its heartbeat | `Bus.ServiceStates`: a record whose `at` (Redis TIME) is younger than 30 s. A service writes it every 5 s (`servicecmd.go`, `Heartbeat`) in any live state (`starting`, `ready`, `busy`, …); `host` is named | `<kind> runs on <host>` |
| a local service is active | `service.Manager.State` | `stop its services first: x` |
| an agent is live | the bus's liveness rule, which counts agents on any host | `N agents are live: Z puts the project to sleep` |
| its herdr workspace is open on this machine | `herdr workspace list` | `close its herdr workspace first (N panes)` |
| a run (`startRun`) is going | the model | the existing "already running" notice |

**It is checked twice** (foureyes): once when `d` is pressed, and **again inside the delete's
`tea.Cmd`, immediately before `deleteProjectNow`**. Minutes can pass between the two (a backup,
the typed name). If anything became live in between, nothing is deleted, the panel closes, and the
notice is the one in the table, prefixed `became live:`. Any check that cannot be read (broker
error, herdr not answering) refuses too: `cannot check that demo is quiet (<err>)`. An unknown
never counts as quiet.

A suspended project passes these checks: its services are stopped, its leases released, and its
agents gone. That is the expected path: `Z`, then `d`, and it works the same from a remote broker
(§10 Q5). Whether the person wants a forced delete that skips the checks is §10 Q1. This spec
has none.

### 5.2 The confirmation: a backup offered, the name typed

`d` opens a confirmation panel over the page. It reuses the shell's dialog frame
(`tea_dialogs.go`), so no new widget is needed:

```
 Delete project 'demo'
 1 204 keys · 7 agents (0 live) · last activity 3d · services: none here · herdr: –
 This is a DEL, not a reset: streams, consumer groups, agents/usage/budget/verdicts,
 pilot and gate leases all go. The template and the worktrees stay.
 units reported on: vdr (dispatch, missions) · remove them there: agentbus --project demo service remove
 other hosts may keep units the broker no longer records: agentbus service list there

 A backup first?  b back up, then continue · n no backup · esc cancel
```

**The hosts where units remain** (the person, Q5). A delete is allowed from a remote broker for a
sleeping or stopped project: `deleteIfQuiet` checks the leases and the services' records of every
host (§5.3). It removes the units of **this machine** only. The panel names the other hosts the
broker **reports**, read from `{p}:services` before the delete: the installed marker's `host` and
the `host` of each service record, minus this machine, each with the kinds it ran.

The broker cannot name **every** host (reviewer-codex). `{p}:services` holds one installed
marker, which the last `agentbus up` overwrites, and one record per service kind, which its last
writer overwrites. A host that installed units earlier and then fell silent leaves no trace. So
the panel says `units reported on: vdr (dispatch, missions)` (or `units reported: this machine
only`) and always adds `other hosts may keep units the broker no longer records: agentbus service
list there`. A registry of installations per host would close that gap; it is a change to `bus/`
and to `agentbus up`, outside this spec. Since the DEL removes `{p}:services`, the line is
read before it and repeated in the done notice (`deleted demo; units reported on vdr: agentbus --project demo service
remove there`).

1. **The backup is offered first, every time.** `b` runs `agentbus backup --project demo`
   (`startRun`). Only when it exits 0 does the panel go on, showing the archive path. A failed
   backup stops here, with the log path. `n` goes on without one, and the panel then says
   `no backup` in its next step.
2. **The name is typed.** One line field: `type demo to delete:`. `enter` is accepted only when
   the field equals the selected row's name, byte for byte. Anything else keeps the panel open
   with `does not match`. The field compares; it never builds a key or a glob.
3. **The open project asks twice.** When the row is busmon's own project, a second line follows
   the typed name: `busmon has demo open: after the delete busmon exits. y deletes · esc
   cancels`. After the delete, busmon quits and prints `demo deleted (N keys); busmon --pick to
   open another project`. The picker is its own program (`pickMain`), and the shell cannot turn
   into it in place. That is the "project you run in" case. Nothing else is checked here
   (foureyes): the open project is the one busmon runs in.
4. `esc` cancels at every step, and cancelling writes nothing.

### 5.3 The delete itself: the one path busmon --delete uses

The core of `main.go` `deleteProject` (services removed on this machine with `localServices`,
then `bus.DeleteProject`) becomes a function with no terminal I/O: `deleteProjectNow(ctx,
client, project, removeServices) (keys int64, services []string, err error)`. Both paths call it:
`busmon --delete` after `confirmDelete`, and the page after §5.2. The order stays as it is
(services first, so nothing restarts the project). So do the guards: `bus.ProjectKeys` refuses an
invalid name, and the `{p}:*` glob with its trailing colon cannot match a sibling (`demo` never
takes `demo-2`). The page runs it in a `tea.Cmd`, with a deadline of 30 s.

**The final check and the DEL are one atomic step** (reviewer-codex). Today `bus.DeleteProject`
scans and then deletes, and a check run just before it leaves a window. It gains a Lua script,
`deleteIfQuiet`. Its KEYS are the scanned keys plus `{p}:dispatch`, `{p}:conductor` and
`{p}:services`. Inside the script, Redis runs nothing else, and it:

1. refuses (returns the reason, deletes nothing) if `{p}:dispatch` or `{p}:conductor` exists, or
   if a `{p}:services` record's `at` (decoded with `cjson`) is younger than 30 s by Redis `TIME`;
2. otherwise `DEL`s every key it was given, and returns the count.

Every Redis script is atomic, so no lease can be taken between the check and the DEL. The
local checks (this machine's services, herdr, live agents) stay in Go just before it, as §5.1
says.

**Then a second scan, and the truth on the notice.** A key written between the scan and the script
(an agent's `status`, say) is not in the list and survives. A writer that starts after the script
re-creates its key. No check can stop that without a fence that every writer honours, and adding
one is out of scope. So `deleteProjectNow` scans `{p}:*` again after the script:

- nothing left: `deleted 1 204 keys of demo; removed dispatch and missions services here`;
- keys left or back: `deleted 1 204 keys of demo, but 2 keys came back (demo:status,
  demo:dispatch): something of demo still runs (on <host> if a record names it); stop it, then d
  again`. The row stays, and the delete is never reported clean.

The list is then read again (the row disappears, or shows `template` when a template remains).
`busmon --delete` prints the same two outcomes, and exits with code 1 in the second case. The
residual race is stated in operator-notes.

The template, the worktrees and the backups are **not** touched by default. Two options, off by
default, delete them too (§5.5).

### 5.5 Files too: two options, off by default (the person, Q3)

Two settings, registered in `config/keys.go`, set in `config.toml` and on busmon's settings screen
(`C`), both **off** by default:

- `projects.delete_files`: the project's **template** and its **worktrees**;
- `projects.delete_backups`: its **backup archives**. They are separate because the backups are
  the only way to restore.

They act only **after** a clean broker delete (§5.3: nothing came back). If keys came back, no
file is touched, and the notice says so.

**What `delete_files` removes, and what it never removes:**

- **The template**, `<projects dir>/<p>.toml`. If it is a link, the link is removed, never its
  target.
- **`roles.toml` is never removed.** It is one machine-wide file (`ROLES_TOML`, or the agent-bus
  checkout's), shared by every project. Deleting it would break the others. The person confirmed
  it (part 10, Q6): `delete_files` covers the template and the worktrees only.
- **The worktrees.** They are read as text, never by running git (git's config can make git run a
  program: the compose-at-file rule). **That text is writable by any agent**, so what gets removed
  is never decided by it alone (foureyes). For each entry `W/.git/worktrees/<name>`, where `W` is
  the template's `working_dir`:
  1. its `gitdir` file names the worktree's `.git` **file**; the candidate is that file's parent
     directory `D`;
  2. `D` is resolved (`EvalSymlinks`) and must lie **strictly below** the resolved `W`, with no
     link on any component (its resolved path is its own path), and must be neither `W` nor
     contain `W/.git`;
  3. **git's own two-way link must hold:** `D/.git` is a regular **file** (not a directory, not a
     link) whose `gitdir:` line resolves to exactly `W/.git/worktrees/<name>`, the entry that
     named it.

  Only then is `D` removed (`os.RemoveAll`, which removes a link and never follows it), and then
  its `W/.git/worktrees/<name>` entry. Anything else is listed as `not a worktree: kept`, and so is
  a worktree outside `W`. A forged `gitdir` naming `W/cmd/.git`, for example, fails step 3: `W/cmd`
  has no `.git` file pointing back, so nothing there is touched. **The checkout itself (`W`) is
  never removed.** Branches and commits stay in the repository; only what was not committed is
  lost. The panel lists each worktree with its branch (read from its `HEAD`) and says
  `uncommitted changes in them are lost`.

**What `delete_backups` removes:** the archives in the backup folder (`config.BackupDir`) whose
`coverage.json`, their first member, lists **this project alone**. It is read with a bounded
reader, and never the whole archive. An archive that also holds other projects is **kept**, and
the panel says how many (`3 archives also hold other projects: kept`). An archive that cannot be
read is kept and named.

**The panel**, with an option on, adds what it will remove:

```
 also deleted (settings): template ~/.config/agentbus/projects/demo.toml
   worktrees: .worktrees/fix-a (fix/a), .worktrees/spec-b (spec/b): uncommitted changes in them are lost
   backups: 4 archives of demo alone; 3 archives also hold other projects: kept
```

**The backup offer, and the one archive kept** (the person, Q7). The backup that §5.2 offers first
is offered with `delete_backups` on too, and the archive it makes is **never** removed by this
delete: it is the one exception. busmon knows it by the path `b`'s run printed on its
`backup: <path>` line (an absolute path to a `.tar.zst`), read from **this run's** part of the log,
after the runner's own `--- ` header. It is never the log's last line: `agentbus backup` prints
that line first, then per-project lines, notes, `backup.after_command`'s outcome and the
retention's `removed N` (foureyes). That path is compared with each candidate as a file
(`os.SameFile` on the resolved path), never by name, date or size. The panel shows it on its own
line, and its `backup:` line shows that path, not the run's last line:

```
   backups: 4 archives of demo alone; 3 archives also hold other projects: kept
   kept: ~/.local/share/agentbus/backups/agentbus-backup-host-20261110T0915.tar.zst (made now)
```

**Fail closed** (foureyes): when `b` succeeded but its log names no such path, or the path does
not resolve to an existing regular file, `delete_backups` removes **no** archive in this delete,
and the panel says `backups: kept, the new archive's path is not in the log`. It never falls back
to "no exception", which would erase the fresh archive. `delete_files` is unaffected.

With `n` (no backup), nothing is exempt, and every archive of the project alone goes, as listed.
An archive made earlier, even minutes before, is not the exception: only the one this panel's `b`
made. `busmon --delete` offers no backup, so it has no exception either; it lists what it removes
before it removes it.

`busmon --delete` honours the same two settings and prints what it removed.

### 5.4 Agents cannot reach this

busmon is the person's interface, and an agent never drives it. But `busmon --delete <p> --yes`
is one Bash line for any agent. Two layers stop it:

1. **busmon refuses an agent caller itself**, whatever the spelling. This is defence in depth,
   not a boundary (foureyes). `env -u AGENT_BUS_AGENT setsid busmon -d demo --yes` (a double
   fork, reparented) escapes both the environment check and the ancestor walk. That escape was
   already noted on notify-guided-setup.
   `--delete` and `--reset` run the check `agentbus suspend` and `resume` already run
   (`refuseAgentCaller`): `AGENT_BUS_AGENT` set, or a registered agent among the process's
   ancestors. On a hit they exit with `deleting a project is the person's act, not <agent>'s
   (run busmon from your own shell)`. The check is moved to a shared package, so busmon and
   agentbus use the same one. A text deny can never be complete (foureyes): Go's `flag` package
   takes one or two dashes, `name=value`, and flags in any order.
2. **Shared denies in `roles.toml`, as an echo.** They name the spellings busmon's parser accepts
   (`cmd/busmon/flags.go`: every long and short flag registered with `flag`), like deliver's
   denies. A wildcard before the flag catches it after any other flag (`-p x`, `--host h`, `-y`,
   `--yes`):
   - `Bash(busmon *-delete*)` covers `--delete`, `-delete` and `--delete=`;
   - `Bash(busmon *-d *)`, `Bash(busmon *-d=*)`, `Bash(busmon *--d *)` and `Bash(busmon *--d=*)`;
   - the same for the bare start: `Bash(busmon -d *)`, `Bash(busmon --d *)`;
   - the same set for `reset` and `-r`.

   busmon takes no message text, so a wildcard cannot deny a word inside a message, which was the
   worry with `agentbus * deliver *`. Residuals, named in operator-notes: a full path
   (`~/go/bin/busmon`), `go run ./cmd/busmon`, an env prefix (`X=1 busmon`), a shell alias.
   Layer 1 catches the ordinary ones. On the page, the real guard is the person's typed
   confirmation (§5.2) and the live checks (§5.1, §5.3).

The page itself adds no command an agent could call.

## 6. Tiers (wide ≥ 120, medium 80–119, narrow < 80)

| Tier | Row |
|---|---|
| wide | `project · state · agents · keys · services · herdr · last · template`, one line each |
| medium | `project · state · agents · services · last`; keys and herdr move to the detail |
| narrow | two lines per row: `project  state` / `3/7 agents · 2d · ● dispatch`; the key hints stay in the footer |

The confirmation panel (§5.2) wraps on every tier, and at 60 columns it keeps the typed field on
its own line. Selection and scrolling use `listState`, which the picker already uses
(`pickFill`).

## 7. Keys (keys.go, the one table)

- **To open it,** one shell row: `{Keys: "M", Help: "projects: every project of this host, its
  state, and sleep, stop, back up or delete one", Act: ActProjects, match: runeIs('M')}`. `M` is
  free (the capitals in use are A C D G H I P R T U Y Z). Whether it should be `M` is §10 Q4.
- **On the page,** a new mode, `ModeProjects`, with its own table, `projectsBindings`. The
  handler, the footer and the help all read that table, as `settingsBindings` does:
  `↑↓ j k` select, `pgup pgdn home end`, `enter` detail, `Z` sleep/wake, `x` stop, `b` back up,
  `d del` delete, `R` read again, `esc` back, `F1 ?` help, `q` quit, `^C`.
- **The confirmation** has its own small table (`b`, `n`, `y`, `esc`, and the text field's
  keys), so a `d` typed in the name field is a letter, never a second delete.
- `keys_test.go`'s existing checks (no key advertised twice, every action reachable, footer ==
  table) cover both new tables.

## 8. How it composes with what is merged

- **The shell, For you and triage.** The page is a pushed page (`m.push(page{kind:
  ovProjects})`), like Tokens, Settings and History. `esc` returns to the panel and selection
  `M` was pressed on. For you is unchanged: a run started on the page is not an inbox item, and
  it shows in the notice line, as the shell's `Z` does. A suspend run on another project never
  touches the open project's For you.
- **The open project's `Z`.** It is the same run and the same lock. Pressing `Z` on the open
  project's row is the shell's `Z`.
- **Settings (C).** The C page's backup group (destination, `after_command`) configures what `b`
  runs. The page reads nothing from it: `agentbus backup` does. The two pages share `startRun`,
  so a backup started on one shows as running on the other.
- **The Redis page (F3 → Redis).** Its per-project rows become the page's keys and XLEN source
  through the same `bus.RedisDetail` reader. Nothing is duplicated, and the Redis page stays read
  only.
- **The picker (`--pick`).** It stays the start screen and never deletes (main.go: "a picker never
  purges or deletes"). The Projects page is reached only from inside a project. That is why the
  open-project case (§5.2.3) ends by quitting with a pointer to `--pick`.

## 9. Tests

The seam is `Model` with fake readers, as the Settings and Redis page tests use. Goldens are at
the existing sizes (`goldenSizes`: 60×20, 80×24, 140×40, 140×60):

- **Goldens:** the list at each tier with a live, a suspended, a template-only and the open
  project; the detail; the confirmation at both the backup step and the typed-name step; the
  refusal notice; the `?` columns with herdr and the service manager missing.
- **Delete guards:** each of the §5.1 refusals, including a dispatcher lease and a fresh
  fresh service heartbeat from **another host** (reviewer-codex); a check that cannot be read
  refuses; a project that **becomes live between `d` and the delete** (the fake reader turns a
  lease on after the typed name) deletes nothing and shows `became live:` (foureyes); a typed name that differs by one byte (`demo `,
  `Demo`, `demo-2`) never deletes; `esc` at each step writes nothing; the open project needs the
  second `y`; after it, the model quits with the message (`tea.Quit`), and nothing else runs.
- **The atomic step** (Redis tests on `AGENTBUS_TEST_REDIS_URL`): the script deletes nothing
  and returns the reason when the dispatch lease, the conductor lease or a fresh service record
  exists; a lease set between the Go scan and the script is refused by the script; a key written
  after the script makes the second scan report `came back` and `busmon --delete` exit 1.
- **Delete path:** `deleteProjectNow` is called with the row's name only, and `busmon --delete`'s
  tests pass unchanged through the extracted function. One Redis test against
  `AGENTBUS_TEST_REDIS_URL` checks that a page delete of `demo` leaves `demo-2:*` intact.
- **Backup first:** a failing backup stub stops the delete; a passing one shows the archive path,
  then the typed field.
- **Runs:** `Z`, `x` and `b` call the runner with exactly `--project <p> suspend --yes` /
  `resume --yes`, `down <p>`, and `backup --project <p>`; a second action while a run goes is
  refused.
- **Agents:** `busmon --delete demo --yes` and `--reset` exit with the refusal when
  `AGENT_BUS_AGENT` is set, or when a fake ancestry names an agent. A table-driven roles test
  checks that each spelling (`--delete demo`, `-delete demo`, `-d demo`, `-d=demo`, `--d demo`,
  `--yes --delete demo`, `-p x -d demo`, `-H h --delete=demo`, the same for reset) is denied by
  the shared rules, as deliver's test does, and that `busmon --list` and `busmon -p x` are not.
- **Memory (step 6):** on a disposable Redis, the sum equals the per-key `MEMORY USAGE` totals and
  no command carries `SAMPLES 0`; a stubbed short deadline cut mid-read gives the partial form,
  and one cut during the scan gives the "scan stopped" form, never `?`; the cap stops the scan; a
  key deleted between the scan and its read counts as read; a refused `MEMORY` shows `?`; a late
  answer for a closed detail never lands on another project's detail; the list never calls it.
- **Hosts (step 7):** `{p}:services` with a marker on `vdr` and records on `vdr` and this machine:
  the panel names `vdr` only, with its kinds, as reported; none: `units reported: this machine
  only`; both forms carry the "other hosts may keep units" line; the done notice repeats it after
  the DEL.
- **Files (step 8), on a temp tree:** with `delete_files` on, the template goes (a linked template:
  the link only); `roles.toml` never; the worktrees named in `.git/worktrees/*/gitdir` inside
  `working_dir` go with their metadata, one outside is kept, `working_dir` stays; nothing runs git
  (a repository config naming a program that leaves a mark). Worktree fixtures that must be kept:
  a `gitdir` naming a tracked directory with no `.git` file (`W/cmd/.git`); a `.git` file that
  points at another entry; a link on the way to `D`; a `gitdir` naming `W` itself. With
  `delete_backups` on, an archive
  whose `coverage.json` lists only the project goes, a shared one and an unreadable one stay; the
  backup is still offered, and the archive `b` made is kept and shown (`kept: <path> (made now)`),
  through a link to it too, while an older archive of the project alone goes; with `n`, none is
  kept. The path is read from the run's `backup: <path>` line with `backup.after_command` set and
  retention pruning (the log's last line is then not a path): the fresh archive is still found and
  kept; an earlier run's line in the same log is never taken. A log with no such line, or a path
  that is not an existing regular file: no archive removed, the panel says so. With keys that came back, no file is touched. Both off: nothing but keys.

## 10. The person's answers (2026-11-09)

1. **No forced delete.** A live project is never deleted. The one path is to put it to sleep
   (`Z`), then delete it (`d`). That is how steps 1–4 shipped.
2. **`MEMORY USAGE` in the detail only** (`enter`), never in the list (§3.3).
3. **Files too, as an option.** Two settings, off by default, in `config/keys.go`, `config.toml`
   and busmon's settings screen (`C`): `projects.delete_files` for the template and the worktrees,
   `projects.delete_backups` for the backups (§5.5). `roles.toml` is in neither (Q6).
4. **`M`** opens the page, as shipped.
5. **Remote broker:** a delete is allowed for a sleeping or stopped project, since
   `deleteIfQuiet` covers every host. The panel names the hosts where units remain, from
   `{p}:services` (§5.2).

**Answered on 2026-11-10** (task `projects-admin-answers-3`):

6. **`roles.toml` stays.** It is one file for every project on the machine and is never deleted;
   `delete_files` covers the template and the worktrees only.
7. **The fresh archive is kept.** With `delete_backups` on, the backup is still offered, and the
   archive made just before the delete is never erased: the one exception. The panel shows its
   path (§5.5). (The spec had recommended skipping the offer instead.)

The questions as they were put (2026-11-08):

1. **A forced delete?** Should there be a way to delete a project that is still live (§5.1),
   for example `D` with a third confirmation, or is "sleep it first" the only path?
2. **Memory size per project.** `MEMORY USAGE` on every key costs one call per key. Show it only
   in the detail (one project, on `enter`), or not at all? The recommendation is the detail only.
3. **More than the broker.** Should delete also offer to remove the template, the roles file, and
   the project's worktrees and backups? The recommendation is no in v1: those are files the
   person may want, and `backup` is the only place they can be restored from.
4. **The key.** Is `M` (manage) right, or would you rather use another letter? A menu entry
   alone is not an option, because busmon has no menu.
5. **Remote broker.** The broker-side checks of §5.1 refuse a delete while a dispatcher,
   conductor or service runs on **any** host. A suspended or stopped project can then be deleted
   from a remote broker; only this machine's units are removed, and the panel says
   `services on other hosts: none running; their units stay there`. Is that right, or should
   delete be refused on a remote broker altogether?

## 11. Steps (each its own subject for 4-eyes)

1. `deleteProjectNow` extracted from `main.go` (no behaviour change; `busmon --delete` tests
   unchanged), the agent-caller refusal for `--delete` and `--reset` with the shared check, and
   the §5.4 denies with their spelling test.
2. The page, read-only: rows, tiers, detail, `R`, keys table, goldens.
3. Actions: `Z`, `x`, `b` through `startRun`.
4. Delete: the guards, the panel, the typed name, the open-project second confirmation and the
   exit.
5. Docs: README's busmon keys, OPERATIONAL-GUIDE (and `.fr`), operator-notes (the live check, the
   remote case).
6. The memory size in the detail (§3.3), bounded, with its `?` and partial forms.
7. The hosts where units remain, in the delete panel and the done notice (§5.2).
8. The two options (§5.5): the settings, the template, the worktrees read as text and removed
   without git, the backups by their `coverage.json`, the panel lines and the backup offer, and
   `busmon --delete`. Q6 and Q7 answered (part 10): `roles.toml` never, the archive `b` made kept.
