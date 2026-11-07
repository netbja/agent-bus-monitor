# busmon's Projects page: list, sleep, stop, back up and delete projects (projects-admin)

Status: spec for review (subject `projects-admin-spec`), v0.29 chantier 6. No code on this branch.
Base `origin/main` b1671ee (home-inbox merged). coder is rewriting parts of `cmd/busmon`. This
spec names functions and seams, not line numbers, so it survives that rewrite.

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
- **"This machine".** Services and herdr are local. When busmon runs against a remote broker
  (`--host` through a tunnel), the header says `services and herdr: this machine (<hostname>)`,
  and a project with no local service shows `–`, which means "not here", not "none".

## 4. Actions

All actions work on the selected row. The page never acts on a name the person typed, except to
compare it with the selected row's name (§5.2).

| Key | Action | How | Allowed when |
|---|---|---|---|
| `enter` | detail: every column, the five streams' XLEN, the pause or suspend record (by, reason, since), the template path, the workspace id | read only | always |
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

### 5.1 Refused while the project is live

A live project recreates its keys: the next `agentbus status` from any of its agents writes
`{p}:status` again, and a running dispatcher writes `{p}:dispatch`. A delete then leaves a
half-project that the list shows again a second later. So `d` is **refused**, with the reason
and the way out on the notice line, while:

- a local service of the project is active → `stop its services first: x`;
- an agent is live by the bus's liveness rule → `N agents are live: Z puts the project to sleep`;
- its herdr workspace is open on this machine → `close its herdr workspace first (N panes)`;
- a run (`startRun`) is going → the existing "already running" notice.

A suspended project passes this check: its services are stopped and its agents are gone. That is
the expected path: `Z`, then `d`. Whether the person wants a forced delete that skips the check
is §10 Q1. This spec has none.

### 5.2 The confirmation: a backup offered, the name typed

`d` opens a confirmation panel over the page. It reuses the shell's dialog frame
(`tea_dialogs.go`), so no new widget is needed:

```
 Delete project 'demo'
 1 204 keys · 7 agents (0 live) · last activity 3d · services: none here · herdr: –
 This is a DEL, not a reset: streams, consumer groups, agents/usage/budget/verdicts,
 pilot and gate leases all go. The template and the worktrees stay.

 A backup first?  b back up, then continue · n no backup · esc cancel
```

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
   into it in place. That is the "project you run in" case. The agent's own project (`AGENT_BUS_PROJECT` in
   busmon's environment) is checked too, when it differs from the open one.
4. `esc` cancels at every step, and cancelling writes nothing.

### 5.3 The delete itself: the one path busmon --delete uses

The core of `main.go` `deleteProject` (services removed on this machine with `localServices`,
then `bus.DeleteProject`) becomes a function with no terminal I/O: `deleteProjectNow(ctx,
client, project, removeServices) (keys int64, services []string, err error)`. Both paths call it:
`busmon --delete` after `confirmDelete`, and the page after §5.2. The order stays as it is
(services first, so nothing restarts the project). So do the guards: `bus.ProjectKeys` refuses an
invalid name, and the `{p}:*` glob with its trailing colon cannot match a sibling (`demo` never
takes `demo-2`). The page runs it in a `tea.Cmd`, with a deadline of 30 s. The notice reports
`deleted 1 204 keys of demo; removed dispatch and missions services here`, and the list is read
again (the row disappears, or shows `template` when a template remains).

The template, the roles, the worktrees and the backups are **not** touched. Deleting those is a
different act (§10 Q3).

### 5.4 Agents cannot reach this

busmon is the person's interface, and an agent never drives it. But `busmon --delete <p> --yes`
is one Bash line for any agent, and today no `[tool_rules]` deny names it. This spec adds two
shared denies to `roles.toml`, of the plain form only, as `agentbus suspend*` has: `"Bash(busmon
--delete*)"` and `"Bash(busmon -d *)"`. The same goes for `--reset` (`"Bash(busmon --reset*)"`).
This is defence in depth, as the manifest says of every deny. The page itself adds no command an
agent could call.

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
- **Delete guards:** each of the §5.1 refusals; a typed name that differs by one byte (`demo `,
  `Demo`, `demo-2`) never deletes; `esc` at each step writes nothing; the open project needs the
  second `y`; after it, the model quits with the message (`tea.Quit`), and nothing else runs.
- **Delete path:** `deleteProjectNow` is called with the row's name only, and `busmon --delete`'s
  tests pass unchanged through the extracted function. One Redis test against
  `AGENTBUS_TEST_REDIS_URL` checks that a page delete of `demo` leaves `demo-2:*` intact.
- **Backup first:** a failing backup stub stops the delete; a passing one shows the archive path,
  then the typed field.
- **Runs:** `Z`, `x` and `b` call the runner with exactly `--project <p> suspend --yes` /
  `resume --yes`, `down <p>`, and `backup --project <p>`; a second action while a run goes is
  refused.
- **The manifest:** a roles test checks that the shared denies of §5.4 are present.

## 10. Questions for the person

1. **A forced delete?** Should there be a way to delete a project that is still live (§5.1),
   for example `D` with a third confirmation, or is "sleep it first" the only path?
2. **Memory size per project.** `MEMORY USAGE` on every key costs one call per key. Show it only
   in the detail (one project, on `enter`), or not at all? The recommendation is the detail only.
3. **More than the broker.** Should delete also offer to remove the template, the roles file, and
   the project's worktrees and backups? The recommendation is no in v1: those are files the
   person may want, and `backup` is the only place they can be restored from.
4. **The key.** Is `M` (manage) right, or would you rather use another letter? A menu entry
   alone is not an option, because busmon has no menu.
5. **Remote broker.** When busmon runs against another host's broker, should the page offer
   delete at all? It can remove only this machine's services (§3.3). The recommendation is to
   allow it and say so in the panel.

## 11. Steps (each its own subject for 4-eyes)

1. `deleteProjectNow` extracted from `main.go` (no behaviour change; `busmon --delete` tests
   unchanged) + the §5.4 denies.
2. The page, read-only: rows, tiers, detail, `R`, keys table, goldens.
3. Actions: `Z`, `x`, `b` through `startRun`.
4. Delete: the guards, the panel, the typed name, the open-project second confirmation and the
   exit.
5. Docs: README's busmon keys, OPERATIONAL-GUIDE (and `.fr`), operator-notes (the live check, the
   remote case).
